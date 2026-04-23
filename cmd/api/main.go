package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"deep-backend/internal/auth"
	"deep-backend/internal/config"
	"deep-backend/internal/http/handler"
	mw "deep-backend/internal/http/middleware"
	"deep-backend/internal/jobs"
	"deep-backend/internal/media"
	"deep-backend/internal/service"
	"deep-backend/internal/storage"
	"deep-backend/internal/store"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

func main() {
	_ = godotenv.Load()

	// YouTube cookies from gzipped+base64 env var to avoid huge env payloads.
	if b64 := os.Getenv("YT_COOKIES_GZ_B64"); b64 != "" {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "[startup] WARNING: YT_COOKIES_GZ_B64 is set but failed to decode:", err)
		} else {
			gr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				fmt.Fprintln(os.Stderr, "[startup] WARNING: failed to open gzip cookies payload:", err)
			} else {
				data, err := io.ReadAll(gr)
				_ = gr.Close()
				if err != nil {
					fmt.Fprintln(os.Stderr, "[startup] WARNING: failed to gunzip YouTube cookies:", err)
				} else if err := os.WriteFile("/tmp/yt_cookies.txt", data, 0o600); err != nil {
					fmt.Fprintln(os.Stderr, "[startup] WARNING: failed to write YouTube cookies file:", err)
				} else {
					fmt.Fprintf(os.Stderr, "[startup] YouTube cookies written to /tmp/yt_cookies.txt (%d bytes)\n", len(data))
				}
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "[startup] YT_COOKIES_GZ_B64 not set")
	}

	log, _ := zap.NewProduction()
	defer log.Sync() //nolint:errcheck

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config load failed", zap.Error(err))
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal("db connect failed", zap.Error(err))
	}
	defer pool.Close()
	log.Info("database connected")

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		log.Fatal("migrations failed", zap.Error(err))
	}
	log.Info("migrations applied")

	jobStore := store.NewMediaJobStore(pool)
	variantStore := store.NewMediaVariantStore(pool)
	assetStore := store.NewOutputAssetStore(pool)
	eventStore := store.NewJobEventStore(pool)
	sourceStore := store.NewSourceRequestStore(pool)

	var storageBackend storage.Backend
	switch cfg.StorageBackend {
	case "s3":
		if cfg.S3AccessKey == "" || cfg.S3SecretKey == "" || cfg.S3Bucket == "" {
			log.Fatal("s3 backend requires S3_ACCESS_KEY, S3_SECRET_KEY, S3_BUCKET")
		}
		storageBackend, err = storage.NewS3Backend(ctx, storage.S3Config{
			Endpoint:        cfg.S3Endpoint,
			Region:          cfg.S3Region,
			AccessKeyID:     cfg.S3AccessKey,
			SecretAccessKey: cfg.S3SecretKey,
			Bucket:          cfg.S3Bucket,
			PublicURLPrefix: cfg.S3PublicURL,
		})
		if err != nil {
			log.Fatal("s3 storage init failed", zap.Error(err))
		}
		log.Info("storage: cloudflare R2 / S3", zap.String("bucket", cfg.S3Bucket), zap.String("endpoint", cfg.S3Endpoint))
	default:
		storageBackend, err = storage.NewLocalBackend(cfg.LocalStoragePath, cfg.PublicBaseURL)
		if err != nil {
			log.Fatal("local storage init failed", zap.Error(err))
		}
		log.Info("storage: local filesystem", zap.String("path", cfg.LocalStoragePath))
	}

	prober := media.NewProber(cfg.FFprobePath)
	analyzer := media.NewAnalyzer(prober)
	processor := media.NewProcessor(cfg.FFmpegPath, cfg.TempDir)

	baseURL := cfg.PublicBaseURL
	svc := service.NewMediaService(jobStore, variantStore, assetStore, eventStore, sourceStore, baseURL)
	tokenSvc := auth.NewTokenService(cfg.JWTSecret, cfg.JWTExpiry, cfg.JWTEnabled)
	h := handler.New(svc, storageBackend, tokenSvc, log)

	r := chi.NewRouter()
	r.Use(chiMiddleware.RealIP)
	r.Use(mw.RequestID)
	r.Use(mw.Logger(log))
	r.Use(mw.Recover(log))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/healthz", h.Healthz)
	r.Get("/readyz", h.Readyz)
	r.Get("/v1/assets/dl/{token}", h.DownloadByToken)

	r.Group(func(r chi.Router) {
		r.Use(tokenSvc.Middleware)
		r.With(httprate.LimitByIP(20, time.Minute)).Post("/v1/analyze", h.Analyze)
		r.Get("/v1/jobs/{id}", h.GetJob)
		r.Get("/v1/jobs/{id}/variants", h.GetVariants)
		r.Post("/v1/jobs/{id}/actions/extract-audio", h.ExtractAudio)
		r.Post("/v1/jobs/{id}/actions/merge", h.Merge)
		r.Post("/v1/jobs/{id}/actions/transcode", h.Transcode)
		r.Get("/v1/assets/{id}", h.GetAsset)
	})

	workerCtx, workerCancel := context.WithCancel(ctx)
	pool_ := jobs.NewPool(cfg.WorkerCount, cfg.PollInterval, jobs.WorkerConfig{
		Jobs:      jobStore,
		Variants:  variantStore,
		Assets:    assetStore,
		Events:    eventStore,
		Sources:   sourceStore,
		Analyzer:  analyzer,
		Processor: processor,
		Storage:   storageBackend,
		MaxRetry:  cfg.MaxRetries,
		Timeout:   cfg.JobTimeout,
		Log:       log,
		BaseURL:   baseURL,
	}, log)
	go pool_.Start(workerCtx)

	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-t.C:
				jobs.CleanupAssets(ctx, assetStore, storageBackend, cfg.AssetTTL, log)
			}
		}
	}()

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("server starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutdown signal received")
	workerCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown error", zap.Error(err))
	}
	log.Info("server stopped")
}

func runMigrations(databaseURL string) error {
	m, err := migrate.New("file://migrations", databaseURL)
	if err != nil {
		return fmt.Errorf("migrate new: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

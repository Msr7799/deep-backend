package main

import (
	"context"
	"encoding/base64"
	"fmt"
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
	// ── Load .env (ignored in production) ──
	_ = godotenv.Load()

	// ── YouTube cookies (anti-bot bypass) ──
	// Set YT_COOKIES_B64 on Render to the base64-encoded contents of your
	// Netscape-format cookies file exported from a logged-in browser session.
	// Generate with: base64 -w 0 youtube_cookies.txt
	if b64 := os.Getenv("YT_COOKIES_B64"); b64 != "" {
		if data, err := base64.StdEncoding.DecodeString(b64); err == nil {
			if err := os.WriteFile("/tmp/yt_cookies.txt", data, 0600); err == nil {
				// logger not ready yet — write to stderr so it appears in Render logs
				fmt.Fprintln(os.Stderr, "[startup] YouTube cookies written to /tmp/yt_cookies.txt")
			}
		} else {
			fmt.Fprintln(os.Stderr, "[startup] WARNING: YT_COOKIES_B64 is set but failed to decode:", err)
		}
	}

	// ── Logger ──
	log, _ := zap.NewProduction()
	defer log.Sync() //nolint:errcheck

	// ── Config ──
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config load failed", zap.Error(err))
	}

	// ── Database ──
	ctx := context.Background()
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal("db connect failed", zap.Error(err))
	}
	defer pool.Close()
	log.Info("database connected")

	// ── Migrations ──
	if err := runMigrations(cfg.DatabaseURL); err != nil {
		log.Fatal("migrations failed", zap.Error(err))
	}
	log.Info("migrations applied")

	// ── Stores ──
	jobStore := store.NewMediaJobStore(pool)
	variantStore := store.NewMediaVariantStore(pool)
	assetStore := store.NewOutputAssetStore(pool)
	eventStore := store.NewJobEventStore(pool)
	sourceStore := store.NewSourceRequestStore(pool)

	// ── Storage backend ──
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
		log.Info("storage: cloudflare R2 / S3",
			zap.String("bucket", cfg.S3Bucket),
			zap.String("endpoint", cfg.S3Endpoint),
		)
	default:
		storageBackend, err = storage.NewLocalBackend(
			cfg.LocalStoragePath,
			cfg.PublicBaseURL,
		)
		if err != nil {
			log.Fatal("local storage init failed", zap.Error(err))
		}
		log.Info("storage: local filesystem", zap.String("path", cfg.LocalStoragePath))
	}

	// ── Media services ──
	prober := media.NewProber(cfg.FFprobePath)
	analyzer := media.NewAnalyzer(prober)
	processor := media.NewProcessor(cfg.FFmpegPath, cfg.TempDir)

	// ── Business service ──
	baseURL := cfg.PublicBaseURL
	svc := service.NewMediaService(jobStore, variantStore, assetStore, eventStore, sourceStore, baseURL)

	// ── Auth ──
	tokenSvc := auth.NewTokenService(cfg.JWTSecret, cfg.JWTExpiry, cfg.JWTEnabled)

	// ── HTTP handler ──
	h := handler.New(svc, storageBackend, tokenSvc, log)

	// ── Router ──
	r := chi.NewRouter()

	// Global middleware
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

	// Health probes (unauthenticated)
	r.Get("/healthz", h.Healthz)
	r.Get("/readyz", h.Readyz)

	// Asset download (token-authenticated, no JWT needed)
	r.Get("/v1/assets/dl/{token}", h.DownloadByToken)

	// API v1 – optionally JWT-protected
	r.Group(func(r chi.Router) {
		r.Use(tokenSvc.Middleware)

		// Rate-limit job creation: 20 req/min per IP
		r.With(httprate.LimitByIP(20, time.Minute)).Post("/v1/analyze", h.Analyze)

		r.Get("/v1/jobs/{id}", h.GetJob)
		r.Get("/v1/jobs/{id}/variants", h.GetVariants)

		r.Post("/v1/jobs/{id}/actions/extract-audio", h.ExtractAudio)
		r.Post("/v1/jobs/{id}/actions/merge", h.Merge)
		r.Post("/v1/jobs/{id}/actions/transcode", h.Transcode)

		r.Get("/v1/assets/{id}", h.GetAsset)
	})

	// ── Worker pool ──
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

	// ── Asset cleanup ticker ──
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

	// ── HTTP server ──
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

	// ── Graceful shutdown ──
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

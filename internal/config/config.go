package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	Port            string
	ShutdownTimeout time.Duration

	// Database
	DatabaseURL string

	// Storage
	StorageBackend   string // "local" | "s3"
	LocalStoragePath string
	S3Bucket         string
	S3Region         string
	S3Endpoint       string // custom endpoint for R2/MinIO
	S3AccessKey      string
	S3SecretKey      string
	S3PublicURL      string // optional CDN/public URL prefix for R2

	// Auth
	JWTSecret     string
	JWTExpiry     time.Duration
	JWTEnabled    bool

	// Media
	FFmpegPath  string
	FFprobePath string
	TempDir     string

	// Jobs
	WorkerCount    int
	PollInterval   time.Duration
	JobTimeout     time.Duration
	MaxRetries     int

	// CORS
	AllowedOrigins []string

	// Public URL (used to build download links returned to clients)
	// Set PUBLIC_BASE_URL=https://your-domain.com in production.
	// Falls back to http://localhost:<PORT> for local dev.
	PublicBaseURL string

	// Asset cleanup
	AssetTTL time.Duration
}

// Load reads configuration from environment variables.
// It returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{
		Port:             getEnv("PORT", "8080"),
		ShutdownTimeout:  getDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
		DatabaseURL:      mustGetEnv("DATABASE_URL"),
		StorageBackend:   getEnv("STORAGE_BACKEND", "local"),
		LocalStoragePath: getEnv("LOCAL_STORAGE_PATH", "./tmp/storage"),
		S3Bucket:         getEnv("S3_BUCKET", ""),
		S3Region:         getEnv("S3_REGION", "auto"),
		S3Endpoint:       getEnv("S3_ENDPOINT", ""),
		S3AccessKey:      getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:      getEnv("S3_SECRET_KEY", ""),
		S3PublicURL:      getEnv("S3_PUBLIC_URL", ""),
		JWTSecret:        getEnv("JWT_SECRET", ""),
		JWTExpiry:        getDuration("JWT_EXPIRY", 24*time.Hour),
		JWTEnabled:       getBool("JWT_ENABLED", false),
		FFmpegPath:       getEnv("FFMPEG_PATH", "ffmpeg"),
		FFprobePath:      getEnv("FFPROBE_PATH", "ffprobe"),
		TempDir:          getEnv("TEMP_DIR", "./tmp/jobs"),
		WorkerCount:      getInt("WORKER_COUNT", 4),
		PollInterval:     getDuration("POLL_INTERVAL", 2*time.Second),
		JobTimeout:       getDuration("JOB_TIMEOUT", 30*time.Minute),
		MaxRetries:       getInt("MAX_RETRIES", 3),
		AllowedOrigins:   []string{getEnv("ALLOWED_ORIGINS", "*")},
		AssetTTL:         getDuration("ASSET_TTL", 24*time.Hour),
	}

	// PublicBaseURL: اقرأ من البيئة أو استخدم localhost كافتراضي للتطوير
	if pub := getEnv("PUBLIC_BASE_URL", ""); pub != "" {
		cfg.PublicBaseURL = strings.TrimRight(pub, "/")
	} else {
		cfg.PublicBaseURL = fmt.Sprintf("http://localhost:%s", cfg.Port)
	}

	if cfg.JWTEnabled && cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET must be set when JWT_ENABLED=true")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func getInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

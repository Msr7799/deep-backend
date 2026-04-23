package storage

import (
	"context"
	"io"
)

// Backend is an abstract file-storage interface.
// Implementations: LocalBackend (dev) and S3Backend (prod / Cloudflare R2).
type Backend interface {
	// Store saves the reader content at the given key and returns the storage key.
	Store(ctx context.Context, key string, r io.Reader, mimeType string) (string, error)

	// Open returns a reader for the stored object.
	Open(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes a stored object.
	Delete(ctx context.Context, key string) error

	// SignedURL returns a time-limited pre-signed URL for direct download.
	// For local backend this is an authenticated endpoint URL.
	SignedURL(ctx context.Context, key string, ttl int64) (string, error)

	// PublicURL returns the permanent public URL (if configured).
	PublicURL(key string) string
}

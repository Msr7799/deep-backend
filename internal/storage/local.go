package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// LocalBackend stores files on the local filesystem.
// baseURL is prepended to keys to form a download URL (e.g. http://localhost:8080).
type LocalBackend struct {
	root    string
	baseURL string
}

// NewLocalBackend creates a local storage backend.
// root is the absolute directory; baseURL is the server's public root.
func NewLocalBackend(root, baseURL string) (*LocalBackend, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("local storage: mkdir %s: %w", root, err)
	}
	return &LocalBackend{root: root, baseURL: baseURL}, nil
}

func (b *LocalBackend) Store(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	dst := filepath.Join(b.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return key, nil
}

func (b *LocalBackend) Open(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(b.root, filepath.FromSlash(key)))
}

func (b *LocalBackend) Delete(_ context.Context, key string) error {
	return os.Remove(filepath.Join(b.root, filepath.FromSlash(key)))
}

// SignedURL generates a token-authenticated URL.
// For local dev we simply embed the key + an expiry timestamp into the path.
func (b *LocalBackend) SignedURL(_ context.Context, key string, ttlSeconds int64) (string, error) {
	exp := time.Now().Unix() + ttlSeconds
	// Real signing omitted for brevity; use a HMAC in production.
	return fmt.Sprintf("%s/v1/assets/download-local?key=%s&exp=%d", b.baseURL, key, exp), nil
}

func (b *LocalBackend) PublicURL(key string) string {
	return fmt.Sprintf("%s/v1/assets/download-local?key=%s", b.baseURL, key)
}

// AbsPath returns the full filesystem path for a key (used by media workers).
func (b *LocalBackend) AbsPath(key string) string {
	return filepath.Join(b.root, filepath.FromSlash(key))
}

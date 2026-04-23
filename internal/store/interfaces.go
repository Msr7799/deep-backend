package store

import (
	"context"
	"time"

	"deep-backend/internal/domain"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────
//  Repository interfaces
// ─────────────────────────────────────────────

// SourceRequestStore persists and queries source requests.
type SourceRequestStore interface {
	Create(ctx context.Context, sr *domain.SourceRequest) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.SourceRequest, error)
}

// MediaJobStore persists and queries media jobs.
type MediaJobStore interface {
	Create(ctx context.Context, job *domain.MediaJob) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.MediaJob, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.JobStatus, progress int, stage string) error
	UpdateError(ctx context.Context, id uuid.UUID, msg string) error
	IncrementRetry(ctx context.Context, id uuid.UUID) error

	// Dequeue returns the next queued job and transitions it to the given status atomically.
	Dequeue(ctx context.Context) (*domain.MediaJob, error)
	ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.MediaJob, error)
}

// MediaVariantStore persists and queries media variants.
type MediaVariantStore interface {
	BulkCreate(ctx context.Context, variants []*domain.MediaVariant) error
	ListByJobID(ctx context.Context, jobID uuid.UUID) ([]*domain.MediaVariant, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.MediaVariant, error)
}

// OutputAssetStore persists and queries output assets.
type OutputAssetStore interface {
	Create(ctx context.Context, asset *domain.OutputAsset) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.OutputAsset, error)
	GetByJobID(ctx context.Context, jobID uuid.UUID) (*domain.OutputAsset, error)
	GetByDownloadToken(ctx context.Context, token string) (*domain.OutputAsset, error)
	UpdateSignedURL(ctx context.Context, id uuid.UUID, url string, expiresAt time.Time) error
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// JobEventStore appends and queries job event logs.
type JobEventStore interface {
	Append(ctx context.Context, event *domain.JobEvent) error
	ListByJobID(ctx context.Context, jobID uuid.UUID) ([]*domain.JobEvent, error)
}

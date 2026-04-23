package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"deep-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─────────────────────────────────────────────
//  DB pool factory
// ─────────────────────────────────────────────

// NewPool creates a pgx connection pool from a connection string.
// It automatically enables SSL (Neon requires it) and sets conservative
// pool limits that work well with Neon's connection pooler (pgbouncer).
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	// Neon / pgbouncer: disable prepared statements in pooling mode.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return pool, nil
}

func toJSONB(v any) string {
	if v == nil {
		return "{}"
	}

	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return "{}"
	}

	return string(b)
}

// ─────────────────────────────────────────────
//  SourceRequestStore
// ─────────────────────────────────────────────

type pgSourceRequestStore struct{ db *pgxpool.Pool }

func NewSourceRequestStore(db *pgxpool.Pool) SourceRequestStore {
	return &pgSourceRequestStore{db: db}
}

func (s *pgSourceRequestStore) Create(ctx context.Context, sr *domain.SourceRequest) error {
	if sr.ID == uuid.Nil {
		sr.ID = uuid.New()
	}
	sr.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(ctx,
		`INSERT INTO source_requests (id, user_id, source_url, created_at)
         VALUES ($1,$2,$3,$4)`,
		sr.ID, sr.UserID, sr.SourceURL, sr.CreatedAt,
	)
	return err
}

func (s *pgSourceRequestStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.SourceRequest, error) {
	sr := &domain.SourceRequest{}
	err := s.db.QueryRow(ctx,
		`SELECT id, user_id, source_url, created_at FROM source_requests WHERE id=$1`, id,
	).Scan(&sr.ID, &sr.UserID, &sr.SourceURL, &sr.CreatedAt)
	if err != nil {
		return nil, err
	}
	return sr, nil
}

// ─────────────────────────────────────────────
//  MediaJobStore
// ─────────────────────────────────────────────

type pgMediaJobStore struct{ db *pgxpool.Pool }

func NewMediaJobStore(db *pgxpool.Pool) MediaJobStore {
	return &pgMediaJobStore{db: db}
}

func (s *pgMediaJobStore) Create(ctx context.Context, job *domain.MediaJob) error {
	if job.ID == uuid.Nil {
		job.ID = uuid.New()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	job.Status = domain.JobStatusQueued

	meta := toJSONB(job.Metadata)

	_, err := s.db.Exec(ctx,
		`INSERT INTO media_jobs
         (id, user_id, source_request_id, job_type, status,
          progress_percent, progress_stage, metadata, retry_count, created_at, updated_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11)`,
		job.ID, job.UserID, job.SourceRequestID, job.JobType, job.Status,
		0, "", meta, 0, job.CreatedAt, job.UpdatedAt,
	)
	return err
}

func (s *pgMediaJobStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.MediaJob, error) {
	job := &domain.MediaJob{}
	var metaRaw []byte
	err := s.db.QueryRow(ctx,
		`SELECT id, user_id, source_request_id, job_type, status,
                progress_percent, progress_stage, error_message, metadata, retry_count,
                created_at, updated_at
         FROM media_jobs WHERE id=$1`, id,
	).Scan(
		&job.ID, &job.UserID, &job.SourceRequestID, &job.JobType, &job.Status,
		&job.ProgressPercent, &job.ProgressStage, &job.ErrorMessage, &metaRaw, &job.RetryCount,
		&job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(metaRaw) > 0 {
		_ = json.Unmarshal(metaRaw, &job.Metadata)
	}
	return job, nil
}

func (s *pgMediaJobStore) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.JobStatus, progress int, stage string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE media_jobs
         SET status=$2, progress_percent=$3, progress_stage=$4, updated_at=$5
         WHERE id=$1`,
		id, status, progress, stage, time.Now().UTC(),
	)
	return err
}

func (s *pgMediaJobStore) UpdateError(ctx context.Context, id uuid.UUID, msg string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE media_jobs
         SET status=$2, error_message=$3, updated_at=$4
         WHERE id=$1`,
		id, domain.JobStatusFailed, msg, time.Now().UTC(),
	)
	return err
}

func (s *pgMediaJobStore) IncrementRetry(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE media_jobs SET retry_count=retry_count+1, status=$2, updated_at=$3 WHERE id=$1`,
		id, domain.JobStatusQueued, time.Now().UTC(),
	)
	return err
}

// Dequeue atomically picks the oldest queued job and marks it as processing.
// Uses SELECT … FOR UPDATE SKIP LOCKED for safe concurrent workers.
func (s *pgMediaJobStore) Dequeue(ctx context.Context) (*domain.MediaJob, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	job := &domain.MediaJob{}
	var metaRaw []byte
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, source_request_id, job_type, status,
                progress_percent, progress_stage, error_message, metadata, retry_count,
                created_at, updated_at
         FROM media_jobs
         WHERE status=$1
         ORDER BY created_at ASC
         LIMIT 1
         FOR UPDATE SKIP LOCKED`,
		domain.JobStatusQueued,
	).Scan(
		&job.ID, &job.UserID, &job.SourceRequestID, &job.JobType, &job.Status,
		&job.ProgressPercent, &job.ProgressStage, &job.ErrorMessage, &metaRaw, &job.RetryCount,
		&job.CreatedAt, &job.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx,
		`UPDATE media_jobs SET status=$2, updated_at=$3 WHERE id=$1`,
		job.ID, domain.JobStatusAnalyzing, now,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	job.Status = domain.JobStatusAnalyzing
	job.UpdatedAt = now
	if len(metaRaw) > 0 {
		_ = json.Unmarshal(metaRaw, &job.Metadata)
	}
	return job, nil
}

func (s *pgMediaJobStore) ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.MediaJob, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, user_id, source_request_id, job_type, status,
                progress_percent, progress_stage, error_message, metadata, retry_count,
                created_at, updated_at
         FROM media_jobs WHERE user_id=$1
         ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*domain.MediaJob
	for rows.Next() {
		job := &domain.MediaJob{}
		var metaRaw []byte
		if err := rows.Scan(
			&job.ID, &job.UserID, &job.SourceRequestID, &job.JobType, &job.Status,
			&job.ProgressPercent, &job.ProgressStage, &job.ErrorMessage, &metaRaw, &job.RetryCount,
			&job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &job.Metadata)
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ─────────────────────────────────────────────
//  MediaVariantStore
// ─────────────────────────────────────────────

type pgMediaVariantStore struct{ db *pgxpool.Pool }

func NewMediaVariantStore(db *pgxpool.Pool) MediaVariantStore {
	return &pgMediaVariantStore{db: db}
}

func (s *pgMediaVariantStore) BulkCreate(ctx context.Context, variants []*domain.MediaVariant) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, v := range variants {
		if v.ID == uuid.Nil {
			v.ID = uuid.New()
		}

		meta := toJSONB(v.Metadata)

		_, err := tx.Exec(ctx,
			`INSERT INTO media_variants
             (id, media_job_id, label, container, codec_video, codec_audio,
              bitrate, width, height, duration_ms,
              is_audio_only, is_video_only, is_adaptive, source_url, mime_type, metadata)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb)`,
			v.ID, v.MediaJobID, v.Label, v.Container, v.CodecVideo, v.CodecAudio,
			v.Bitrate, v.Width, v.Height, v.DurationMs,
			v.IsAudioOnly, v.IsVideoOnly, v.IsAdaptive, v.SourceURL, v.MimeType, meta,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *pgMediaVariantStore) ListByJobID(ctx context.Context, jobID uuid.UUID) ([]*domain.MediaVariant, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, media_job_id, label, container, codec_video, codec_audio,
                bitrate, width, height, duration_ms,
                is_audio_only, is_video_only, is_adaptive, source_url, mime_type, metadata
         FROM media_variants WHERE media_job_id=$1 ORDER BY bitrate DESC`,
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []*domain.MediaVariant
	for rows.Next() {
		v := &domain.MediaVariant{}
		var metaRaw []byte
		if err := rows.Scan(
			&v.ID, &v.MediaJobID, &v.Label, &v.Container, &v.CodecVideo, &v.CodecAudio,
			&v.Bitrate, &v.Width, &v.Height, &v.DurationMs,
			&v.IsAudioOnly, &v.IsVideoOnly, &v.IsAdaptive, &v.SourceURL, &v.MimeType, &metaRaw,
		); err != nil {
			return nil, err
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &v.Metadata)
		}
		variants = append(variants, v)
	}
	return variants, rows.Err()
}

func (s *pgMediaVariantStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.MediaVariant, error) {
	v := &domain.MediaVariant{}
	var metaRaw []byte
	err := s.db.QueryRow(ctx,
		`SELECT id, media_job_id, label, container, codec_video, codec_audio,
                bitrate, width, height, duration_ms,
                is_audio_only, is_video_only, is_adaptive, source_url, mime_type, metadata
         FROM media_variants WHERE id=$1`, id,
	).Scan(
		&v.ID, &v.MediaJobID, &v.Label, &v.Container, &v.CodecVideo, &v.CodecAudio,
		&v.Bitrate, &v.Width, &v.Height, &v.DurationMs,
		&v.IsAudioOnly, &v.IsVideoOnly, &v.IsAdaptive, &v.SourceURL, &v.MimeType, &metaRaw,
	)
	if err != nil {
		return nil, err
	}
	if len(metaRaw) > 0 {
		_ = json.Unmarshal(metaRaw, &v.Metadata)
	}
	return v, nil
}

// ─────────────────────────────────────────────
//  OutputAssetStore
// ─────────────────────────────────────────────

type pgOutputAssetStore struct{ db *pgxpool.Pool }

func NewOutputAssetStore(db *pgxpool.Pool) OutputAssetStore {
	return &pgOutputAssetStore{db: db}
}

func (s *pgOutputAssetStore) Create(ctx context.Context, asset *domain.OutputAsset) error {
	if asset.ID == uuid.Nil {
		asset.ID = uuid.New()
	}
	asset.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(ctx,
		`INSERT INTO output_assets
         (id, media_job_id, storage_key, filename, mime_type, size_bytes,
          download_token, signed_url, signed_url_expires_at, created_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		asset.ID, asset.MediaJobID, asset.StorageKey, asset.Filename, asset.MimeType, asset.SizeBytes,
		asset.DownloadToken, asset.SignedURL, asset.SignedURLExpires, asset.CreatedAt,
	)
	return err
}

func (s *pgOutputAssetStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.OutputAsset, error) {
	a := &domain.OutputAsset{}
	err := s.db.QueryRow(ctx,
		`SELECT id, media_job_id, storage_key, filename, mime_type, size_bytes,
                download_token, signed_url, signed_url_expires_at, created_at
         FROM output_assets WHERE id=$1`, id,
	).Scan(
		&a.ID, &a.MediaJobID, &a.StorageKey, &a.Filename, &a.MimeType, &a.SizeBytes,
		&a.DownloadToken, &a.SignedURL, &a.SignedURLExpires, &a.CreatedAt,
	)
	return a, err
}

func (s *pgOutputAssetStore) GetByJobID(ctx context.Context, jobID uuid.UUID) (*domain.OutputAsset, error) {
	a := &domain.OutputAsset{}
	err := s.db.QueryRow(ctx,
		`SELECT id, media_job_id, storage_key, filename, mime_type, size_bytes,
                download_token, signed_url, signed_url_expires_at, created_at
         FROM output_assets WHERE media_job_id=$1 ORDER BY created_at DESC LIMIT 1`, jobID,
	).Scan(
		&a.ID, &a.MediaJobID, &a.StorageKey, &a.Filename, &a.MimeType, &a.SizeBytes,
		&a.DownloadToken, &a.SignedURL, &a.SignedURLExpires, &a.CreatedAt,
	)
	return a, err
}

func (s *pgOutputAssetStore) GetByDownloadToken(ctx context.Context, token string) (*domain.OutputAsset, error) {
	a := &domain.OutputAsset{}
	err := s.db.QueryRow(ctx,
		`SELECT id, media_job_id, storage_key, filename, mime_type, size_bytes,
                download_token, signed_url, signed_url_expires_at, created_at
         FROM output_assets WHERE download_token=$1`, token,
	).Scan(
		&a.ID, &a.MediaJobID, &a.StorageKey, &a.Filename, &a.MimeType, &a.SizeBytes,
		&a.DownloadToken, &a.SignedURL, &a.SignedURLExpires, &a.CreatedAt,
	)
	return a, err
}

func (s *pgOutputAssetStore) UpdateSignedURL(ctx context.Context, id uuid.UUID, url string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx,
		`UPDATE output_assets SET signed_url=$2, signed_url_expires_at=$3 WHERE id=$1`,
		id, url, expiresAt,
	)
	return err
}

func (s *pgOutputAssetStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM output_assets WHERE created_at < $1`, cutoff,
	)
	return tag.RowsAffected(), err
}

// ─────────────────────────────────────────────
//  JobEventStore
// ─────────────────────────────────────────────

type pgJobEventStore struct{ db *pgxpool.Pool }

func NewJobEventStore(db *pgxpool.Pool) JobEventStore {
	return &pgJobEventStore{db: db}
}

func (s *pgJobEventStore) Append(ctx context.Context, event *domain.JobEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	event.CreatedAt = time.Now().UTC()

	payload := toJSONB(event.Payload)

	_, err := s.db.Exec(ctx,
		`INSERT INTO job_events (id, job_id, event_type, message, payload, created_at)
         VALUES ($1,$2,$3,$4,$5::jsonb,$6)`,
		event.ID, event.JobID, event.EventType, event.Message, payload, event.CreatedAt,
	)
	return err
}

func (s *pgJobEventStore) ListByJobID(ctx context.Context, jobID uuid.UUID) ([]*domain.JobEvent, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, job_id, event_type, message, payload, created_at
         FROM job_events WHERE job_id=$1 ORDER BY created_at ASC`, jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*domain.JobEvent
	for rows.Next() {
		e := &domain.JobEvent{}
		var payloadRaw []byte
		if err := rows.Scan(&e.ID, &e.JobID, &e.EventType, &e.Message, &payloadRaw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(payloadRaw) > 0 {
			_ = json.Unmarshal(payloadRaw, &e.Payload)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
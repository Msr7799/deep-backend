package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"deep-backend/internal/domain"
	"deep-backend/internal/media"
	"deep-backend/internal/store"

	"github.com/google/uuid"
)

// MediaService orchestrates business logic between stores.
// Handlers call this; this calls stores. No DB code in handlers.
type MediaService struct {
	jobs     store.MediaJobStore
	variants store.MediaVariantStore
	assets   store.OutputAssetStore
	events   store.JobEventStore
	sources  store.SourceRequestStore
	baseURL  string
}

func NewMediaService(
	jobs store.MediaJobStore,
	variants store.MediaVariantStore,
	assets store.OutputAssetStore,
	events store.JobEventStore,
	sources store.SourceRequestStore,
	baseURL string,
) *MediaService {
	return &MediaService{
		jobs: jobs, variants: variants, assets: assets,
		events: events, sources: sources, baseURL: baseURL,
	}
}

// ─────────────────────────────────────────────
//  Analyze
// ─────────────────────────────────────────────

// SubmitAnalyze validates the URL, creates a SourceRequest, enqueues an analyze job,
// and returns the job immediately.
func (s *MediaService) SubmitAnalyze(ctx context.Context, rawURL string, userID *uuid.UUID) (*domain.MediaJob, error) {
	if err := validateURL(rawURL); err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	sr := &domain.SourceRequest{
		UserID:    userID,
		SourceURL: rawURL,
	}
	if err := s.sources.Create(ctx, sr); err != nil {
		return nil, fmt.Errorf("create source request: %w", err)
	}

	job := &domain.MediaJob{
		UserID:          userID,
		SourceRequestID: sr.ID,
		JobType:         domain.JobTypeAnalyze,
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return job, nil
}

// ─────────────────────────────────────────────
//  Job / Variant / Asset queries
// ─────────────────────────────────────────────

func (s *MediaService) GetJob(ctx context.Context, id uuid.UUID) (*domain.MediaJob, error) {
	return s.jobs.GetByID(ctx, id)
}

func (s *MediaService) GetVariants(ctx context.Context, jobID uuid.UUID) ([]*domain.MediaVariant, error) {
	return s.variants.ListByJobID(ctx, jobID)
}

func (s *MediaService) GetAsset(ctx context.Context, id uuid.UUID) (*domain.OutputAsset, error) {
	return s.assets.GetByID(ctx, id)
}

func (s *MediaService) GetAssetByToken(ctx context.Context, token string) (*domain.OutputAsset, error) {
	return s.assets.GetByDownloadToken(ctx, token)
}

func (s *MediaService) GetJobAsset(ctx context.Context, jobID uuid.UUID) (*domain.OutputAsset, error) {
	return s.assets.GetByJobID(ctx, jobID)
}

// ─────────────────────────────────────────────
//  Actions
// ─────────────────────────────────────────────

// SubmitExtractAudio creates a processing job for audio extraction from a variant.
func (s *MediaService) SubmitExtractAudio(ctx context.Context, analysisJobID uuid.UUID, variantID uuid.UUID, userID *uuid.UUID) (*domain.MediaJob, error) {
	v, err := s.variants.GetByID(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("variant not found: %w", err)
	}

	// Get original source request
	parentJob, err := s.jobs.GetByID(ctx, analysisJobID)
	if err != nil {
		return nil, fmt.Errorf("parent job: %w", err)
	}

	job := &domain.MediaJob{
		UserID:          userID,
		SourceRequestID: parentJob.SourceRequestID,
		JobType:         domain.JobTypeExtractAudio,
		Metadata: map[string]any{
			"source_url": v.SourceURL,
			"variant_id": variantID.String(),
		},
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// SubmitMerge creates a merge job combining a video-only and audio-only variant.
func (s *MediaService) SubmitMerge(ctx context.Context, analysisJobID uuid.UUID, videoVariantID, audioVariantID uuid.UUID, userID *uuid.UUID) (*domain.MediaJob, error) {
	vv, err := s.variants.GetByID(ctx, videoVariantID)
	if err != nil {
		return nil, fmt.Errorf("video variant not found: %w", err)
	}
	av, err := s.variants.GetByID(ctx, audioVariantID)
	if err != nil {
		return nil, fmt.Errorf("audio variant not found: %w", err)
	}

	parentJob, err := s.jobs.GetByID(ctx, analysisJobID)
	if err != nil {
		return nil, fmt.Errorf("parent job: %w", err)
	}

	job := &domain.MediaJob{
		UserID:          userID,
		SourceRequestID: parentJob.SourceRequestID,
		JobType:         domain.JobTypeMerge,
		Metadata: map[string]any{
			"video_url":        vv.SourceURL,
			"audio_url":        av.SourceURL,
			"video_variant_id": videoVariantID.String(),
			"audio_variant_id": audioVariantID.String(),
		},
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// SubmitTranscode creates a transcode job.
func (s *MediaService) SubmitTranscode(ctx context.Context, analysisJobID uuid.UUID, variantID uuid.UUID, userID *uuid.UUID) (*domain.MediaJob, error) {
	v, err := s.variants.GetByID(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("variant not found: %w", err)
	}

	parentJob, err := s.jobs.GetByID(ctx, analysisJobID)
	if err != nil {
		return nil, fmt.Errorf("parent job: %w", err)
	}

	job := &domain.MediaJob{
		UserID:          userID,
		SourceRequestID: parentJob.SourceRequestID,
		JobType:         domain.JobTypeTranscode,
		Metadata: map[string]any{
			"source_url": v.SourceURL,
			"variant_id": variantID.String(),
		},
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// DownloadURL generates a short-lived signed download URL for an asset.
func (s *MediaService) DownloadURL(asset *domain.OutputAsset) string {
	if asset.DownloadToken != "" {
		return fmt.Sprintf("%s/v1/assets/dl/%s", s.baseURL, asset.DownloadToken)
	}
	return asset.SignedURL
}

// AssetTTLCleanup removes expired assets from the DB.
func (s *MediaService) AssetTTLCleanup(ctx context.Context, ttl time.Duration) error {
	cutoff := time.Now().UTC().Add(-ttl)
	_, err := s.assets.DeleteOlderThan(ctx, cutoff)
	return err
}

// ─────────────────────────────────────────────
//  Validation helpers
// ─────────────────────────────────────────────

// validateURL ensures the URL is well-formed and uses http/https.
func validateURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("malformed url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must use http or https scheme")
	}
	if u.Host == "" {
		return fmt.Errorf("url host is empty")
	}
	// Basic allowlist check - block localhost/private ranges in production
	if isPrivateHost(u.Host) {
		return fmt.Errorf("url host is not allowed")
	}
	return nil
}

func isPrivateHost(host string) bool {
	blocked := []string{"localhost", "127.", "192.168.", "10.", "172.16.", "::1", "0.0.0.0"}
	for _, b := range blocked {
		if strings.HasPrefix(strings.ToLower(host), b) {
			return true
		}
	}
	return false
}

// AnalysisJobForSource checks if an active analyze job exists for a source URL.
// Returns nil if none found (no error). Useful for de-duplication.
func (s *MediaService) AnalysisJobForSource(_ context.Context, _ string) *domain.MediaJob {
	return nil // simplification; implement caching/de-dup here if needed
}

// BuildAssetResponse builds the AssetResponse DTO for API output.
func (s *MediaService) BuildAssetResponse(asset *domain.OutputAsset) domain.AssetResponse {
	return domain.AssetResponse{
		ID:          asset.ID.String(),
		Filename:    asset.Filename,
		MimeType:    asset.MimeType,
		SizeBytes:   asset.SizeBytes,
		DownloadURL: s.DownloadURL(asset),
		ExpiresAt:   asset.SignedURLExpires,
	}
}

// BuildVariantResponse builds the VariantResponse DTO for API output.
func BuildVariantResponse(v *domain.MediaVariant) domain.VariantResponse {
	return domain.ToVariantResponse(v)
}

// ValidateSourceURLCtx wraps media.ValidateSourceURL with a context.
func ValidateSourceURLCtx(ctx context.Context, rawURL string) error {
	return media.ValidateSourceURL(ctx, rawURL)
}

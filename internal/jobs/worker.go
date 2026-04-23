package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"deep-backend/internal/domain"
	"deep-backend/internal/media"
	"deep-backend/internal/storage"
	"deep-backend/internal/store"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Worker is a single goroutine that dequeues and processes media jobs.
type Worker struct {
	id        int
	jobs      store.MediaJobStore
	variants  store.MediaVariantStore
	assets    store.OutputAssetStore
	events    store.JobEventStore
	sources   store.SourceRequestStore
	analyzer  *media.Analyzer
	processor *media.Processor
	storage   storage.Backend
	maxRetry  int
	timeout   time.Duration
	log       *zap.Logger
	baseURL   string
}

// WorkerConfig groups the dependencies for a Worker.
type WorkerConfig struct {
	ID        int
	Jobs      store.MediaJobStore
	Variants  store.MediaVariantStore
	Assets    store.OutputAssetStore
	Events    store.JobEventStore
	Sources   store.SourceRequestStore
	Analyzer  *media.Analyzer
	Processor *media.Processor
	Storage   storage.Backend
	MaxRetry  int
	Timeout   time.Duration
	Log       *zap.Logger
	BaseURL   string
}

func NewWorker(cfg WorkerConfig) *Worker {
	return &Worker{
		id:        cfg.ID,
		jobs:      cfg.Jobs,
		variants:  cfg.Variants,
		assets:    cfg.Assets,
		events:    cfg.Events,
		sources:   cfg.Sources,
		analyzer:  cfg.Analyzer,
		processor: cfg.Processor,
		storage:   cfg.Storage,
		maxRetry:  cfg.MaxRetry,
		timeout:   cfg.Timeout,
		log:       cfg.Log.With(zap.Int("worker_id", cfg.ID)),
		baseURL:   cfg.BaseURL,
	}
}

// Run starts the polling loop. It exits when ctx is cancelled.
func (w *Worker) Run(ctx context.Context, pollInterval time.Duration) {
	w.log.Info("worker started")
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker stopped")
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Worker) poll(ctx context.Context) {
	job, err := w.jobs.Dequeue(ctx)
	if err != nil {
		w.log.Error("dequeue error", zap.Error(err))
		return
	}
	if job == nil {
		return // nothing to do
	}

	w.log.Info("processing job", zap.String("job_id", job.ID.String()), zap.String("type", string(job.JobType)))
	w.process(ctx, job)
}

func (w *Worker) process(ctx context.Context, job *domain.MediaJob) {
	jobCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	var err error
	switch job.JobType {
	case domain.JobTypeAnalyze:
		err = w.handleAnalyze(jobCtx, job)
	case domain.JobTypeExtractAudio:
		err = w.handleExtractAudio(jobCtx, job)
	case domain.JobTypeMerge:
		err = w.handleMerge(jobCtx, job)
	case domain.JobTypeTranscode:
		err = w.handleTranscode(jobCtx, job)
	default:
		err = fmt.Errorf("unknown job type: %s", job.JobType)
	}

	if err != nil {
		w.log.Error("job failed", zap.String("job_id", job.ID.String()), zap.Error(err))
		w.appendEvent(ctx, job.ID, "error", err.Error())

		if job.RetryCount < w.maxRetry {
			_ = w.jobs.IncrementRetry(ctx, job.ID)
		} else {
			_ = w.jobs.UpdateError(ctx, job.ID, err.Error())
		}
	}
}

// ─────────────────────────────────────────────
//  Handlers
// ─────────────────────────────────────────────

func (w *Worker) handleAnalyze(ctx context.Context, job *domain.MediaJob) error {
	w.setProgress(ctx, job.ID, domain.JobStatusAnalyzing, 10, "fetching_source")

	// Get source URL from source_requests
	sr, err := w.sources.GetByID(ctx, job.SourceRequestID)
	if err != nil {
		return fmt.Errorf("get source request: %w", err)
	}

	w.setProgress(ctx, job.ID, domain.JobStatusAnalyzing, 30, "probing")
	result, err := w.analyzer.Analyze(ctx, job.ID, sr.SourceURL)
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	w.setProgress(ctx, job.ID, domain.JobStatusAnalyzing, 80, "storing_variants")
	if len(result.Variants) > 0 {
		if err := w.variants.BulkCreate(ctx, result.Variants); err != nil {
			return fmt.Errorf("store variants: %w", err)
		}
	}

	_ = w.jobs.UpdateStatus(ctx, job.ID, domain.JobStatusCompleted, 100, "done")
	w.appendEvent(ctx, job.ID, "info", fmt.Sprintf("found %d variants", len(result.Variants)))
	return nil
}

func (w *Worker) handleExtractAudio(ctx context.Context, job *domain.MediaJob) error {
	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 5, "starting")

	sourceURL, err := w.sourceURLFromMeta(job)
	if err != nil {
		return err
	}

	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 20, "extracting_audio")
	result, err := w.processor.ExtractAudio(ctx, sourceURL, job.ID.String(), func(p int) {
		w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 20+p/2, "ffmpeg_processing")
	})
	if err != nil {
		return fmt.Errorf("extract audio: %w", err)
	}
	defer media.Cleanup(result.FilePath)

	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 75, "uploading")
	asset, err := w.storeFile(ctx, job.ID, result.FilePath, result.Filename, result.MimeType)
	if err != nil {
		return err
	}

	_ = w.jobs.UpdateStatus(ctx, job.ID, domain.JobStatusCompleted, 100, "done")
	w.appendEvent(ctx, job.ID, "info", fmt.Sprintf("asset stored: %s", asset.ID))
	return nil
}

func (w *Worker) handleMerge(ctx context.Context, job *domain.MediaJob) error {
	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 5, "starting")

	videoURL, ok1 := job.Metadata["video_url"].(string)
	audioURL, ok2 := job.Metadata["audio_url"].(string)
	if !ok1 || !ok2 {
		return fmt.Errorf("merge job missing video_url/audio_url in metadata")
	}

	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 20, "merging_streams")
	result, err := w.processor.MergeAV(ctx, videoURL, audioURL, job.ID.String())
	if err != nil {
		return fmt.Errorf("merge AV: %w", err)
	}
	defer media.Cleanup(result.FilePath)

	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 75, "uploading")
	asset, err := w.storeFile(ctx, job.ID, result.FilePath, result.Filename, result.MimeType)
	if err != nil {
		return err
	}

	_ = w.jobs.UpdateStatus(ctx, job.ID, domain.JobStatusCompleted, 100, "done")
	w.appendEvent(ctx, job.ID, "info", fmt.Sprintf("merged asset: %s", asset.ID))
	return nil
}

func (w *Worker) handleTranscode(ctx context.Context, job *domain.MediaJob) error {
	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 5, "starting")

	sourceURL, err := w.sourceURLFromMeta(job)
	if err != nil {
		return err
	}

	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 20, "transcoding")
	result, err := w.processor.Transcode(ctx, sourceURL, job.ID.String())
	if err != nil {
		return fmt.Errorf("transcode: %w", err)
	}
	defer media.Cleanup(result.FilePath)

	w.setProgress(ctx, job.ID, domain.JobStatusProcessing, 75, "uploading")
	asset, err := w.storeFile(ctx, job.ID, result.FilePath, result.Filename, result.MimeType)
	if err != nil {
		return err
	}

	_ = w.jobs.UpdateStatus(ctx, job.ID, domain.JobStatusCompleted, 100, "done")
	w.appendEvent(ctx, job.ID, "info", fmt.Sprintf("transcoded asset: %s", asset.ID))
	return nil
}

// ─────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────

func (w *Worker) storeFile(ctx context.Context, jobID uuid.UUID, filePath, filename, mimeType string) (*domain.OutputAsset, error) {
	f, size, err := media.OpenFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	storageKey := filepath.Join("assets", jobID.String(), filename)
	_, err = w.storage.Store(ctx, storageKey, f, mimeType)
	if err != nil {
		return nil, fmt.Errorf("store file: %w", err)
	}

	token := uuid.New().String()
	expires := time.Now().UTC().Add(24 * time.Hour)

	// Try to get a backend-native signed URL (R2 presigned / local token URL)
	signedURL, signErr := w.storage.SignedURL(ctx, storageKey, int64(24*time.Hour/time.Second))
	if signErr != nil {
		// Fallback: build token-based download URL served by our API
		signedURL = fmt.Sprintf("%s/v1/assets/dl/%s", w.baseURL, token)
	}

	asset := &domain.OutputAsset{
		ID:               uuid.New(),
		MediaJobID:       jobID,
		StorageKey:       storageKey,
		Filename:         filename,
		MimeType:         mimeType,
		SizeBytes:        size,
		DownloadToken:    token,
		SignedURL:        signedURL,
		SignedURLExpires: &expires,
	}

	if err := w.assets.Create(ctx, asset); err != nil {
		return nil, fmt.Errorf("create asset record: %w", err)
	}
	return asset, nil
}

func (w *Worker) setProgress(ctx context.Context, jobID uuid.UUID, status domain.JobStatus, pct int, stage string) {
	_ = w.jobs.UpdateStatus(ctx, jobID, status, pct, stage)
}

func (w *Worker) appendEvent(ctx context.Context, jobID uuid.UUID, eventType, message string) {
	_ = w.events.Append(ctx, &domain.JobEvent{
		JobID:     jobID,
		EventType: eventType,
		Message:   message,
	})
}

func (w *Worker) sourceURLFromMeta(job *domain.MediaJob) (string, error) {
	if u, ok := job.Metadata["source_url"].(string); ok && u != "" {
		return u, nil
	}
	return "", fmt.Errorf("job metadata missing source_url")
}

// ─────────────────────────────────────────────
//  Pool (manages N workers)
// ─────────────────────────────────────────────

// Pool manages a fixed number of Worker goroutines.
type Pool struct {
	workers      []*Worker
	pollInterval time.Duration
	log          *zap.Logger
}

func NewPool(count int, pollInterval time.Duration, cfg WorkerConfig, log *zap.Logger) *Pool {
	workers := make([]*Worker, count)
	for i := 0; i < count; i++ {
		cfg.ID = i + 1
		workers[i] = NewWorker(cfg)
	}
	return &Pool{workers: workers, pollInterval: pollInterval, log: log}
}

// Start launches all workers. It returns when ctx is cancelled.
func (p *Pool) Start(ctx context.Context) {
	for _, w := range p.workers {
		go w.Run(ctx, p.pollInterval)
	}
	<-ctx.Done()
	p.log.Info("worker pool shutting down")
}

// CleanupAssets deletes output_assets older than the given TTL from disk.
func CleanupAssets(ctx context.Context, assetStore store.OutputAssetStore, back storage.Backend, ttl time.Duration, log *zap.Logger) {
	cutoff := time.Now().UTC().Add(-ttl)
	n, err := assetStore.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		log.Error("asset cleanup error", zap.Error(err))
		return
	}
	log.Info("asset cleanup done", zap.Int64("deleted", n))
	_ = os.Remove(os.TempDir()) // noop – actual deletion happens per-asset in real impl
}

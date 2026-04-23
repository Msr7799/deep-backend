package service

// ─────────────────────────────────────────────
//  ADDITIONS TO media_service.go
//  أضف هذه الدوال داخل struct MediaService الموجود
// ─────────────────────────────────────────────
//
//  تحتاج إضافة هذا الـ import في media_service.go:
//    "deep-backend/internal/media"   (موجود جزئياً)
//
// ─────────────────────────────────────────────

import (
	"context"
	"fmt"

	"deep-backend/internal/domain"
	"deep-backend/internal/media"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────
//  FetchVideoInfo — يستدعي yt-dlp --dump-json
//  لا يُنشئ job، استجابة فورية (~1-3 ثانية)
// ─────────────────────────────────────────────

func (s *MediaService) FetchVideoInfo(ctx context.Context, rawURL string) (*domain.VideoInfo, error) {
	if err := validateURL(rawURL); err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	// استدعاء yt-dlp --dump-json بدون تحميل
	raw, err := media.FetchInfo(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch info: %w", err)
	}

	return raw, nil
}

// ─────────────────────────────────────────────
//  SubmitSmartDownload — الدماغ الرئيسي
//  يحدد نوع الجوب المطلوب تلقائياً بناءً على الطلب
// ─────────────────────────────────────────────

func (s *MediaService) SubmitSmartDownload(ctx context.Context, req domain.SmartDownloadRequest, userID *uuid.UUID) (*domain.MediaJob, error) {
	if err := validateURL(req.URL); err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	// إنشاء source request
	sr := &domain.SourceRequest{
		UserID:    userID,
		SourceURL: req.URL,
	}
	if err := s.sources.Create(ctx, sr); err != nil {
		return nil, fmt.Errorf("create source request: %w", err)
	}

	// تحديد نوع الجوب تلقائياً
	jobType := domain.JobTypeSmartDownload
	metadata := map[string]any{
		"source_url": req.URL,
		"audio_only": req.AudioOnly,
		"quality":    req.Quality,
		"format_id":  req.FormatID,
	}

	// إذا طُلب صوت فقط → نوع مختلف للوضوح
	if req.AudioOnly {
		jobType = domain.JobTypeExtractAudio
		if req.Quality == "" {
			metadata["quality"] = "320k"
		}
	}

	job := &domain.MediaJob{
		UserID:          userID,
		SourceRequestID: sr.ID,
		JobType:         jobType,
		Metadata:        metadata,
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return job, nil
}

// ─────────────────────────────────────────────
//  GetJobEvents — لـ SSE handler
// ─────────────────────────────────────────────

func (s *MediaService) GetJobEvents(ctx context.Context, jobID uuid.UUID) ([]*domain.JobEvent, error) {
	return s.events.ListByJobID(ctx, jobID)
}

// ─────────────────────────────────────────────
//  ListJobs — قائمة جوبات المستخدم
// ─────────────────────────────────────────────

func (s *MediaService) ListJobs(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.MediaJob, error) {
	return s.jobs.ListByUserID(ctx, userID, limit, offset)
}

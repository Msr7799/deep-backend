package domain

import (
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────
//  Enums
// ─────────────────────────────────────────────

type JobType string

const (
	JobTypeAnalyze      JobType = "analyze"
	JobTypeExtractAudio JobType = "extract_audio"
	JobTypeMerge        JobType = "merge"
	JobTypeTranscode    JobType = "transcode"
	JobTypePackage      JobType = "package"
)

type JobStatus string

const (
	JobStatusQueued     JobStatus = "queued"
	JobStatusAnalyzing  JobStatus = "analyzing"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

// ─────────────────────────────────────────────
//  Core entities
// ─────────────────────────────────────────────

// User is a lightweight user record (JWT sub maps here).
type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// SourceRequest captures the original URL submitted by the client.
type SourceRequest struct {
	ID        uuid.UUID `json:"id"`
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	SourceURL string    `json:"source_url"`
	CreatedAt time.Time `json:"created_at"`
}

// MediaJob is the central entity for all async work.
type MediaJob struct {
	ID              uuid.UUID      `json:"id"`
	UserID          *uuid.UUID     `json:"user_id,omitempty"`
	SourceRequestID uuid.UUID      `json:"source_request_id"`
	JobType         JobType        `json:"job_type"`
	Status          JobStatus      `json:"status"`
	ProgressPercent int            `json:"progress_percent"`
	ProgressStage   string         `json:"progress_stage"`
	ErrorMessage    string         `json:"error_message,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	RetryCount      int            `json:"retry_count"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// MediaVariant describes one available stream/format from an analysis job.
type MediaVariant struct {
	ID           uuid.UUID      `json:"id"`
	MediaJobID   uuid.UUID      `json:"media_job_id"`
	Label        string         `json:"label"`
	Container    string         `json:"container"`
	CodecVideo   string         `json:"codec_video,omitempty"`
	CodecAudio   string         `json:"codec_audio,omitempty"`
	Bitrate      int64          `json:"bitrate,omitempty"`
	Width        int            `json:"width,omitempty"`
	Height       int            `json:"height,omitempty"`
	DurationMs   int64          `json:"duration_ms,omitempty"`
	IsAudioOnly  bool           `json:"is_audio_only"`
	IsVideoOnly  bool           `json:"is_video_only"`
	IsAdaptive   bool           `json:"is_adaptive"`
	SourceURL    string         `json:"source_url"`
	MimeType     string         `json:"mime_type,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// OutputAsset is the produced file stored in the configured backend.
type OutputAsset struct {
	ID               uuid.UUID  `json:"id"`
	MediaJobID       uuid.UUID  `json:"media_job_id"`
	StorageKey       string     `json:"storage_key"`
	Filename         string     `json:"filename"`
	MimeType         string     `json:"mime_type"`
	SizeBytes        int64      `json:"size_bytes"`
	SignedURL        string     `json:"signed_url,omitempty"`
	SignedURLExpires *time.Time `json:"signed_url_expires_at,omitempty"`
	DownloadToken    string     `json:"download_token,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// JobEvent records a log entry associated with a job (progress, errors, stages).
type JobEvent struct {
	ID        uuid.UUID `json:"id"`
	JobID     uuid.UUID `json:"job_id"`
	EventType string    `json:"event_type"` // "progress" | "error" | "stage" | "info"
	Message   string    `json:"message"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// APIToken is an optional bearer token for server-to-server auth.
type APIToken struct {
	ID        uuid.UUID  `json:"id"`
	UserID    uuid.UUID  `json:"user_id"`
	TokenHash string     `json:"-"`
	Label     string     `json:"label"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ─────────────────────────────────────────────
//  Request / Response DTOs
// ─────────────────────────────────────────────

type AnalyzeRequest struct {
	SourceURL string `json:"source_url"`
}

type ActionRequest struct {
	VariantID string         `json:"variant_id"`
	Options   map[string]any `json:"options,omitempty"`
}

type JobResponse struct {
	JobID           string    `json:"job_id"`
	Status          JobStatus `json:"status"`
	ProgressPercent int       `json:"progress_percent"`
	ProgressStage   string    `json:"progress_stage,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type VariantResponse struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Container   string `json:"container"`
	CodecVideo  string `json:"codec_video,omitempty"`
	CodecAudio  string `json:"codec_audio,omitempty"`
	Bitrate     int64  `json:"bitrate,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	IsAudioOnly bool   `json:"is_audio_only"`
	IsVideoOnly bool   `json:"is_video_only"`
	IsAdaptive  bool   `json:"is_adaptive"`
	MimeType    string `json:"mime_type,omitempty"`
}

type AssetResponse struct {
	ID          string     `json:"id"`
	Filename    string     `json:"filename"`
	MimeType    string     `json:"mime_type"`
	SizeBytes   int64      `json:"size_bytes"`
	DownloadURL string     `json:"download_url"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// ToJobResponse converts a domain MediaJob to its API response shape.
func ToJobResponse(j *MediaJob) JobResponse {
	return JobResponse{
		JobID:           j.ID.String(),
		Status:          j.Status,
		ProgressPercent: j.ProgressPercent,
		ProgressStage:   j.ProgressStage,
		ErrorMessage:    j.ErrorMessage,
		CreatedAt:       j.CreatedAt,
		UpdatedAt:       j.UpdatedAt,
	}
}

// ToVariantResponse converts a domain MediaVariant to its API response shape.
func ToVariantResponse(v *MediaVariant) VariantResponse {
	return VariantResponse{
		ID:          v.ID.String(),
		Label:       v.Label,
		Container:   v.Container,
		CodecVideo:  v.CodecVideo,
		CodecAudio:  v.CodecAudio,
		Bitrate:     v.Bitrate,
		Width:       v.Width,
		Height:      v.Height,
		DurationMs:  v.DurationMs,
		IsAudioOnly: v.IsAudioOnly,
		IsVideoOnly: v.IsVideoOnly,
		IsAdaptive:  v.IsAdaptive,
		MimeType:    v.MimeType,
	}
}

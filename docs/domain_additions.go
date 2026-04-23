package domain

// ─────────────────────────────────────────────
//  ADDITIONS TO domain.go
//  أضف هذه الأنواع في نهاية ملف domain.go
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Video Info (استجابة /v1/info)
// ─────────────────────────────────────────────

// VideoInfo هو الـ response الكامل لطلب معلومات الفيديو.
// يُشابه ما يعرضه موقع ytget.net من thumbnail وقائمة جودات.
type VideoInfo struct {
	Title        string          `json:"title"`
	ThumbnailURL string          `json:"thumbnail_url"`
	DurationSec  int64           `json:"duration_sec"`
	Uploader     string          `json:"uploader,omitempty"`
	ViewCount    int64           `json:"view_count,omitempty"`
	Platform     string          `json:"platform"` // "youtube" | "twitter" | etc.
	OriginalURL  string          `json:"original_url"`
	AudioTracks  []AudioTrack    `json:"audio_tracks"`
	VideoTracks  []VideoTrack    `json:"video_tracks"`
}

// AudioTrack يمثل جودة صوتية واحدة.
type AudioTrack struct {
	FormatID  string  `json:"format_id"`   // معرف yt-dlp
	Label     string  `json:"label"`        // "128k (mp3)"
	Container string  `json:"container"`    // "mp3" | "m4a" | "webm"
	Bitrate   int     `json:"bitrate_kbps"` // 128 | 320
	SizeBytes int64   `json:"size_bytes,omitempty"`
	MimeType  string  `json:"mime_type"`
}

// VideoTrack يمثل جودة فيديو واحدة.
type VideoTrack struct {
	FormatID    string `json:"format_id"`
	Label       string `json:"label"`         // "1080p"
	Container   string `json:"container"`     // "mp4" | "webm"
	CodecVideo  string `json:"codec_video"`   // "avc1" | "av01"
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	HasAudio    bool   `json:"has_audio"`    // false يعني يحتاج merge
	NeedsMerge  bool   `json:"needs_merge"`  // true لـ 1080p+ بدون صوت
	MimeType    string `json:"mime_type"`
}

// ─────────────────────────────────────────────
//  Smart Download Request/Job
// ─────────────────────────────────────────────

// SmartDownloadRequest هو body طلب POST /v1/download
type SmartDownloadRequest struct {
	URL       string `json:"url"`        // رابط YouTube/Twitter/etc
	FormatID  string `json:"format_id"`  // format_id من yt-dlp (اختياري)
	AudioOnly bool   `json:"audio_only"` // true = استخرج MP3 فقط
	Quality   string `json:"quality"`    // "best" | "1080p" | "720p" | "480p" | "360p" | "128k" | "320k"
}

// JobTypeSmartDownload نوع جوب جديد
const JobTypeSmartDownload JobType = "smart_download"

// ─────────────────────────────────────────────
//  SSE Progress Event
// ─────────────────────────────────────────────

// ProgressEvent يُبث عبر SSE أثناء معالجة الجوب
type ProgressEvent struct {
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Stage    string `json:"stage"`
}

package handler

// ─────────────────────────────────────────────
//  ADDITIONS TO handler.go
//  أضف هذه الدوال داخل ملف handler.go الموجود
// ─────────────────────────────────────────────
//
//  Import إضافية مطلوبة في handler.go:
//    "fmt"
//    "strconv"
//    "time"
//    "encoding/json"   (موجودة)
//    "net/http"        (موجودة)
//    "github.com/go-chi/chi/v5"   (موجودة)
//
// ─────────────────────────────────────────────

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"deep-backend/internal/auth"
	"deep-backend/internal/domain"
	mw "deep-backend/internal/http/middleware"
	"deep-backend/internal/service"
	"deep-backend/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────
//  GET /v1/info?url=<youtube_url>
//
//  يُرجع: عنوان الفيديو، الصورة المصغرة، المدة،
//         وقائمة كاملة بالجودات المتاحة مع أحجامها
//  لا يُنشئ job — استجابة فورية من yt-dlp --dump-json
// ─────────────────────────────────────────────

func (h *Handler) GetVideoInfo(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		mw.WriteError(w, http.StatusBadRequest, "url query param is required")
		return
	}

	info, err := h.svc.FetchVideoInfo(r.Context(), rawURL)
	if err != nil {
		h.log.Warn("fetch video info failed", zap.Error(err))
		mw.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	mw.WriteJSON(w, http.StatusOK, info)
}

// ─────────────────────────────────────────────
//  POST /v1/download
//
//  Body: { "url": "...", "format_id": "...", "audio_only": false }
//
//  الذكاء هنا:
//   • إذا format_id يحتوي صوت+فيديو → تحميل مباشر (merge)
//   • إذا audio_only=true → extract audio فقط
//   • إذا فيديو بدون صوت (1080p+) → auto merge مع أفضل صوت
//
//  يُنشئ job ويُرجعه فوراً، والعميل يتابع التقدم عبر /jobs/{id}
// ─────────────────────────────────────────────

func (h *Handler) SmartDownload(w http.ResponseWriter, r *http.Request) {
	var req domain.SmartDownloadRequest
	if err := decodeJSON(r, &req); err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.URL == "" {
		mw.WriteError(w, http.StatusBadRequest, "url is required")
		return
	}

	userID, _ := auth.UserIDFromContext(r.Context())
	var uid *uuid.UUID
	if userID != uuid.Nil {
		uid = &userID
	}

	job, err := h.svc.SubmitSmartDownload(r.Context(), req, uid)
	if err != nil {
		h.log.Warn("smart download failed", zap.Error(err))
		mw.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	mw.WriteJSON(w, http.StatusAccepted, domain.ToJobResponse(job))
}

// ─────────────────────────────────────────────
//  GET /v1/jobs/{id}/events  (Server-Sent Events)
//
//  يُبث تحديثات التقدم فوراً بدون polling
//  العميل يستمع إلى هذا الـ stream أثناء المعالجة
// ─────────────────────────────────────────────

func (h *Handler) JobEvents(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "id")
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	// التحقق من دعم SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		mw.WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // مهم لـ nginx

	ctx := r.Context()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	sentEvents := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// جلب الجوب الحالي
			job, err := h.svc.GetJob(ctx, id)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"job not found\"}\n\n")
				flusher.Flush()
				return
			}

			// جلب الأحداث الجديدة فقط
			events, err := h.svc.GetJobEvents(ctx, id)
			if err == nil && len(events) > sentEvents {
				for _, ev := range events[sentEvents:] {
					data, _ := json.Marshal(map[string]any{
						"type":    ev.EventType,
						"message": ev.Message,
						"time":    ev.CreatedAt,
					})
					fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
				}
				sentEvents = len(events)
			}

			// إرسال حالة التقدم
			progress := domain.ProgressEvent{
				JobID:    job.ID.String(),
				Status:   string(job.Status),
				Progress: job.ProgressPercent,
				Stage:    job.ProgressStage,
			}
			data, _ := json.Marshal(progress)
			fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data)
			flusher.Flush()

			// إذا انتهى الجوب → أرسل النتيجة النهائية وأغلق
			if job.Status == domain.JobStatusCompleted {
				if asset, err := h.svc.GetJobAsset(ctx, job.ID); err == nil {
					result := map[string]any{
						"job":   domain.ToJobResponse(job),
						"asset": h.svc.BuildAssetResponse(asset),
					}
					data, _ := json.Marshal(result)
					fmt.Fprintf(w, "event: done\ndata: %s\n\n", data)
					flusher.Flush()
				}
				return
			}

			if job.Status == domain.JobStatusFailed {
				data, _ := json.Marshal(map[string]string{
					"error": job.ErrorMessage,
				})
				fmt.Fprintf(w, "event: failed\ndata: %s\n\n", data)
				flusher.Flush()
				return
			}
		}
	}
}

// ─────────────────────────────────────────────
//  GET /v1/jobs  (قائمة جميع جوبات المستخدم)
// ─────────────────────────────────────────────

func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok || userID == uuid.Nil {
		mw.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 20
	offset := 0
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
		offset = o
	}

	jobs, err := h.svc.ListJobs(r.Context(), userID, limit, offset)
	if err != nil {
		mw.WriteError(w, http.StatusInternalServerError, "could not fetch jobs")
		return
	}

	resp := make([]domain.JobResponse, 0, len(jobs))
	for _, j := range jobs {
		resp = append(resp, domain.ToJobResponse(j))
	}

	mw.WriteJSON(w, http.StatusOK, map[string]any{
		"jobs":   resp,
		"limit":  limit,
		"offset": offset,
	})
}

// ─────────────────────────────────────────────
//  GET /v1/assets/download-local  (local backend فقط)
//  يُستخدم في بيئة التطوير لتقديم الملفات المحلية
// ─────────────────────────────────────────────

func (h *Handler) DownloadLocal(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		mw.WriteError(w, http.StatusBadRequest, "key is required")
		return
	}

	// التحقق من صلاحية التوقيت (exp)
	expStr := r.URL.Query().Get("exp")
	if expStr != "" {
		if exp, err := strconv.ParseInt(expStr, 10, 64); err == nil {
			if time.Now().Unix() > exp {
				mw.WriteError(w, http.StatusGone, "link expired")
				return
			}
		}
	}

	rc, err := h.storage.Open(r.Context(), key)
	if err != nil {
		h.log.Warn("local open failed", zap.String("key", key), zap.Error(err))
		mw.WriteError(w, http.StatusNotFound, "file not found")
		return
	}
	defer rc.Close()

	// تحديد نوع الملف من الامتداد
	mimeType := mimeFromKey(key)
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filenameFromKey(key)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// ─────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────

func mimeFromKey(key string) string {
	switch {
	case hasExt(key, ".mp4"):
		return "video/mp4"
	case hasExt(key, ".mp3"):
		return "audio/mpeg"
	case hasExt(key, ".m4a"):
		return "audio/mp4"
	case hasExt(key, ".webm"):
		return "video/webm"
	case hasExt(key, ".mkv"):
		return "video/x-matroska"
	default:
		return "application/octet-stream"
	}
}

func hasExt(key, ext string) bool {
	return len(key) > len(ext) && key[len(key)-len(ext):] == ext
}

func filenameFromKey(key string) string {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' || key[i] == '\\' {
			return key[i+1:]
		}
	}
	return key
}

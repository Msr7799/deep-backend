package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"deep-backend/internal/auth"
	"deep-backend/internal/domain"
	mw "deep-backend/internal/http/middleware"
	"deep-backend/internal/service"
	"deep-backend/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler holds all HTTP handler dependencies.
type Handler struct {
	svc     *service.MediaService
	storage storage.Backend
	auth    *auth.TokenService
	log     *zap.Logger
}

func New(svc *service.MediaService, stor storage.Backend, auth *auth.TokenService, log *zap.Logger) *Handler {
	return &Handler{svc: svc, storage: stor, auth: auth, log: log}
}

// ─────────────────────────────────────────────
//  Health / Readiness
// ─────────────────────────────────────────────

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	mw.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	mw.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// ─────────────────────────────────────────────
//  POST /v1/analyze
// ─────────────────────────────────────────────

func (h *Handler) Analyze(w http.ResponseWriter, r *http.Request) {
	var req domain.AnalyzeRequest
	if err := decodeJSON(r, &req); err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userID, _ := auth.UserIDFromContext(r.Context())
	var uid *uuid.UUID
	if userID != uuid.Nil {
		uid = &userID
	}

	job, err := h.svc.SubmitAnalyze(r.Context(), req.SourceURL, uid)
	if err != nil {
		h.log.Warn("analyze submit failed", zap.Error(err))
		mw.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	mw.WriteJSON(w, http.StatusAccepted, domain.ToJobResponse(job))
}

// ─────────────────────────────────────────────
//  GET /v1/jobs/{id}
// ─────────────────────────────────────────────

func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "id")
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	job, err := h.svc.GetJob(r.Context(), id)
	if err != nil {
		mw.WriteError(w, http.StatusNotFound, "job not found")
		return
	}

	resp := domain.ToJobResponse(job)

	// If completed, attach asset info
	if job.Status == domain.JobStatusCompleted {
		if asset, err := h.svc.GetJobAsset(r.Context(), job.ID); err == nil {
			mw.WriteJSON(w, http.StatusOK, map[string]any{
				"job":   resp,
				"asset": h.svc.BuildAssetResponse(asset),
			})
			return
		}
	}

	mw.WriteJSON(w, http.StatusOK, map[string]any{"job": resp})
}

// ─────────────────────────────────────────────
//  GET /v1/jobs/{id}/variants
// ─────────────────────────────────────────────

func (h *Handler) GetVariants(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "id")
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	variants, err := h.svc.GetVariants(r.Context(), id)
	if err != nil {
		mw.WriteError(w, http.StatusInternalServerError, "could not fetch variants")
		return
	}

	resp := make([]domain.VariantResponse, 0, len(variants))
	for _, v := range variants {
		resp = append(resp, domain.ToVariantResponse(v))
	}

	mw.WriteJSON(w, http.StatusOK, map[string]any{"variants": resp})
}

// ─────────────────────────────────────────────
//  POST /v1/jobs/{id}/actions/extract-audio
// ─────────────────────────────────────────────

func (h *Handler) ExtractAudio(w http.ResponseWriter, r *http.Request) {
	jobID, err := uuidParam(r, "id")
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	var req domain.ActionRequest
	if err := decodeJSON(r, &req); err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	variantID, err := uuid.Parse(req.VariantID)
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid variant_id")
		return
	}

	userID, _ := auth.UserIDFromContext(r.Context())
	var uid *uuid.UUID
	if userID != uuid.Nil {
		uid = &userID
	}

	job, err := h.svc.SubmitExtractAudio(r.Context(), jobID, variantID, uid)
	if err != nil {
		h.log.Warn("extract audio submit failed", zap.Error(err))
		mw.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	mw.WriteJSON(w, http.StatusAccepted, domain.ToJobResponse(job))
}

// ─────────────────────────────────────────────
//  POST /v1/jobs/{id}/actions/merge
// ─────────────────────────────────────────────

func (h *Handler) Merge(w http.ResponseWriter, r *http.Request) {
	jobID, err := uuidParam(r, "id")
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	var body struct {
		VideoVariantID string `json:"video_variant_id"`
		AudioVariantID string `json:"audio_variant_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	vvID, err := uuid.Parse(body.VideoVariantID)
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid video_variant_id")
		return
	}
	avID, err := uuid.Parse(body.AudioVariantID)
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid audio_variant_id")
		return
	}

	userID, _ := auth.UserIDFromContext(r.Context())
	var uid *uuid.UUID
	if userID != uuid.Nil {
		uid = &userID
	}

	job, err := h.svc.SubmitMerge(r.Context(), jobID, vvID, avID, uid)
	if err != nil {
		h.log.Warn("merge submit failed", zap.Error(err))
		mw.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	mw.WriteJSON(w, http.StatusAccepted, domain.ToJobResponse(job))
}

// ─────────────────────────────────────────────
//  POST /v1/jobs/{id}/actions/transcode
// ─────────────────────────────────────────────

func (h *Handler) Transcode(w http.ResponseWriter, r *http.Request) {
	jobID, err := uuidParam(r, "id")
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	var req domain.ActionRequest
	if err := decodeJSON(r, &req); err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	variantID, err := uuid.Parse(req.VariantID)
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid variant_id")
		return
	}

	userID, _ := auth.UserIDFromContext(r.Context())
	var uid *uuid.UUID
	if userID != uuid.Nil {
		uid = &userID
	}

	job, err := h.svc.SubmitTranscode(r.Context(), jobID, variantID, uid)
	if err != nil {
		h.log.Warn("transcode submit failed", zap.Error(err))
		mw.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	mw.WriteJSON(w, http.StatusAccepted, domain.ToJobResponse(job))
}

// ─────────────────────────────────────────────
//  GET /v1/assets/{id}
// ─────────────────────────────────────────────

func (h *Handler) GetAsset(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "id")
	if err != nil {
		mw.WriteError(w, http.StatusBadRequest, "invalid asset id")
		return
	}

	asset, err := h.svc.GetAsset(r.Context(), id)
	if err != nil {
		mw.WriteError(w, http.StatusNotFound, "asset not found")
		return
	}

	mw.WriteJSON(w, http.StatusOK, h.svc.BuildAssetResponse(asset))
}

// ─────────────────────────────────────────────
//  GET /v1/assets/dl/{token}  (token-authenticated download)
// ─────────────────────────────────────────────

func (h *Handler) DownloadByToken(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		mw.WriteError(w, http.StatusBadRequest, "missing token")
		return
	}

	asset, err := h.svc.GetAssetByToken(r.Context(), token)
	if err != nil {
		mw.WriteError(w, http.StatusNotFound, "asset not found or token expired")
		return
	}

	rc, err := h.storage.Open(r.Context(), asset.StorageKey)
	if err != nil {
		h.log.Error("open storage file", zap.Error(err), zap.String("key", asset.StorageKey))
		mw.WriteError(w, http.StatusInternalServerError, "file not available")
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", asset.MimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+asset.Filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// ─────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────

func uuidParam(r *http.Request, key string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, key))
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

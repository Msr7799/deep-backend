# ══════════════════════════════════════════════
#  Dockerfile – Deep Backend
#  مُحسَّن لـ Render.com
#  Multi-stage build: Go binary + ffmpeg + yt-dlp
# ══════════════════════════════════════════════

# ── Stage 1: Build Go binary ──────────────────
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -extldflags '-static'" \
    -o /app/server ./cmd/api

# ── Stage 2: Runtime image ────────────────────
FROM python:3.12-slim

# Install ffmpeg + system deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    ca-certificates \
    tzdata \
    wget \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install yt-dlp (latest stable)
RUN pip3 install --no-cache-dir yt-dlp

# Verify tools exist
RUN ffmpeg -version | head -1 && \
    ffprobe -version | head -1 && \
    yt-dlp --version

WORKDIR /app

# Copy compiled binary
COPY --from=builder /app/server /app/server

# Copy DB migrations
COPY migrations /app/migrations

# Create required directories
# Note: On Render use a Disk mount at /app/tmp for persistence
RUN mkdir -p /app/tmp/storage /app/tmp/jobs

# ── Environment defaults ──────────────────────
# All sensitive values should be set via Render's Environment Variables UI
ENV PORT=8080 \
    STORAGE_BACKEND=local \
    LOCAL_STORAGE_PATH=/app/tmp/storage \
    TEMP_DIR=/app/tmp/jobs \
    FFMPEG_PATH=ffmpeg \
    FFPROBE_PATH=ffprobe \
    GIN_MODE=release

# Render injects PORT automatically — make sure we listen on $PORT
EXPOSE 8080

# Health check (Render uses HTTP health checks at /healthz)
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD wget -qO- http://localhost:${PORT}/healthz || exit 1

ENTRYPOINT ["/app/server"]

# ── Build stage ──────────────────────────────
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /deep-backend ./cmd/api

# ── Runtime stage ─────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache \
    ffmpeg \
    ca-certificates \
    tzdata \
    python3 \
    py3-pip \
    && pip3 install --break-system-packages yt-dlp \
    && rm -rf /var/cache/apk/*

WORKDIR /app

COPY --from=builder /deep-backend /app/deep-backend
COPY migrations /app/migrations

# Storage and temp directories
RUN mkdir -p /app/tmp/storage /app/tmp/jobs

ENV PORT=8080 \
    STORAGE_BACKEND=local \
    LOCAL_STORAGE_PATH=/app/tmp/storage \
    TEMP_DIR=/app/tmp/jobs \
    FFMPEG_PATH=ffmpeg \
    FFPROBE_PATH=ffprobe

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/deep-backend"]

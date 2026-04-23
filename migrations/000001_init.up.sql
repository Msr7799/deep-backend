-- migrate: up

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ─────────────────────────────────────────────
--  users
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────
--  api_tokens (optional bearer tokens)
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS api_tokens (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    label      TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user_id ON api_tokens(user_id);

-- ─────────────────────────────────────────────
--  source_requests
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS source_requests (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    source_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_source_requests_user_id ON source_requests(user_id);

-- ─────────────────────────────────────────────
--  media_jobs
-- ─────────────────────────────────────────────
CREATE TYPE job_type   AS ENUM ('analyze','extract_audio','merge','transcode','package');
CREATE TYPE job_status AS ENUM ('queued','analyzing','processing','completed','failed');

CREATE TABLE IF NOT EXISTS media_jobs (
    id                UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id           UUID        REFERENCES users(id) ON DELETE SET NULL,
    source_request_id UUID        NOT NULL REFERENCES source_requests(id) ON DELETE CASCADE,
    job_type          job_type    NOT NULL,
    status            job_status  NOT NULL DEFAULT 'queued',
    progress_percent  SMALLINT    NOT NULL DEFAULT 0,
    progress_stage    TEXT        NOT NULL DEFAULT '',
    error_message     TEXT        NOT NULL DEFAULT '',
    metadata          JSONB       NOT NULL DEFAULT '{}',
    retry_count       SMALLINT    NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_media_jobs_status     ON media_jobs(status) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_media_jobs_user_id    ON media_jobs(user_id);
CREATE INDEX IF NOT EXISTS idx_media_jobs_created_at ON media_jobs(created_at);

-- ─────────────────────────────────────────────
--  media_variants
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS media_variants (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    media_job_id UUID NOT NULL REFERENCES media_jobs(id) ON DELETE CASCADE,
    label        TEXT NOT NULL DEFAULT '',
    container    TEXT NOT NULL DEFAULT '',
    codec_video  TEXT NOT NULL DEFAULT '',
    codec_audio  TEXT NOT NULL DEFAULT '',
    bitrate      BIGINT NOT NULL DEFAULT 0,
    width        INTEGER NOT NULL DEFAULT 0,
    height       INTEGER NOT NULL DEFAULT 0,
    duration_ms  BIGINT NOT NULL DEFAULT 0,
    is_audio_only BOOLEAN NOT NULL DEFAULT FALSE,
    is_video_only BOOLEAN NOT NULL DEFAULT FALSE,
    is_adaptive   BOOLEAN NOT NULL DEFAULT FALSE,
    source_url   TEXT NOT NULL,
    mime_type    TEXT NOT NULL DEFAULT '',
    metadata     JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_media_variants_job_id ON media_variants(media_job_id);

-- ─────────────────────────────────────────────
--  output_assets
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS output_assets (
    id                    UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    media_job_id          UUID        NOT NULL REFERENCES media_jobs(id) ON DELETE CASCADE,
    storage_key           TEXT        NOT NULL,
    filename              TEXT        NOT NULL,
    mime_type             TEXT        NOT NULL DEFAULT '',
    size_bytes            BIGINT      NOT NULL DEFAULT 0,
    download_token        TEXT        UNIQUE,
    signed_url            TEXT        NOT NULL DEFAULT '',
    signed_url_expires_at TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_output_assets_job_id        ON output_assets(media_job_id);
CREATE INDEX IF NOT EXISTS idx_output_assets_token         ON output_assets(download_token) WHERE download_token IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_output_assets_created_at    ON output_assets(created_at);

-- ─────────────────────────────────────────────
--  job_events
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS job_events (
    id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id     UUID        NOT NULL REFERENCES media_jobs(id) ON DELETE CASCADE,
    event_type TEXT        NOT NULL DEFAULT 'info',
    message    TEXT        NOT NULL DEFAULT '',
    payload    JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_job_events_job_id ON job_events(job_id);

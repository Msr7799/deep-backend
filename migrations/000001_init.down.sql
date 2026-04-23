-- migrate: down

DROP TABLE IF EXISTS job_events;
DROP TABLE IF EXISTS output_assets;
DROP TABLE IF EXISTS media_variants;
DROP TABLE IF EXISTS media_jobs;
DROP TABLE IF EXISTS source_requests;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS job_status;
DROP TYPE IF EXISTS job_type;

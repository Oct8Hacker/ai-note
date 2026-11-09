CREATE TABLE jobs (
  id                    UUID PRIMARY KEY,
  video_id              TEXT NOT NULL,
  youtube_url           TEXT NOT NULL,
  status                TEXT NOT NULL,           -- §4.1
  progress              SMALLINT NOT NULL DEFAULT 0,
  video_title           TEXT,
  duration_sec          INTEGER,
  notion_parent_id      TEXT,                    -- as submitted (nullable)
  notion_resolved_parent TEXT,                   -- page_id or data_source_id after resolution
  notion_parent_kind    TEXT,                    -- 'page' | 'data_source'
  notion_title_prop     TEXT,                    -- title property name for data_source parents
  notion_page_id        TEXT,
  notion_page_url       TEXT,
  notion_create_started_at TIMESTAMPTZ,          -- publish idempotency (§9.3)
  frames_requested      INTEGER,
  frames_published      INTEGER,
  attempts              INTEGER NOT NULL DEFAULT 0,   -- failure retries used
  rate_limit_retries    INTEGER NOT NULL DEFAULT 0,   -- 429 retries used
  deferrals             INTEGER NOT NULL DEFAULT 0,   -- lease-busy deferrals
  waiting_reason        TEXT,
  waiting_provider      TEXT,
  next_attempt_at       TIMESTAMPTZ,
  warnings              JSONB NOT NULL DEFAULT '[]',
  error_code            TEXT,
  error_message         TEXT,
  lease_owner           TEXT,                    -- worker id holding the job
  lease_until           TIMESTAMPTZ,
  idempotency_key       TEXT UNIQUE,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at           TIMESTAMPTZ
);
CREATE INDEX jobs_status_idx ON jobs(status);

CREATE TABLE job_artifacts (           -- stage checkpoints
  job_id     UUID REFERENCES jobs(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,            -- 'transcript' | 'synthesis_chunk:<n>' | 'synthesis'
  data       JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (job_id, kind)
);

CREATE TABLE job_frames (
  job_id              UUID REFERENCES jobs(id) ON DELETE CASCADE,
  idx                 INTEGER NOT NULL,        -- order in synthesis.visuals
  requested_sec       NUMERIC NOT NULL,        -- LLM-identified time (debug only)
  captured_sec        NUMERIC,                 -- actual capture time; used in captions/links/warnings
  section_index       INTEGER NOT NULL,
  caption             TEXT NOT NULL,
  reason              TEXT NOT NULL,
  status              TEXT NOT NULL,           -- 'pending'|'captured'|'failed'|'omitted'
  verified            BOOLEAN NOT NULL DEFAULT false,
  image_jpeg          BYTEA,
  notion_file_upload_id TEXT,
  notion_uploaded_at  TIMESTAMPTZ,
  warning_code        TEXT,
  PRIMARY KEY (job_id, idx)
);

CREATE TABLE job_events (
  id          BIGSERIAL PRIMARY KEY,
  job_id      UUID REFERENCES jobs(id) ON DELETE CASCADE,
  at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  type        TEXT NOT NULL,          -- 'transition'|'retry_scheduled'|'claimed'|'info'|...
  from_status TEXT,
  to_status   TEXT,
  worker_id   TEXT,
  detail      JSONB
);
CREATE INDEX job_events_job_idx ON job_events(job_id, at);

CREATE TABLE outbox (
  id            BIGSERIAL PRIMARY KEY,
  job_id        UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  payload       BYTEA,                -- message body (contains encrypted token); set to NULL once published
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at  TIMESTAMPTZ,
  CHECK (published_at IS NOT NULL OR payload IS NOT NULL)
);
CREATE INDEX outbox_unpublished_idx ON outbox(id) WHERE published_at IS NULL;

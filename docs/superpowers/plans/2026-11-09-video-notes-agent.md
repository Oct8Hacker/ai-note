# Distributed Multimodal Video Note-Taking Agent — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the system in `docs/SRS.md`: a Chrome extension submits YouTube videos to a Go gateway; a pool of Go workers fed by RabbitMQ turns each one into a Notion page of notes plus real, captioned video frames, while surviving rate limits, worker crashes, and partial failures.

**Architecture:** Monorepo. Go module `ainote` with two binaries (`cmd/gateway`, `cmd/worker`) sharing `internal/` packages; Postgres for job state and checkpoints, RabbitMQ (quorum work queue, TTL retry queues, DLX) for delivery, Redis for the shared rate limiter, worker liveness, and fault flags. Pipeline stages implement one `pipeline.Stage` interface; the worker runtime owns claiming, checkpoint commits, retries, and dead-lettering. A TypeScript MV3 extension (Vite + CRXJS + Preact) is the only user interface.

**Tech Stack:** Go 1.23+, `pgx/v5`, `golang-migrate/v4`, `rabbitmq/amqp091-go`, `redis/go-redis/v9`, `google/uuid`, `caarlos0/env/v11`, `stretchr/testify`, `testcontainers-go`, `google.golang.org/genai`; yt-dlp + ffmpeg; Docker Compose; TypeScript 5 (strict), Vite 5, `@crxjs/vite-plugin` 2.x, Preact, Vitest + jsdom + `@testing-library/preact`.

**Spec:** `docs/SRS.md` (v1.2). Requirement IDs below (FR-…, IR-…, NFR-…) refer to it — read the referenced section before starting a task.

**Owners and dates:** every task is tagged **[A]** (Person A — Platform & Reliability), **[B]** (Person B — Pipeline & Client), or **[A+B]**, with a target date matching SRS §17. Track A and Track B run in parallel after Phase 0.

## Global Constraints

- Go ≥ 1.23 (uses `net/http` method+path patterns, e.g. `mux.HandleFunc("GET /jobs/{job_id}", …)`); module path `ainote`.
- Container images pinned: `postgres:16-alpine`, `redis:7-alpine`, `rabbitmq:3.13-management`.
- Notion API header `Notion-Version: 2025-09-03` on every Notion call (env `NOTION_VERSION`).
- `MAX_VIDEO_SECONDS = 1200`; video ID regex `^[A-Za-z0-9_-]{11}$`.
- Status strings exactly: `QUEUED, TRANSCRIBING, SUMMARIZING, EXTRACTING_FRAMES, PUBLISHING, COMPLETED, COMPLETED_WITH_WARNINGS, FAILED, DEAD_LETTERED`.
- All JSON fields `snake_case`; every 4xx/5xx body is `{"error": {"code": "...", "message": "..."}}`.
- Environment variable names and defaults are exactly those in SRS §13; `.env.example` lists every one.
- Never log or return Notion tokens, OAuth secrets, or LLM keys; tokens travel only AES-256-GCM-encrypted through the broker (FR-GW-4).
- Subprocesses (`yt-dlp`, `ffmpeg`) are invoked with argument slices via `exec.CommandContext`, never through a shell (NFR-SEC-3).
- Images on Notion come only from ffmpeg reading the source stream (Constraint B, FR-PIPE-26). No code path may generate, fetch, or substitute images.
- Integration tests carry `//go:build integration` and need Docker; unit tests run with plain `go test ./...`.
- TypeScript `strict: true`; the extension never renders raw JSON (NFR-UX-1).
- Default LLM provider is the Gemini free tier; nothing in the build may require a paid service.

## Review Focus

1. **YouTube URLs with extra parameters** (`&t=1m30s`, `&list=…`, `youtu.be/ID?si=…`, `m.youtube.com`, mixed-case host) must resolve to the same 11-char video ID; a playlist-only URL (`/playlist?list=…`) must be `400 INVALID_URL`. → pinned in Task A5.
2. **Short or sparse-caption videos** (≈ 45 s, a handful of caption lines) must still publish a page with ≥ 1 section; `frames_requested = 0` and final status `COMPLETED` (not with-warnings). → pinned in Tasks B4 and B9.
3. **Auto-generated captions** with repeated consecutive lines and `[Music]`/`[Applause]` markers must produce a transcript with no consecutive duplicates and no noise markers. → pinned in Task B2.
4. **The same video submitted twice in quick succession** (different `Idempotency-Key`s, e.g. two tabs) must yield two independent jobs that both complete with separate pages. → pinned in Tasks A5 and A9.
5. **Notion access revoked mid-job** (401 during publishing) must end in `FAILED / NOTION_UNAUTHORIZED`, and the extension must clear its token and prompt reconnect. → pinned in Tasks B9 and B14.

---

## File structure

```
go.mod, go.sum, Makefile, .golangci.yml, .gitignore, .env.example, README.md
.github/workflows/ci.yml, .github/workflows/release.yml
cmd/gateway/main.go                 gateway wiring
cmd/worker/main.go                  worker wiring (+ `worker doctor` subcommand)
internal/config/config.go           Gateway & Worker config structs, parsing, validation
internal/pipeline/types.go          Status, Job, Transcript, Synthesis, Frame, Warning
internal/pipeline/errors.go         RateLimitedError, RetryableError, FatalError, Classify
internal/pipeline/stage.go          Stage, StageResult, Deps, CheckpointStore, Limiter, Faults, bucket names
internal/pipeline/pipelinetest/     MemStore, NoopLimiter, NoFaults, fake stages (test helpers)
internal/crypto/sealer.go           AES-GCM token sealing
internal/jobs/                      repo.go, statemachine.go, progress.go, checkpoints.go, outbox.go, migrate.go
migrations/0001_init.up.sql/.down.sql
internal/api/                       server.go, jobs_handlers.go, errors.go, videourl.go, notionid.go,
                                    admin_handlers.go, notion_handlers.go, health_handler.go, cors.go
internal/health/checker.go          infra checks + workers alive + doctor reports
internal/queue/                     conn.go, topology.go, policy.go, publisher.go, consumer.go, message.go
internal/outbox/relay.go            transactional outbox relay
internal/dlqwatch/watcher.go        consumes jobs.dlq.audit
internal/worker/                    runtime.go, liveness.go, lease.go, retry.go, deps.go
internal/ratelimit/                 limiter.go, bucket.lua, daily.go
internal/faults/faults.go           env + Redis fault flags
internal/youtube/                   runner.go, client.go, classify.go
internal/pipeline/transcript/       json3.go, clean.go, language.go, stage.go (+ testdata/)
internal/llm/                       provider.go, gemini.go, ollama.go, openaicompat.go, guarded.go, tokens.go, factory.go
internal/pipeline/synthesis/        format.go, chunk.go, merge.go, validate.go, select.go, schema.go, stage.go, prompts/*.tmpl
internal/pipeline/frames/           ffmpeg.go, scene.go, sharpness.go, verify.go, stage.go
internal/notion/                    client.go, errors.go, oauth.go, pages.go, blocks.go, search.go, uploads.go
internal/pipeline/publish/          title.go, blocks.go, parent.go, stage.go
internal/doctor/doctor.go           preflight checks (LLM key, yt-dlp, ffmpeg, YouTube reachability)
deploy/docker-compose.yml, deploy/gateway.Dockerfile, deploy/worker.Dockerfile, deploy/demo.env
scripts/setup.sh, scripts/setup.ps1, scripts/doctor.sh, scripts/extension-id.sh, scripts/demo/*.sh
extension/                          package.json, tsconfig.json, vite.config.ts, manifest.json,
                                    src/shared/*, src/content/*, src/background/*, src/popup/*, test/*
docs/SRS.md, docs/decisions.md, docs/demo-runbook.md
```

---

## Phase 0 — Shared foundation [A+B] — 9 Nov

### Task 0.1: Repository bootstrap [A+B]

**Files:**
- Create: `go.mod`, `.gitignore`, `.golangci.yml`, `Makefile`, `.github/workflows/ci.yml`, `README.md` (stub), `docs/decisions.md` (stub, one line per SRS §19 row)

**Interfaces:**
- Produces: `make test` (unit tests), `make test-integration` (`go test -tags=integration ./...`), `make lint`, `make ext-build`, `make up`, `make down`.

- [ ] **Step 1:** `git init`, `go mod init ainote`, set `go 1.23` in `go.mod`. `.gitignore` covers `.env`, `extension/node_modules`, `extension/dist` (dist is committed only on release tags, Task Z3), `*.jpg` under `tmp/`.
- [ ] **Step 2:** Write `Makefile` targets above plus `fmt` (`gofmt -w`), and `.golangci.yml` enabling `govet, staticcheck, errcheck, gosec, revive`.
- [ ] **Step 3:** CI workflow: on push/PR run `make lint`, `make test`, `make test-integration` (ubuntu runner has Docker), and `npm --prefix extension ci && npm --prefix extension test && npm --prefix extension run build` (guarded by `if: hashFiles('extension/package.json') != ''`).
- [ ] **Step 4:** Run `make test` → Expected: `no packages to test` / exit 0.
- [ ] **Step 5:** Commit `chore: bootstrap repository`. Push to a private GitHub repo; protect `main` (PRs reviewed by the other person).

### Task 0.2: Pipeline contracts and test helpers [A+B — agree together, A types it in]

The seam between the two tracks (SRS §16.2). Both people review this PR before anything else merges.

**Files:**
- Create: `internal/pipeline/types.go`, `internal/pipeline/errors.go`, `internal/pipeline/stage.go`, `internal/pipeline/pipelinetest/memstore.go`, `internal/pipeline/pipelinetest/fakes.go`
- Test: `internal/pipeline/errors_test.go`, `internal/pipeline/types_test.go`, `internal/pipeline/pipelinetest/memstore_test.go`

**Interfaces — Produces (everything below is frozen after this task; changes need both owners' approval):**
```go
// types.go
type Status string
const (StatusQueued Status = "QUEUED"; StatusTranscribing = "TRANSCRIBING"; StatusSummarizing = "SUMMARIZING";
       StatusExtractingFrames = "EXTRACTING_FRAMES"; StatusPublishing = "PUBLISHING"; StatusCompleted = "COMPLETED";
       StatusCompletedWithWarnings = "COMPLETED_WITH_WARNINGS"; StatusFailed = "FAILED"; StatusDeadLettered = "DEAD_LETTERED")
func (s Status) IsTerminal() bool
func (s Status) NextStage() (Status, bool) // QUEUED→TRANSCRIBING→SUMMARIZING→EXTRACTING_FRAMES→PUBLISHING; PUBLISHING→("",false)

type Job struct {
    ID uuid.UUID; VideoID, YouTubeURL string; Status Status; VideoTitle string; DurationSec int
    NotionParentID, NotionResolvedParent, NotionParentKind, NotionTitleProp string // kind: "page" | "data_source"
    NotionPageID, NotionPageURL string; NotionCreateStartedAt *time.Time; Attempts int
}
type Segment struct{ StartSec, EndSec float64; Text string }                       // json: start_sec, end_sec, text
type Transcript struct{ Language string; AutoGenerated bool; Segments []Segment }
type Section struct{ Heading string; StartSec, EndSec float64; Bullets []string }
type Visual struct{ TimestampSec float64; SectionIndex int; Caption, Reason string; Priority int }
type Synthesis struct{ Title, Overview string; Sections []Section; Visuals []Visual } // json tags per SRS §11.2
type FrameStatus string // "pending" | "captured" | "failed" | "omitted"
type Frame struct {
    JobID uuid.UUID; Idx int; RequestedSec float64; CapturedSec *float64; SectionIndex int
    Caption, Reason string; Status FrameStatus; Verified bool; ImageJPEG []byte
    NotionFileUploadID string; NotionUploadedAt *time.Time; WarningCode string
}
type Warning struct{ Code string `json:"code"`; TimestampSec *float64 `json:"timestamp_sec,omitempty"` }
const ( // warning codes, SRS §6.6
    WarnFrameExtractionFailed = "FRAME_EXTRACTION_FAILED"; WarnFrameNotRelevant = "FRAME_NOT_RELEVANT"
    WarnVisualDroppedInvalid = "VISUAL_DROPPED_INVALID"; WarnFrameVerifySkipped = "FRAME_VERIFY_SKIPPED")

// errors.go
type RateLimitedError struct{ Provider string; RetryAfter time.Duration } // Provider: "llm" | "notion" | "youtube"
type RetryableError struct{ Code string; Err error }
type FatalError struct{ Code, Msg string }
type ErrorClass int
const (ClassRetryable ErrorClass = iota; ClassRateLimited; ClassFatal)
func Classify(err error) ErrorClass   // errors.As; unknown errors → ClassRetryable
func ErrorCode(err error) string       // Fatal/Retryable code; RateLimited → "RATE_LIMITED"; unknown → "INTERNAL"

// stage.go
type Stage interface {
    Name() Status
    Run(ctx context.Context, job *Job, deps Deps) (StageResult, error)
}
type StageResult struct {
    ArtifactKind string; Artifact any           // "transcript" | "synthesis" | ""
    VideoTitle *string; DurationSec *int
    FramesRequested, FramesPublished *int; PageURL string
}
type Deps struct {
    Store CheckpointStore; Limiter Limiter; Faults Faults
    Progress func(stagePct int)     // 0..100 within the stage; runtime maps to SRS §4.3 ranges
    Warn func(Warning)              // deduplicated by (code, timestamp) downstream
    NotionToken string              // decrypted; non-empty only for PUBLISHING
    Log *slog.Logger
}
type CheckpointStore interface {
    SaveArtifact(ctx context.Context, jobID uuid.UUID, kind string, v any) error
    LoadArtifact(ctx context.Context, jobID uuid.UUID, kind string, v any) (bool, error)
    UpsertFrame(ctx context.Context, f Frame) error
    ListFrames(ctx context.Context, jobID uuid.UUID) ([]Frame, error)
    SetVideoMeta(ctx context.Context, jobID uuid.UUID, title string, durationSec int) error
    SetNotionParent(ctx context.Context, jobID uuid.UUID, resolved, kind, titleProp string) error
    MarkNotionCreateStarted(ctx context.Context, jobID uuid.UUID) error
    SetNotionPage(ctx context.Context, jobID uuid.UUID, pageID, url string) error
    Warnings(ctx context.Context, jobID uuid.UUID) ([]Warning, error)
}
type Limiter interface{ Acquire(ctx context.Context, bucket string, n int) error } // returns *RateLimitedError if wait > max
type FaultPoint string
const (FaultLLM FaultPoint = "llm"; FaultNotion = "notion"; FaultYouTube = "youtube"; FaultFrame = "frame")
type Faults interface {
    Check(ctx context.Context, p FaultPoint) error // llm/notion/youtube: *RateLimitedError; frame: ErrInjectedFrameFailure
    FrameDelay(ctx context.Context) time.Duration
}
var ErrInjectedFrameFailure = errors.New("injected frame failure")
const (BucketLLMRequests = "llm:requests"; BucketLLMTokens = "llm:tokens"; BucketLLMDaily = "llm:daily"
       BucketLLMFallbackRequests = "llm-fallback:requests"; BucketYouTube = "youtube")
func NotionBucket(token string) string // "notion:" + first 16 hex of sha256(token)
```
`pipelinetest`: `NewMemStore() *MemStore` (implements `CheckpointStore`, plus `Artifacts() map[string]any`), `NoopLimiter{}`, `NoFaults{}`, `FakeStage{Status pipeline.Status; Results []error /* returned in order, then nil */; Hook func(ctx context.Context) error /* optional, runs first: lets tests block or crash mid-stage */; Calls int}`, `TestDeps(store) pipeline.Deps`.

- [ ] **Step 1: Write failing tests**
```go
func TestClassify(t *testing.T) {
    require.Equal(t, ClassRateLimited, Classify(fmt.Errorf("wrap: %w", &RateLimitedError{Provider: "llm"})))
    require.Equal(t, ClassFatal, Classify(&FatalError{Code: "VIDEO_TOO_LONG"}))
    require.Equal(t, ClassRetryable, Classify(&RetryableError{Code: "NETWORK"}))
    require.Equal(t, ClassRetryable, Classify(errors.New("boom")))
    require.Equal(t, "VIDEO_TOO_LONG", ErrorCode(&FatalError{Code: "VIDEO_TOO_LONG"}))
}
func TestStatusOrder(t *testing.T) {
    next, ok := StatusSummarizing.NextStage(); require.True(t, ok); require.Equal(t, StatusExtractingFrames, next)
    _, ok = StatusPublishing.NextStage(); require.False(t, ok)
    require.True(t, StatusDeadLettered.IsTerminal()); require.False(t, StatusPublishing.IsTerminal())
}
func TestNotionBucketStable(t *testing.T) {
    require.Equal(t, NotionBucket("secret_x"), NotionBucket("secret_x")); require.NotContains(t, NotionBucket("secret_x"), "secret")
}
func TestMemStoreRoundTrip(t *testing.T) { /* SaveArtifact then LoadArtifact returns found=true and equal value; UpsertFrame same Idx twice → ListFrames len 1 */ }
```
- [ ] **Step 2:** `go test ./internal/pipeline/...` → FAIL (undefined identifiers).
- [ ] **Step 3:** Implement the types above exactly as listed.
- [ ] **Step 4:** `go test ./internal/pipeline/...` → PASS.
- [ ] **Step 5:** Commit `feat(pipeline): shared contracts and test fakes`.

---

## Track A — Platform & Reliability

### Task A1: Configuration [A] — 9 Nov

**Files:** Create `internal/config/config.go`; Test `internal/config/config_test.go`

**Interfaces — Produces:**
```go
type Common struct { DatabaseURL, AMQPURL, RedisURL string; AMQPHeartbeat time.Duration; TokenEncKey string; LogLevel string
    RetryDelays []time.Duration; DeferDelay time.Duration; NotionRPS float64; NotionVersion string }
type Gateway struct { Common; Port int; PublicBaseURL string; AdminToken string; CORSAllowedOrigins []string
    NotionClientID, NotionClientSecret, NotionRedirectURI string; DeliveryLimit int; OutboxSweepInterval time.Duration
    RabbitMgmtURL, RabbitUser, RabbitPass string }
type Worker struct { Common; LLMProvider, LLMModel, LLMVisionProvider, LLMVisionModel, LLMFallbackProvider, LLMFallbackModel string
    GeminiAPIKey, GroqAPIKey, OpenRouterAPIKey, AnthropicAPIKey, OllamaURL string
    LLMRPM, LLMTPM, LLMRPD, LLMMaxInputTokens int; LLMTimeout, LimiterMaxWait time.Duration; LLMRPDResetTZ string
    YouTubeRPS float64; YtDlpCookiesFile string; MaxVideoSeconds, MaxFrames, FrameConcurrency int; FrameTimeout time.Duration
    FrameVerify bool; SceneThreshold, MaxFrameDriftSec, FrameDedupWindowSec float64; NotesLanguage string
    NotionDefaultParentID string; NotionMaxImageBytes int
    MaxAttempts, MaxRateLimitRetries, MaxDeferrals, DeliveryLimit, WorkerConcurrency int
    LeaseTTL, HeartbeatInterval, HeartbeatTTL, ShutdownGrace time.Duration; DoctorInterval time.Duration
    Faults FaultDefaults }
type FaultDefaults struct { LLM429Rate, Notion429Rate, YouTube429Rate, FrameFailRate float64; FrameFailAll bool; StageDelayMs int }
func LoadGateway() (Gateway, error); func LoadWorker() (Worker, error)
func ParseDurations(s string) ([]time.Duration, error) // "10s,30s,90s,270s"
```
Env names and defaults: SRS §13 verbatim (add `RABBITMQ_MGMT_URL=http://rabbitmq:15672`, `RABBITMQ_USER=guest`, `RABBITMQ_PASS=guest`, `DOCTOR_INTERVAL=60s`).

- [ ] **Step 1: Failing tests:** `TestLoadWorkerDefaults` (with only `TOKEN_ENC_KEY` and `GEMINI_API_KEY` set: `MaxAttempts==5`, `RetryDelays=={10s,30s,90s,270s}`, `MaxVideoSeconds==1200`, `LLMRPM==8`, `LLMRPD==200`, `DeferDelay==3s`, `MaxFrameDriftSec==3`); `TestMissingTokenKeyFails` (error message contains `TOKEN_ENC_KEY`); `TestGeminiKeyRequiredOnlyForGemini` (`LLM_PROVIDER=ollama` without key → ok); `TestBadRetryDelays` (`"10s,banana"` → error naming `RETRY_DELAYS`); `TestTokenKeyMustBe32Bytes`.
- [ ] **Step 2:** `go test ./internal/config/` → FAIL.
- [ ] **Step 3:** Implement with `caarlos0/env/v11` + a `validate()` per struct (fail fast, NFR-MAIN-4).
- [ ] **Step 4:** `go test ./internal/config/` → PASS.
- [ ] **Step 5:** Commit `feat(config): typed env configuration`.

### Task A2: Compose stack, Dockerfiles, `/healthz` [A] — 9 Nov

**Files:** Create `deploy/docker-compose.yml`, `deploy/gateway.Dockerfile`, `deploy/worker.Dockerfile`, `internal/health/checker.go`, `internal/api/health_handler.go`, `cmd/gateway/main.go`, `cmd/worker/main.go` (heartbeat-only placeholder); Test `internal/health/checker_test.go`, `internal/api/health_handler_test.go`

**Interfaces — Produces:**
```go
type Probe func(ctx context.Context) error
type Report struct { Status string `json:"status"`; Checks map[string]string `json:"checks"`; WorkersAlive int `json:"workers_alive"`
    Provider429Total int64 `json:"provider_429_total"`; Version string `json:"version"`; Doctor map[string]any `json:"doctor,omitempty"` }
type Checker struct{ Probes map[string]Probe; WorkersAlive func(ctx) (int, error); Provider429 func(ctx) (int64, error); Doctor func(ctx) (map[string]any, error); Version string }
func (c *Checker) Check(ctx context.Context, deep bool) Report // 2s timeout per probe; Status "ok" or "degraded"
func HealthHandler(c *health.Checker) http.HandlerFunc       // 200 if ok else 503; ?deep=1 includes Doctor
```
- [ ] **Step 1: Failing tests:** `TestCheckAllOK` (3 probes nil → `Status=="ok"`, checks all `"ok"`); `TestCheckOneDown` (rabbitmq probe returns `errors.New("connection refused")` → `Status=="degraded"`, `Checks["rabbitmq"]=="error: connection refused"`); `TestProbeTimeout` (probe blocks → reported error within ~2 s); `TestHandler503` (degraded → HTTP 503, body has `"status":"degraded"`); `TestDeepOnlyWhenAsked` (no `deep` → no `doctor` key).
- [ ] **Step 2:** `go test ./internal/health/ ./internal/api/` → FAIL.
- [ ] **Step 3:** Add `internal/logging/logging.go`: `func New(level string, component string) *slog.Logger` — JSON handler, attribute `component`, and a `ReplaceAttr` that replaces values of keys `notion_token`, `token`, `authorization`, `api_key`, `client_secret` with `"[REDACTED]"` (FR-ADM-5). Test `TestRedacts` (log a record with `token="secret_x"` → output lacks `secret_x`). Both mains use it.
- [ ] **Step 3b:** Implement checker + handler. Gateway `main`: load config, connect pgxpool / amqp / redis, probes = Postgres `SELECT 1`, RabbitMQ channel + passive declare `jobs.work` (until Task A7 declares it, treat "not found" as ok), Redis `PING`; serve on `GATEWAY_PORT`; graceful shutdown on SIGTERM (10 s, FR-GW-7).
- [ ] **Step 4:** Compose: services `postgres`, `redis`, `rabbitmq` (ports 5672/15672, volume), `gateway` (port 8080), `worker` (`deploy.replicas: 2`, no ports, no `container_name`, `hostname` unset so each replica gets a unique one); healthchecks on all (`pg_isready`, `redis-cli ping`, `rabbitmq-diagnostics -q ping`, gateway `wget -qO- localhost:8080/healthz`, worker `wget -qO- localhost:8081/healthz`); `depends_on: condition: service_healthy`; `restart: unless-stopped`; `env_file: ../.env`. Worker Dockerfile: multi-stage Go build, runtime `debian:bookworm-slim` + `ffmpeg` (apt) + pinned `yt-dlp` binary (release asset, version in `ARG YTDLP_VERSION`) + `wget`.
- [ ] **Step 5:** Run `go test ./internal/health/ ./internal/api/` → PASS; `docker compose -f deploy/docker-compose.yml up -d --build` then `curl -s localhost:8080/healthz` → `{"status":"ok",...}`; `docker compose -f deploy/docker-compose.yml up -d --scale worker=3` → 3 worker containers running.
- [ ] **Step 6:** Commit `feat(deploy): compose stack, dockerfiles, healthz`.

### Task A3: Schema, migrations, job repository core [A] — 10 Nov

**Files:** Create `migrations/0001_init.up.sql`, `migrations/0001_init.down.sql`, `internal/jobs/migrate.go`, `internal/jobs/repo.go`, `internal/jobs/views.go`; Test `internal/jobs/repo_integration_test.go`, `internal/jobs/testdb_test.go` (testcontainers helper)

**Interfaces — Produces:**
```go
var ErrNotFound = errors.New("job not found")
func Migrate(ctx context.Context, databaseURL string) error   // golang-migrate, embedded FS, postgres advisory lock
type Repo struct{ /* pgxpool */ }
func NewRepo(pool *pgxpool.Pool) *Repo
type NewJob struct{ ID uuid.UUID; VideoID, YouTubeURL, NotionParentID, IdempotencyKey string }
func (r *Repo) CreateJob(ctx context.Context, in NewJob, outboxPayload []byte) (job pipeline.Job, created bool, err error)
func (r *Repo) GetJob(ctx context.Context, id uuid.UUID) (pipeline.Job, error)
type Waiting struct{ Reason string `json:"reason"`; Provider string `json:"provider,omitempty"`; NextAttemptAt time.Time `json:"next_attempt_at"` }
type ErrorBody struct{ Code string `json:"code"`; Message string `json:"message"` }
type Result struct{ NotionPageURL string `json:"notion_page_url"`; FramesRequested int `json:"frames_requested"`; FramesPublished int `json:"frames_published"` }
type JobView struct{ JobID uuid.UUID `json:"job_id"`; Status pipeline.Status `json:"status"`; Progress int `json:"progress"`
    VideoID string `json:"video_id"`; VideoTitle *string `json:"video_title"`; DurationSec *int `json:"duration_sec"`
    Warnings []pipeline.Warning `json:"warnings"`; Error *ErrorBody `json:"error"`; Result *Result `json:"result"`
    Waiting *Waiting `json:"waiting"`; Attempt int `json:"attempt"`; CreatedAt, UpdatedAt time.Time }
func (r *Repo) GetJobView(ctx context.Context, id uuid.UUID) (JobView, error)  // read-only (IR-API-11)
```
Schema: SRS §7.1 verbatim (including `notion_parent_kind`, `notion_title_prop`, nullable `outbox.payload` + CHECK).

- [ ] **Step 1: Failing tests (integration):** `TestMigrateIdempotent` (run twice → no error); `TestCreateJobWritesOutbox` (one `jobs` row status `QUEUED`, progress 0, one unpublished `outbox` row with payload); `TestCreateJobIdempotencyKey` (same key twice → second returns `created=false` and same ID; one outbox row); `TestTwoJobsSameVideo` (two keys, same video → two distinct IDs) *(Review Focus 4)*; `TestGetJobViewNotFound` (`ErrNotFound`); `TestGetJobViewIsReadOnly` (call twice → `updated_at` unchanged); `TestWarningsNeverNull` (fresh job → `Warnings` is `[]` not nil in JSON).
- [ ] **Step 2:** `go test -tags=integration ./internal/jobs/` → FAIL.
- [ ] **Step 3:** Implement. `GetJobView` builds `Result` only when status is `COMPLETED*`, `Error` only when `FAILED/DEAD_LETTERED`, `Waiting` only when `next_attempt_at > now()`.
- [ ] **Step 4:** Run again → PASS. Call `jobs.Migrate` from gateway `main` before serving.
- [ ] **Step 5:** Commit `feat(jobs): schema, migrations, create/get`.

### Task A4: State machine, stage commits, retries, checkpoint store [A] — 10 Nov

**Files:** Create `internal/jobs/statemachine.go`, `internal/jobs/progress.go`, `internal/jobs/lifecycle.go`, `internal/jobs/checkpoints.go`; Test `internal/jobs/statemachine_test.go`, `internal/jobs/progress_test.go`, `internal/jobs/lifecycle_integration_test.go`, `internal/jobs/checkpoints_integration_test.go`

**Interfaces — Consumes:** Task 0.2 types; Task A3 `Repo`.
**Produces:**
```go
var ErrIllegalTransition = errors.New("illegal transition")
func AllowedFrom(to pipeline.Status) []pipeline.Status            // SRS §4.2 table
func GlobalProgress(s pipeline.Status, stagePct int) int           // QUEUED 0; TRANSCRIBING 5–20; SUMMARIZING 20–50; EXTRACTING_FRAMES 50–80; PUBLISHING 80–99; terminal 100
func (r *Repo) Transition(ctx context.Context, id uuid.UUID, to pipeline.Status, workerID string, detail any) error
func (r *Repo) CompleteStage(ctx context.Context, id uuid.UUID, cur pipeline.Status, res pipeline.StageResult, workerID string) (pipeline.Status, error)
    // ONE tx: save artifact (if kind != ""), apply VideoTitle/Duration/FramesRequested/FramesPublished/PageURL,
    // next = cur.NextStage() or, after PUBLISHING, COMPLETED / COMPLETED_WITH_WARNINGS (warnings non-empty),
    // UPDATE … WHERE status = cur, progress = GREATEST(progress, start of next range or 100), clear waiting_*, append job_events.
func (r *Repo) SetProgress(ctx context.Context, id uuid.UUID, status pipeline.Status, stagePct int) error // monotonic
func (r *Repo) AddWarning(ctx context.Context, id uuid.UUID, w pipeline.Warning) error             // dedupe (code, timestamp_sec)
type RetryKind string
const (RetryFailure RetryKind = "failure"; RetryRateLimit = "rate_limit"; RetryDefer = "defer")
type RetryDecision struct{ Exhausted bool; N int; Delay time.Duration } // N = counter value after increment
func RetryDelay(delays []time.Duration, n int, retryAfter time.Duration) time.Duration // n-th retry (1-based), last value repeats, max(…, retryAfter)
func (r *Repo) ScheduleRetry(ctx context.Context, id uuid.UUID, kind RetryKind, provider string, delays []time.Duration, retryAfter time.Duration, budget int, errCode string) (RetryDecision, error)
    // one tx: increment attempts | rate_limit_retries | deferrals → N; Exhausted when N >= budget;
    // otherwise Delay = RetryDelay(delays, N, retryAfter) and set waiting_reason ("RETRYING" | "RATE_LIMITED" | "BUSY"), waiting_provider, next_attempt_at = now()+Delay; append event
func (r *Repo) Fail(ctx context.Context, id uuid.UUID, terminal pipeline.Status, code, msg string) error // only from non-terminal; sets finished_at, progress 100
func (r *Repo) Checkpoints() pipeline.CheckpointStore
```
- [ ] **Step 1: Failing unit tests:** `TestAllowedTransitions` (table: every row of SRS §4.2 allowed; `COMPLETED→SUMMARIZING`, `SUMMARIZING→TRANSCRIBING`, `QUEUED→PUBLISHING` rejected); `TestGlobalProgress` (`(SUMMARIZING,0)==20`, `(SUMMARIZING,100)==50`, `(EXTRACTING_FRAMES,50)==65`, terminal `==100`).
- [ ] **Step 2: Failing integration tests:** `TestCompleteStageAtomic` (TRANSCRIBING + transcript artifact → status SUMMARIZING and artifact row exist; calling again with `cur=TRANSCRIBING` → `ErrIllegalTransition`, artifact unchanged); `TestPublishCompletesWithWarnings` (AddWarning then CompleteStage(PUBLISHING) → `COMPLETED_WITH_WARNINGS`, progress 100); `TestPublishCompletesClean` (no warnings → `COMPLETED`) *(Review Focus 2)*; `TestProgressMonotonic` (SetProgress 60 then 55 → stays 60); `TestAddWarningDedupes`; `TestRetryDelay` (unit: `[10s,30s,90s,270s]`: n=1→10s, n=4→270s, n=7→270s, n=1 with retryAfter 45s → 45s); `TestScheduleRetryBudget` (budget 5: four calls `Exhausted=false` with `Delay` 10s,30s,90s,270s, fifth `true`; rate-limit counter independent of failure counter; `next_attempt_at` set); `TestFailFromTerminalRejected`; `TestCheckpointStoreFrames` (UpsertFrame twice same idx → one row, latest values; ListFrames ordered by idx).
- [ ] **Step 3:** `go test ./internal/jobs/ && go test -tags=integration ./internal/jobs/` → FAIL.
- [ ] **Step 4:** Implement. All writes use conditional updates (FR-SM-1) and append `job_events` (FR-SM-5).
- [ ] **Step 5:** Run both → PASS.
- [ ] **Step 6:** Commit `feat(jobs): state machine, atomic stage commits, retries, checkpoints`.

### Task A5: Gateway job API [A] — 11 Nov

**Files:** Create `internal/crypto/sealer.go`, `internal/api/server.go`, `internal/api/errors.go`, `internal/api/videourl.go`, `internal/api/notionid.go`, `internal/api/jobs_handlers.go`, `internal/api/cors.go`, `internal/queue/message.go`; Test `internal/crypto/sealer_test.go`, `internal/api/videourl_test.go`, `internal/api/notionid_test.go`, `internal/api/jobs_handlers_test.go`

**Interfaces — Consumes:** `jobs.Repo` (`CreateJob`, `GetJobView`), `jobs.JobView`, `jobs.ErrNotFound`.
**Produces:**
```go
// crypto
type Sealer struct{}; func NewSealer(keyB64 string) (*Sealer, error); func (s *Sealer) Seal(plain string) (string, error); func (s *Sealer) Open(enc string) (string, error)
// queue/message.go
type Message struct{ V int `json:"v"`; JobID uuid.UUID `json:"job_id"`; NotionTokenEnc string `json:"notion_token_enc"`; EnqueuedAt time.Time `json:"enqueued_at"`; Reason string `json:"reason"` }
// api
func ParseVideoID(raw string) (string, error)   // IR-API-1
func ParseNotionID(raw string) (string, error)  // 32 hex with/without dashes or notion URL → dashed UUID form
func WriteError(w http.ResponseWriter, status int, code, msg string)
type JobStore interface { CreateJob(ctx, jobs.NewJob, []byte) (pipeline.Job, bool, error); GetJobView(ctx, uuid.UUID) (jobs.JobView, error) }
type OutboxKicker interface{ Kick() }
type Server struct{ /* deps */ }
func NewServer(cfg config.Gateway, deps ServerDeps) *Server; func (s *Server) Handler() http.Handler
type ServerDeps struct{ Jobs JobStore; Sealer *crypto.Sealer; Outbox OutboxKicker; Health *health.Checker; Notion NotionGateway; Admin AdminStore; Faults FaultAdmin }
```
(`NotionGateway`, `AdminStore`, `FaultAdmin` are interfaces defined in Tasks B10 and A10; until then pass `nil` and register their routes only when non-nil.)

- [ ] **Step 1: Failing tests:**
```go
func TestParseVideoID(t *testing.T) {
    ok := map[string]string{
        "https://www.youtube.com/watch?v=dQw4w9WgXcQ": "dQw4w9WgXcQ",
        "https://youtube.com/watch?v=dQw4w9WgXcQ&t=1m30s&list=PLx": "dQw4w9WgXcQ",
        "https://m.youtube.com/watch?feature=share&v=dQw4w9WgXcQ": "dQw4w9WgXcQ",
        "https://WWW.YouTube.com/watch?v=dQw4w9WgXcQ": "dQw4w9WgXcQ",
        "https://youtu.be/dQw4w9WgXcQ?si=abc": "dQw4w9WgXcQ",
        "https://www.youtube.com/embed/dQw4w9WgXcQ": "dQw4w9WgXcQ",
        "https://www.youtube.com/shorts/dQw4w9WgXcQ": "dQw4w9WgXcQ",
        "https://www.youtube.com/live/dQw4w9WgXcQ": "dQw4w9WgXcQ"}
    bad := []string{"", "not a url", "https://vimeo.com/123", "https://www.youtube.com/playlist?list=PLx",
        "https://www.youtube.com/watch?v=short", "https://evil.com/watch?v=dQw4w9WgXcQ", "javascript:alert(1)"}
    // assert each ok maps; each bad returns error
}
```
`TestSealRoundTrip` (Open(Seal(x))==x; two seals differ; tampered ciphertext → error). `TestParseNotionID` (dashed, undashed, `https://www.notion.so/My-Page-0123456789abcdef0123456789abcdef` → `01234567-89ab-cdef-0123-456789abcdef`; `"xyz"` → error).
Handler tests with a fake `JobStore` and fake kicker:
- `TestPostJobAccepted`: 202, `Location: /jobs/<id>`, body `{"job_id":…, "status":"QUEUED", "video_id":"dQw4w9WgXcQ"}`, kicker called once, store received payload whose decoded `Message.NotionTokenEnc` ≠ plaintext token and `Open()` returns it.
- `TestPostJobErrors` table: invalid JSON → 400 `INVALID_JSON`; bad URL → 400 `INVALID_URL`; missing token → 400 `MISSING_NOTION_TOKEN`; bad parent → 400 `INVALID_NOTION_PARENT`; 20 KB body → 413 `PAYLOAD_TOO_LARGE`; `text/plain` → 415 `UNSUPPORTED_MEDIA_TYPE`; every body matches `{"error":{"code","message"}}`.
- `TestPostJobIdempotencyKey` (store returns `created=false` → still 202 with same job, kicker not called).
- `TestPostJobNoExternalCalls` (handler deps contain no HTTP client; p95 measured < 200 ms over 50 calls with fake store).
- `TestGetJob` (200 with every field from SRS §6.3 present, including `warnings: []`); `TestGetJobUnknown` (404 `JOB_NOT_FOUND`; malformed UUID → 404).
- `TestResultsBeforeTerminal` (status `SUMMARIZING` → 409 `JOB_NOT_FINISHED`, message contains `SUMMARIZING`); `TestResultsCompleted` (shape SRS §6.4, absolute `https://` URL); `TestResultsFailed` (200, `result:null`, `error` set).
- `TestCORS` (Origin `chrome-extension://<id>` in allowed list → `Access-Control-Allow-Origin` echoed; preflight `OPTIONS` → 204; other origin → no header).
- [ ] **Step 2:** `go test ./internal/api/ ./internal/crypto/` → FAIL.
- [ ] **Step 3:** Implement. `POST /jobs`: `http.MaxBytesReader` 16 KB; decode; validate; `Message{V:1, Reason:"new"}`; `CreateJob(…, payload)`; `Kick()`; 202.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(api): job submission, status, results`.

### Task A6: RabbitMQ topology, policy, publisher, consumer [A] — 12 Nov

**Files:** Create `internal/queue/conn.go`, `internal/queue/topology.go`, `internal/queue/policy.go`, `internal/queue/publisher.go`, `internal/queue/consumer.go`; Test `internal/queue/topology_test.go`, `internal/queue/queue_integration_test.go`

**Interfaces — Produces:**
```go
const (ExchangeJobs = "jobs"; ExchangeDLX = "jobs.dlx"; QueueWork = "jobs.work"; QueueDefer = "jobs.retry.defer"; QueueDLQ = "jobs.dlq"; QueueDLQAudit = "jobs.dlq.audit"; RoutingWork = "work")
type Topology struct{ RetryDelays []time.Duration; DeferDelay time.Duration }
func RetryQueueName(d time.Duration) string                       // 10s → "jobs.retry.10s", 1m30s → "jobs.retry.90s"
func PickRetryDelay(configured []time.Duration, want time.Duration) time.Duration // smallest configured ≥ want, else largest
func Declare(ch *amqp.Channel, t Topology) error                  // SRS §8.1 table, idempotent
func ApplyDeliveryLimitPolicy(ctx context.Context, mgmtURL, user, pass string, limit int) error // PUT /api/policies/%2F/jobs-work-limits {"pattern":"^jobs\\.work$","definition":{"delivery-limit":N},"apply-to":"queues"}
type Conn struct{}                                                 // reconnecting wrapper
func Dial(ctx context.Context, url string, heartbeat time.Duration, log *slog.Logger) (*Conn, error)
func (c *Conn) Channel(ctx context.Context) (*amqp.Channel, error)
type Publisher struct{}
func NewPublisher(c *Conn, t Topology) *Publisher
func (p *Publisher) PublishWork(ctx context.Context, m Message) error
func (p *Publisher) PublishRetry(ctx context.Context, m Message, delay time.Duration) error // to RetryQueueName(PickRetryDelay(...)) via default exchange
func (p *Publisher) PublishDefer(ctx context.Context, m Message) error
func (p *Publisher) PublishDeadLetter(ctx context.Context, m Message, reason string) error // to jobs.dlx, header x-reason
type Delivery struct{ Msg Message; Raw amqp.Delivery }
func (d Delivery) DeliveryCount() int                              // x-delivery-count header (0 on first delivery)
func (d Delivery) Ack() error; func (d Delivery) NackRequeue() error
func Consume(ctx context.Context, c *Conn, queue string, prefetch int, handle func(context.Context, Delivery)) error // manual ack; reconnects
```
All publishes: persistent, `content_type=application/json`, `message_id=job_id`, publisher confirms (wait for ack, error on nack/timeout 5 s).

- [ ] **Step 1: Failing unit tests:** `TestRetryQueueName`; `TestPickRetryDelay` (`[10s,30s,90s,270s]`: want 0 → 10s, 25s → 30s, 5m → 270s).
- [ ] **Step 2: Failing integration tests** (testcontainers `rabbitmq:3.13-management`): `TestDeclareTwice` (no error; changing `RetryDelays` adds queues without error); `TestPolicyChangeDoesNotBreakRedeclare` (apply limit 10 then 3 → Declare still succeeds) ; `TestRetryReturnsToWork` (PublishRetry with 1 s → message arrives on `jobs.work` after ≥ 1 s); `TestDeferReturnsToWork` (defer 1 s → back on `jobs.work`, not lost); `TestDeadLetterFansOut` (PublishDeadLetter → one copy in `jobs.dlq`, one in `jobs.dlq.audit`); `TestDeliveryLimitDeadLetters` (limit 2; consume + NackRequeue 3× → message lands in `jobs.dlq`); `TestUnackedRedeliveredOnConnClose` (consume without ack, close conn → second consumer receives it with `Redelivered=true`); `TestPublishConfirmed`.
- [ ] **Step 3:** `go test ./internal/queue/ && go test -tags=integration ./internal/queue/` → FAIL.
- [ ] **Step 4:** Implement. Gateway `main`: Dial, Declare, ApplyDeliveryLimitPolicy (retry 10× / 2 s until management API is up).
- [ ] **Step 5:** Run → PASS.
- [ ] **Step 6:** Commit `feat(queue): topology, delivery-limit policy, confirmed publisher, consumer`.

### Task A7: Outbox relay [A] — 12 Nov

**Files:** Create `internal/jobs/outbox.go`, `internal/outbox/relay.go`; Test `internal/outbox/relay_integration_test.go`

**Interfaces — Consumes:** `queue.Publisher.PublishWork`, `queue.Message`.
**Produces:**
```go
type OutboxRow struct{ ID int64; JobID uuid.UUID; Payload []byte }
func (r *Repo) UnpublishedOutbox(ctx context.Context, limit int) ([]OutboxRow, error) // FOR UPDATE SKIP LOCKED, ordered by id
func (r *Repo) MarkOutboxPublished(ctx context.Context, id int64) error             // published_at=now(), payload=NULL
type Relay struct{}
func NewRelay(repo *jobs.Repo, pub *queue.Publisher, interval time.Duration, log *slog.Logger) *Relay
func (r *Relay) Run(ctx context.Context)   // loop: on Kick() or every interval → publish batch of 100
func (r *Relay) Kick()                     // non-blocking
```
- [ ] **Step 1: Failing integration tests:** `TestRelayPublishesAndClears` (create job → Kick → message on `jobs.work` with matching `job_id`; outbox `payload IS NULL`, `published_at` set); `TestRelaySurvivesBrokerOutage` (pause RabbitMQ container, create job, unpause → message published within 2 sweeps; job never lost); `TestRelayNoDoublePublishConcurrent` (two relays → each row published once).
- [ ] **Step 2:** `go test -tags=integration ./internal/outbox/` → FAIL.
- [ ] **Step 3:** Implement; wire into gateway `main` and pass `relay` as `OutboxKicker`.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(outbox): transactional outbox relay`.

### Task A8: Worker liveness and leases [A] — 12 Nov

**Files:** Create `internal/worker/liveness.go`, `internal/jobs/lease.go`; Test `internal/worker/liveness_integration_test.go`, `internal/jobs/lease_integration_test.go`

**Interfaces — Produces:**
```go
func HeartbeatKey(workerID string) string  // "worker:alive:" + id
func StartHeartbeat(ctx context.Context, rdb *redis.Client, workerID string, interval, ttl time.Duration) // SET key EX ttl every interval until ctx done; DEL on exit
func IsAlive(ctx context.Context, rdb *redis.Client, workerID string) (bool, error)
func CountAlive(ctx context.Context, rdb *redis.Client) (int, error) // SCAN worker:alive:*
type ClaimOutcome int
const (Claimed ClaimOutcome = iota; Busy; Terminal; NotFound)
func (r *Repo) Claim(ctx context.Context, id uuid.UUID, workerID string, ttl time.Duration, ownerAlive func(owner string) (bool, error)) (pipeline.Job, ClaimOutcome, error)
    // read lease_owner; if other owner AND lease_until > now AND ownerAlive(owner) → Busy;
    // else conditional UPDATE … WHERE lease_owner IS NOT DISTINCT FROM $observedOwner (lost race → Busy)
func (r *Repo) RenewLease(ctx context.Context, id uuid.UUID, workerID string, ttl time.Duration) error
func (r *Repo) ReleaseLease(ctx context.Context, id uuid.UUID, workerID string) error
```
- [ ] **Step 1: Failing integration tests:** `TestHeartbeatExpires` (stop heartbeat without DEL → key gone after ttl); `TestClaimFresh` (Claimed); `TestClaimBusyLiveOwner` (owner alive, lease valid → Busy); `TestClaimDeadOwnerImmediately` (owner not alive, lease still valid → Claimed); `TestClaimExpiredLease` (owner alive, lease expired → Claimed); `TestClaimTerminal` (→ Terminal); `TestClaimRace` (10 goroutines → exactly one Claimed).
- [ ] **Step 2:** `go test -tags=integration ./internal/worker/ ./internal/jobs/` → FAIL.
- [ ] **Step 3:** Implement. Wire `CountAlive` into the gateway's `health.Checker.WorkersAlive` (IR-API-31).
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(worker): liveness heartbeats and job leases`.

### Task A9: Worker runtime [A] — 12–13 Nov

**Files:** Create `internal/worker/runtime.go`, `internal/worker/deps.go`, `internal/worker/health.go`; Modify `cmd/worker/main.go`; Test `internal/worker/runtime_integration_test.go`

**Interfaces — Consumes:** `queue.Consume/Delivery/Publisher`, `jobs.Repo` (Claim, Transition, CompleteStage, SetProgress, AddWarning, ScheduleRetry, Fail, RenewLease, ReleaseLease, Checkpoints, GetJob), `crypto.Sealer`, `pipeline.*`.
**Produces:**
```go
type Config struct{ WorkerID string; Prefetch int; LeaseTTL, ShutdownGrace time.Duration
    MaxAttempts, MaxRateLimitRetries, MaxDeferrals, DeliveryLimit int; RetryDelays []time.Duration }
type Runtime struct{}
func New(cfg Config, repo *jobs.Repo, pub *queue.Publisher, rdb *redis.Client, sealer *crypto.Sealer,
         stages map[pipeline.Status]pipeline.Stage, limiter pipeline.Limiter, faults pipeline.Faults, log *slog.Logger) *Runtime
func (rt *Runtime) Run(ctx context.Context, conn *queue.Conn) error   // Consume(jobs.work, prefetch, rt.Handle)
func (rt *Runtime) Handle(ctx context.Context, d queue.Delivery)
func HealthServer(addr string) *http.Server // :8081/healthz → 200
```
`Handle` algorithm (the only place jobs move between stages):
```
1. If d.DeliveryCount() >= DeliveryLimit-1 → Fail(DEAD_LETTERED,"DELIVERY_LIMIT_EXCEEDED"), PublishDeadLetter, Ack. return
2. Claim → Terminal|NotFound: Ack. Busy: dec := ScheduleRetry(RetryDefer, "", []time.Duration{DeferDelay}, 0, MaxDeferrals, "");
   exhausted → Fail(DEAD_LETTERED,"RETRIES_EXHAUSTED") + PublishDeadLetter; else PublishDefer. Ack. return
3. Start lease renewal (every LeaseTTL/3) for the duration of Handle; defer ReleaseLease.
4. If job.Status == QUEUED → Transition(TRANSCRIBING).
5. Loop: stage := stages[job.Status]; token decrypted only when Status == PUBLISHING;
   res, err := stage.Run(ctx, &job, deps)
   err == nil → next := CompleteStage(job.Status, res); if next.IsTerminal() → Ack, return; job = GetJob; continue
6. ctx cancelled (shutdown/grace exceeded) → NackRequeue, return
7. switch Classify(err):
   Fatal       → Fail(FAILED, ErrorCode(err), err.Error()); Ack
   RateLimited → dec := ScheduleRetry(RetryRateLimit, provider, RetryDelays, retryAfter, MaxRateLimitRetries, "RATE_LIMITED")
                 exhausted → Fail(DEAD_LETTERED,"RATE_LIMIT_RETRIES_EXHAUSTED") + PublishDeadLetter, else PublishRetry(msg, dec.Delay); Ack
   Retryable   → dec := ScheduleRetry(RetryFailure, "", RetryDelays, 0, MaxAttempts, ErrorCode(err)) → exhausted → "RETRIES_EXHAUSTED" as above
   (DB state is always committed BEFORE the publish, and the publish is confirmed BEFORE the ack — FR-RET-3, FR-MQ-3.)
```
Deps built per job: `Store=repo.Checkpoints()`, `Progress=func(p){repo.SetProgress(job.Status,p)}`, `Warn=func(w){repo.AddWarning(w)}`.

- [ ] **Step 1–2: Failing integration tests** (Postgres + RabbitMQ + Redis containers; `pipelinetest.FakeStage`s; retry delays `[200ms,400ms,800ms,1600ms]`):
  - `TestHappyPathFakeStages`: job reaches `COMPLETED`, each stage ran once, `progress==100`, no messages left.
  - `TestRetryableThenSuccess`: summarize fails twice with `RetryableError` → `COMPLETED`, `attempts==2`, transcribe ran once (checkpoint skip).
  - `TestRateLimitedDoesNotSpendFailureBudget`: 3× `RateLimitedError` then ok → `COMPLETED`, `attempts==0`, `rate_limit_retries==3`; while waiting, `GetJobView.Waiting.Reason=="RATE_LIMITED"` and status stays `SUMMARIZING`.
  - `TestDeadLetterAfterBudget`: always `RetryableError` with `MaxAttempts=5` → `DEAD_LETTERED`, `error.code=="RETRIES_EXHAUSTED"`, message present in `jobs.dlq`.
  - `TestFatalFailsImmediately`: `FatalError{VIDEO_TOO_LONG}` → `FAILED`, stage called once.
  - `TestCrashRecovery`: stage blocks; cancel runtime #1's context hard (close its AMQP conn, stop heartbeat) mid-stage; runtime #2 completes the job within 15 s; earlier stages not re-run.
  - `TestDuplicateDeliveryDefers`: publish the same message twice while runtime #1 holds the lease → runtime #2 defers; job completes exactly once (`CompleteStage(PUBLISHING)` called once).
  - `TestTwoJobsSameVideoIndependent` *(Review Focus 4)*: two jobs, same `video_id` → both `COMPLETED`, distinct leases.
  - `TestGracefulShutdown`: SIGTERM-equivalent cancel with grace 1 s while stage runs 5 s → message requeued, lease released, job finishes on next runtime.
- [ ] **Step 3:** `go test ./internal/worker/ && go test -tags=integration ./internal/worker/` → FAIL.
- [ ] **Step 4:** Implement. `cmd/worker/main.go`: config → pg/amqp/redis → `StartHeartbeat` → `HealthServer(":8081")` → `Runtime.Run` with fake stages (sleep 1 s each) until Task Z1 swaps in real ones; `worker_id = os.Hostname()`.
- [ ] **Step 5:** Run → PASS. Manual: `docker compose up -d --build`; `curl -XPOST localhost:8080/jobs -H 'content-type: application/json' -d '{"youtube_url":"https://youtu.be/dQw4w9WgXcQ","notion_token":"x"}'` → poll until `COMPLETED`.
- [ ] **Step 6:** Commit `feat(worker): runtime with checkpoints, retries, DLQ, crash recovery`.

### Task A10: DLQ watcher and admin endpoints [A] — 13 Nov

**Files:** Create `internal/dlqwatch/watcher.go`, `internal/api/admin_handlers.go`, `internal/jobs/admin.go`; Test `internal/dlqwatch/watcher_integration_test.go`, `internal/api/admin_handlers_test.go`

**Interfaces — Produces:**
```go
func Watch(ctx context.Context, conn *queue.Conn, repo *jobs.Repo, log *slog.Logger) error // consume jobs.dlq.audit; if job not terminal → Fail(DEAD_LETTERED,"DELIVERY_LIMIT_EXCEEDED"); Ack
type Event struct{ At time.Time `json:"at"`; Type string `json:"type"`; FromStatus *string `json:"from_status"`; ToStatus *string `json:"to_status"`; WorkerID string `json:"worker_id,omitempty"`; Detail json.RawMessage `json:"detail,omitempty"` }
type DLQEntry struct{ JobView; Events []Event `json:"events"` }      // package jobs
func (r *Repo) ListDeadLettered(ctx context.Context, limit int) ([]DLQEntry, error)
func (r *Repo) ResetForReplay(ctx context.Context, id uuid.UUID, outboxPayload []byte) (pipeline.Job, error)
    // one tx: DEAD_LETTERED → stage status before dead-lettering (from last transition event), counters 0, error cleared, new outbox row
func (r *Repo) PurgeFrameImages(ctx context.Context, olderThan time.Duration) (int64, error) // image_jpeg = NULL for jobs terminal > olderThan (SRS §7.2)
type AdminStore interface {
    ListDeadLettered(ctx context.Context, limit int) ([]jobs.DLQEntry, error)
    ResetForReplay(ctx context.Context, id uuid.UUID, outboxPayload []byte) (pipeline.Job, error) }
// routes (Bearer ADMIN_TOKEN): GET /admin/dlq; POST /admin/dlq/{job_id}/replay {"notion_token": "..."}; GET/PUT /admin/faults
type FaultAdmin interface{ Get(ctx) (map[string]string, error); Set(ctx, map[string]string) error } // implemented in Task A12
```
- [ ] **Step 1: Failing tests:** `TestAdminRequiresToken` (no/invalid bearer → 401 `UNAUTHORIZED`); `TestListDLQ` (shape includes last error + events); `TestReplayRequeues` (replay → outbox row created, job non-terminal, counters 0; replay without token → 400 `MISSING_NOTION_TOKEN`); integration `TestWatcherMarksDeliveryLimit` (message dead-lettered by broker for a `SUMMARIZING` job → job `DEAD_LETTERED/DELIVERY_LIMIT_EXCEEDED`; message still visible in `jobs.dlq`).
- [ ] **Step 2:** Run tests → FAIL.
  Add integration `TestPurgeFrameImages` (terminal 25 h ago → bytes cleared, metadata kept; running job untouched).
- [ ] **Step 3:** Implement; start `Watch` and an hourly `PurgeFrameImages(24h)` loop in gateway `main`; replay handler seals the new token into a `queue.Message{Reason:"replay"}` and calls `Outbox.Kick()`.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(admin): DLQ watcher, DLQ inspection and replay, frame purge`.

### Task A11: Redis rate limiter [A] — 14 Nov

**Files:** Create `internal/ratelimit/limiter.go`, `internal/ratelimit/bucket.lua`, `internal/ratelimit/daily.go`, `internal/ratelimit/metrics.go`; Test `internal/ratelimit/limiter_integration_test.go`, `internal/ratelimit/daily_test.go`

**Interfaces — Produces:**
```go
type BucketSpec struct{ Capacity float64; RefillPerSec float64 }
type DailySpec struct{ Limit int; Loc *time.Location }
type Limiter struct{}
func New(rdb *redis.Client, buckets map[string]BucketSpec, notionTemplate BucketSpec, daily map[string]DailySpec, maxWait time.Duration, now func() time.Time) *Limiter
func (l *Limiter) Acquire(ctx context.Context, bucket string, n int) error // implements pipeline.Limiter; "notion:*" uses notionTemplate
func (l *Limiter) Take(ctx context.Context, bucket string, n int) (wait time.Duration, err error) // atomic Lua: refill, take or return wait
func NextReset(now time.Time, loc *time.Location) time.Time
func FromWorkerConfig(cfg config.Worker) (map[string]BucketSpec, BucketSpec, map[string]DailySpec)
    // llm:requests & llm-fallback:requests {Capacity: max(1, RPM/6), Refill: RPM/60}; llm:tokens {TPM/6, TPM/60};
    // youtube {1, YOUTUBE_RPS}; notion template {3, NOTION_RPS}; llm:daily DailySpec{LLM_RPD, LLM_RPD_RESET_TZ} (skip if 0)
func Incr429(ctx context.Context, rdb *redis.Client) ; func Get429(ctx context.Context, rdb *redis.Client) (int64, error) // key "metrics:provider_429_total"
```
`Acquire`: loop `Take`; if `wait == 0` return; if cumulative wait would exceed `maxWait` → `&pipeline.RateLimitedError{Provider: providerOf(bucket), RetryAfter: wait}`; else sleep `wait` (ctx-aware). Daily bucket: `INCR` key `rl:<name>:<YYYY-MM-DD in loc>` with `EXPIREAT` next reset; over limit → `RateLimitedError{RetryAfter: until reset}` immediately (no blocking).
- [ ] **Step 1: Failing tests:** `TestNextReset` (LA time 23:00 → next midnight LA); integration: `TestBucketRate` (capacity 2, refill 1/s: 2 immediate takes, third waits ≈ 1 s); `TestSharedAcrossClients` (two `Limiter`s on same Redis, 20 acquires total at 10/s with capacity 1 → elapsed ≥ 1.9 s, i.e. never exceeds rate); `TestMaxWaitReturnsRateLimited` (refill 0.01/s, maxWait 1 s → `*RateLimitedError` with `Provider=="llm"`); `TestDailyCap` (limit 3 → 4th returns `RateLimitedError` with RetryAfter ≈ until reset); `TestNotionTemplate` (`NotionBucket("a")` and `NotionBucket("b")` limited independently).
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement (Lua loaded with `redis.NewScript`). Worker fails closed: if Redis errors, `Acquire` returns `RetryableError{Code:"LIMITER_UNAVAILABLE"}` (FR-RL-5). Gateway constructs a `Limiter` with only the notion template (for Task B10) and wires `Get429` into `health.Checker.Provider429`.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(ratelimit): global Redis token buckets and daily cap`.

### Task A12: Fault injection [A] — 15 Nov

**Files:** Create `internal/faults/faults.go`; Test `internal/faults/faults_test.go`

**Interfaces — Produces:**
```go
type Faults struct{}
func New(rdb *redis.Client, defaults config.FaultDefaults, rnd func() float64) *Faults // implements pipeline.Faults and api.FaultAdmin
// Redis hash "faults" fields: llm_429_rate, notion_429_rate, youtube_429_rate, frame_fail_rate, frame_fail_all, stage_delay_ms (override env defaults; cached 1 s)
func (f *Faults) Check(ctx context.Context, p pipeline.FaultPoint) error // 429 points → &RateLimitedError{Provider: string(p), RetryAfter: 5s}
func (f *Faults) FrameDelay(ctx context.Context) time.Duration
func (f *Faults) Get(ctx context.Context) (map[string]string, error); func (f *Faults) Set(ctx context.Context, m map[string]string) error // validates keys/ranges
```
- [ ] **Step 1: Failing tests** (miniredis or testcontainers): `TestNoFaultsByDefault` (all Check nil); `TestRateOne` (`llm_429_rate=1` → `*RateLimitedError{Provider:"llm"}`); `TestFrameFailAll` (`ErrInjectedFrameFailure`); `TestRuntimeOverride` (Set via API → effective within 1 s, no restart); `TestSetRejectsUnknownKey`; `TestLogsWhenEnabled` (warn log line `fault injection active`).
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement; wire into worker `Deps` and gateway admin routes. **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(faults): runtime fault injection`.

---

## Track B — Pipeline & Client

### Task B1: YouTube tool wrapper [B] — 10 Nov

**Files:** Create `internal/youtube/runner.go`, `internal/youtube/client.go`, `internal/youtube/classify.go`; Test `internal/youtube/classify_test.go`, `internal/youtube/client_test.go`, testdata `internal/youtube/testdata/{metadata.json,stderr_429.txt,stderr_private.txt,stderr_unavailable.txt,stderr_bot.txt}`

**Interfaces — Consumes:** `pipeline.Limiter` (`BucketYouTube`), `pipeline.Faults` (`FaultYouTube`).
**Produces:**
```go
type Runner interface{ Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) }
type ExecRunner struct{}                       // exec.CommandContext, args slice
type Metadata struct{ Title string; DurationSec int; Subtitles, AutoCaptions map[string]bool /* langs with json3 */ }
type Client struct{}
func NewClient(r Runner, lim pipeline.Limiter, f pipeline.Faults, cookiesFile string) *Client
func (c *Client) Metadata(ctx context.Context, videoID string) (Metadata, error)                  // yt-dlp -J --skip-download --no-playlist
func (c *Client) Captions(ctx context.Context, videoID, lang string, auto bool) ([]byte, error)  // --write-subs|--write-auto-subs --sub-langs L --sub-format json3 into os.MkdirTemp
func (c *Client) StreamURL(ctx context.Context, videoID string) (string, error)                   // -g -f "bv*[height<=720][ext=mp4]/b[height<=720]"
func ClassifyYtDlpError(stderr string, runErr error) error
    // "HTTP Error 429" → &RateLimitedError{"youtube", 30s}; "Sign in to confirm you" → &RetryableError{"YOUTUBE_BOT_CHECK"}
    // "Private video" | "Video unavailable" | "This video is not available" → &RetryableError{"VIDEO_UNAVAILABLE"} (FR-PIPE-5); other → &RetryableError{"YTDLP_FAILED"}
```
Every call: `faults.Check(FaultYouTube)` → `limiter.Acquire(BucketYouTube,1)` → run. Args always include `--no-warnings`, and `--cookies <file>` when configured. Video ID is re-validated against the global regex before building args.
- [ ] **Step 1: Failing tests:** `TestClassifyYtDlpError` (table over the four testdata stderr files + generic); `TestMetadataParses` (fake runner returns `testdata/metadata.json` → title, `DurationSec==1147`, `Subtitles["en"]`); `TestArgsNoShell` (fake runner records `name=="yt-dlp"` and args contain the video URL as one element); `TestRejectsBadVideoID` (`"abc;rm -rf"` → error, runner not called); `TestAcquiresYouTubeBucket` (recording limiter saw `BucketYouTube`).
- [ ] **Step 2:** `go test ./internal/youtube/` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(youtube): yt-dlp wrapper with error classification`.

### Task B2: Transcript stage [B] — 10 Nov

**Files:** Create `internal/pipeline/transcript/json3.go`, `clean.go`, `language.go`, `stage.go`; Test `json3_test.go`, `clean_test.go`, `language_test.go`, `stage_test.go`; testdata `testdata/manual_en.json3`, `testdata/auto_en.json3`

**Interfaces — Consumes:** `youtube.Metadata`, `pipeline.*`, `pipelinetest`.
**Produces:**
```go
func ParseJSON3(b []byte) ([]pipeline.Segment, error) // events with segs → text joined; start=tStartMs/1000, end=start+dDurationMs/1000; skip events w/o segs
func Clean(segs []pipeline.Segment) []pipeline.Segment // strip [Music]/[Applause]/[Laughter] (any [..] all-letters), collapse whitespace, drop empties,
                                                        // drop a segment whose text equals the previous one, merge fragments < 1 s into previous
func PickLanguage(m youtube.Metadata) (lang string, auto bool, ok bool) // manual "en" > manual "en-*" > auto "en" > auto "en-*" > manual any (sorted) > auto any (sorted)
type YouTubeAPI interface{ Metadata(ctx, string) (youtube.Metadata, error); Captions(ctx, string, string, bool) ([]byte, error) }
type Stage struct{ YT YouTubeAPI; MaxVideoSeconds int }
func (s *Stage) Name() pipeline.Status
func (s *Stage) Run(ctx context.Context, job *pipeline.Job, deps pipeline.Deps) (pipeline.StageResult, error)
    // Metadata → Store.SetVideoMeta → if DurationSec > MaxVideoSeconds: &FatalError{"VIDEO_TOO_LONG", …} BEFORE any Captions call
    // no language → &FatalError{"NO_TRANSCRIPT"}; Captions → Parse → Clean; empty → NO_TRANSCRIPT
    // returns StageResult{ArtifactKind:"transcript", Artifact: pipeline.Transcript{...}, VideoTitle, DurationSec}; Progress(50) after metadata
```
- [ ] **Step 1: Failing tests:** `TestParseJSON3Manual` (first segment `{0.0, 2.5, "hello and welcome"}`); `TestCleanAutoCaptions` *(Review Focus 3)*: input with `"[Music]"`, `"so today"`, `"so today"`, `"we  will   learn"` → output texts `["so today", "we will learn"]`; `TestPickLanguage` table (manual en-GB vs auto en → manual en-GB; only auto hi → `("hi", true, true)`; none → `ok=false`); `TestTooLongRejectedBeforeCaptions` (duration 1201 → `FatalError` code `VIDEO_TOO_LONG`, fake YT `Captions` call count 0; 1200 → allowed); `TestNoCaptions` (`NO_TRANSCRIPT`); `TestUnavailablePropagatesRetryable` (YT returns `RetryableError{VIDEO_UNAVAILABLE}` → same error class).
- [ ] **Step 2:** `go test ./internal/pipeline/transcript/` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(transcript): metadata, constraint A, json3 captions`.

### Task B3: LLM providers and guarded client [B] — 11 Nov

**Files:** Create `internal/llm/provider.go`, `tokens.go`, `gemini.go`, `ollama.go`, `openaicompat.go`, `guarded.go`, `factory.go`; Test `internal/llm/tokens_test.go`, `ollama_test.go`, `gemini_test.go`, `openaicompat_test.go`, `guarded_test.go`

**Interfaces — Consumes:** `pipeline.Limiter`, `pipeline.Faults`, `ratelimit.Incr429` (via a `func(ctx)` hook to avoid an import cycle).
**Produces:**
```go
type Image struct{ Label string; JPEG []byte }
type JSONRequest struct{ System, Prompt string; Images []Image; Schema map[string]any; MaxOutputTokens int }
type JSONResponse struct{ Text string; InputTokens, OutputTokens int }
type Provider interface {
    Name() string
    GenerateJSON(ctx context.Context, req JSONRequest) (JSONResponse, error)
    SupportsImages() bool
    Ping(ctx context.Context) error // cheap auth/model check, no generation (used by doctor)
}
func EstimateTokens(req JSONRequest) int // ceil(len(System+Prompt)/4) + 260*len(Images) + MaxOutputTokens
func NewGemini(apiKey, model string, timeout time.Duration) (Provider, error)  // google.golang.org/genai; ResponseMIMEType "application/json" + ResponseJsonSchema
func NewOllama(baseURL, model string, timeout time.Duration) Provider           // POST /api/chat {stream:false, format: schema, images: base64}
func NewOpenAICompat(name, baseURL, apiKey, model string, timeout time.Duration) Provider // Groq/OpenRouter: /chat/completions, response_format json_object
type Guarded struct{ Primary, Fallback Provider; Limiter pipeline.Limiter; Faults pipeline.Faults; On429 func(context.Context) }
func (g *Guarded) GenerateJSON(ctx context.Context, req JSONRequest) (JSONResponse, error) // implements Provider
func FromConfig(cfg config.Worker, lim pipeline.Limiter, f pipeline.Faults, on429 func(context.Context)) (text Provider, vision Provider, err error)
```
Error mapping in every adapter: HTTP 429 / `RESOURCE_EXHAUSTED` → `&RateLimitedError{"llm", retryAfter parsed from Retry-After or RetryInfo, default 30s}` and call `On429`; 5xx / timeout / connection → `&RetryableError{"LLM_UNAVAILABLE"}`; 400 invalid model, 401, 403 → `&FatalError{"LLM_CONFIG_ERROR"}`.
`Guarded.GenerateJSON`: `Faults.Check(FaultLLM)` → `Acquire(BucketLLMDaily,1)` → `Acquire(BucketLLMRequests,1)` → `Acquire(BucketLLMTokens, EstimateTokens(req))` → Primary. If Primary returns `RetryableError{LLM_UNAVAILABLE}` twice in a row for this call and `Fallback != nil` → `Acquire(BucketLLMFallbackRequests,1)` → Fallback once. 429s never trigger fallback (FR-LLM-3).
- [ ] **Step 1: Failing tests:** `TestEstimateTokens`; Ollama/OpenAICompat against `httptest.Server`: `TestOllamaSendsSchemaAndImages` (request body has `format` object and `images[0]` base64), `TestMaps429` (429 + `Retry-After: 12` → `RateLimitedError{RetryAfter:12s}`, On429 called once), `TestMaps500Retryable`, `TestMaps401Fatal`; Gemini via `genai` client with `HTTPOptions.BaseURL` pointed at `httptest`: `TestGeminiJSONMode` (request has `responseMimeType: application/json`); Guarded: `TestGuardedAcquiresAllBuckets` (order daily, requests, tokens), `TestGuardedFallbackOn5xxOnly` (two 503s → fallback called once; two 429s → no fallback, RateLimited returned), `TestGuardedFaultInjection` (Faults returns RateLimited → provider never called).
- [ ] **Step 2:** `go test ./internal/llm/` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(llm): provider interface, gemini/ollama/openai-compat, guarded client`.

### Task B4: Synthesis stage [B] — 11 Nov

**Files:** Create `internal/pipeline/synthesis/format.go`, `chunk.go`, `merge.go`, `validate.go`, `select.go`, `schema.go`, `stage.go`, `prompts/synthesize.tmpl`, `prompts/repair.tmpl`, `prompts/overview.tmpl`; Test `format_test.go`, `chunk_test.go`, `merge_test.go`, `validate_test.go`, `select_test.go`, `stage_test.go`

**Interfaces — Consumes:** `llm.Provider`, `pipeline.*`.
**Produces:**
```go
func FormatTimestamp(sec float64) string                  // 75 → "01:15"; 3725 → "1:02:05"
func FormatTranscript(segs []pipeline.Segment) string     // "[mm:ss] text\n" per segment
func Chunk(segs []pipeline.Segment, maxTokens int, overlapSec float64) [][]pipeline.Segment // split on segment boundaries; est tokens = chars/4; overlap 30s
func Merge(parts []pipeline.Synthesis, ranges [][2]float64) pipeline.Synthesis
    // sections concatenated in chunk order; a section of part i whose StartSec < ranges[i-1][1] is merged into the last section (bullets appended, dedupe exact);
    // visuals concatenated with SectionIndex re-based; Title/Overview from part 0 unless overview call succeeded
func Validate(s pipeline.Synthesis, durationSec int) (pipeline.Synthesis, []pipeline.Warning, error)
    // error (RetryableError "LLM_BAD_OUTPUT") if no sections or empty title; drop visuals with ts<0, ts>duration, empty caption/reason → warning VISUAL_DROPPED_INVALID;
    // clamp SectionIndex into range by timestamp; drop empty bullets
func SelectVisuals(v []pipeline.Visual, dedupWindowSec float64, maxFrames int) []pipeline.Visual
    // sort by (priority asc, ts asc); drop any within window of an already kept one; then round-robin across sections in section order
    // taking each section's best remaining until maxFrames; return sorted by timestamp
func Schema() map[string]any      // SRS §11.2
type Stage struct{ LLM llm.Provider; MaxInputTokens int; DedupWindowSec float64; MaxFrames int; NotesLanguage string }
func (s *Stage) Run(ctx context.Context, job *pipeline.Job, deps pipeline.Deps) (pipeline.StageResult, error)
    // load "transcript"; per chunk n: LoadArtifact("synthesis_chunk:n") or call LLM (+1 repair call on invalid JSON, else RetryableError LLM_BAD_OUTPUT) then SaveArtifact;
    // Progress per chunk; Merge → Validate (Warn each warning) → SelectVisuals;
    // StageResult{ArtifactKind:"synthesis", Artifact: syn, FramesRequested: len(syn.Visuals)}
```
Prompt rules from SRS §11.3 go in `synthesize.tmpl` (inputs: `.Language`, `.RangeStart`, `.RangeEnd`, `.Transcript`, `.MaxFrames`).
- [ ] **Step 1: Failing tests:** `TestFormatTimestamp`; `TestChunkSingle` (small transcript → 1 chunk); `TestChunkSplitsWithOverlap` (≈ 3× limit → 3 chunks, chunk 2 starts ≤ 30 s before chunk 1 ends, no segment split); `TestMergeKeepsOrder` (3 parts → headings in order, overlap section merged); `TestValidateDropsBadVisuals` (ts 9999 dropped with warning carrying that timestamp); `TestSelectVisualsDedupAndCap` (visuals at 10,12,100,200,… with window 5 and max 3 → 10 kept not 12, spread across sections, sorted); `TestStageShortVideo` *(Review Focus 2)*: fake LLM returns 1 section, 0 visuals → `FramesRequested==0`, no error; `TestStageRepairOnce` (first response invalid JSON, second valid → ok, 2 calls); `TestStageResumesFromChunkCheckpoint` (MemStore has `synthesis_chunk:0` → LLM called only for chunks 1..n); `TestStagePropagatesRateLimit`.
- [ ] **Step 2:** `go test ./internal/pipeline/synthesis/` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(synthesis): chunked LLM synthesis with validation and visual selection`.

### Task B5: Frame capture primitives [B] — 12 Nov

**Files:** Create `internal/pipeline/frames/ffmpeg.go`, `scene.go`, `sharpness.go`; Test `ffmpeg_integration_test.go`, `scene_test.go`, `sharpness_test.go`

**Interfaces — Produces:**
```go
type FFmpeg interface {
    Capture(ctx context.Context, src string, atSec float64) ([]byte, error)                         // ffmpeg -hide_banner -loglevel error -ss T -i SRC -frames:v 1 -q:v 2 -f image2 -c:v mjpeg pipe:1
    SceneChanges(ctx context.Context, src string, fromSec, toSec, threshold float64) ([]float64, error) // -ss from -t (to-from) -i SRC -vf "select='gt(scene,TH)',showinfo" -f null - ; parse pts_time + from
}
func NewFFmpeg(r youtube.Runner) FFmpeg
func classifyFFmpeg(stderr string, err error) error // "429" or "403 Forbidden" in stderr → &RateLimitedError{"youtube", 30s} (FR-PIPE-20a); else &RetryableError{"FRAME_CAPTURE_FAILED"}
func ChooseTime(requested float64, changes []float64, maxDrift float64) float64 // nearest change c with |c+0.5-requested| <= min(2, maxDrift) → c+0.5; else requested
func Sharpness(jpeg []byte) (float64, error)    // grayscale, 3x3 Laplacian, variance
func ClampDrift(requested, t, maxDrift float64) float64
```
- [ ] **Step 1: Failing tests:** `TestChooseTime` (req 100, changes [98.7, 104] → 99.2; changes [90] → 100; empty → 100); `TestClampDrift`; `TestSharpness` (blurred image < sharp checkerboard); `TestClassifyFFmpeg` (stderr `"Server returned 429"` → RateLimited youtube; `"403 Forbidden"` → RateLimited; other → Retryable). Integration (`//go:build integration`, needs local ffmpeg): generate `testdata/slides.mp4` in `TestMain` with `ffmpeg -f lavfi -i "color=c=red:s=320x240:d=3,format=yuv420p" -f lavfi -i "color=c=blue:s=320x240:d=3" -filter_complex concat=n=2:v=1 …`; `TestCaptureRealFrame` (returns JPEG starting `FF D8`); `TestSceneChangeDetected` (window 1–5 s → one change ≈ 3.0 s).
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(frames): ffmpeg capture, scene detection, sharpness`.

### Task B6: Frame stage with verification [B] — 15 Nov (basic capture path by 12 Nov)

**Files:** Create `internal/pipeline/frames/verify.go`, `internal/pipeline/frames/stage.go`, `internal/pipeline/frames/prompts/verify.tmpl`; Test `verify_test.go`, `stage_test.go`

**Interfaces — Consumes:** `FFmpeg`, `ChooseTime`, `ClampDrift`, `Sharpness` (Task B5); `llm.Provider` (vision); `pipeline.Synthesis`, `Frame`, `Deps`.
**Produces:**
```go
type VerifyInput struct{ Index int; JPEG []byte; Caption, Reason string }
type VerifyOutput struct{ Index int `json:"index"`; ShowsVisual bool `json:"shows_visual"`; MatchesCaption bool `json:"matches_caption"`; RevisedCaption string `json:"revised_caption"` }
type Verifier interface{ Verify(ctx context.Context, in []VerifyInput) ([]VerifyOutput, error) }
func NewLLMVerifier(p llm.Provider) Verifier // one call, images downscaled to ≤768 px wide, labelled "#<index>"
type StreamResolver interface{ StreamURL(ctx context.Context, videoID string) (string, error) }
type Stage struct{ YT StreamResolver; FF FFmpeg; Verifier Verifier; Verify bool; Concurrency int; FrameTimeout time.Duration
    SceneThreshold, MaxDriftSec float64; Snap, PickSharpest bool }
func (s *Stage) Run(ctx context.Context, job *pipeline.Job, deps pipeline.Deps) (pipeline.StageResult, error)
```
Run algorithm:
```
1. syn := LoadArtifact("synthesis"); frames := ListFrames; for each visual i without a row → UpsertFrame{Idx:i, RequestedSec, SectionIndex, Caption, Reason, Status:"pending"}.
2. pending := frames with Status "pending"; if none pending and all captured are Verified → return StageResult{}.
3. src := YT.StreamURL (errors propagate).
4. errgroup with Concurrency: per pending frame (ctx timeout FrameTimeout): Faults.FrameDelay sleep; Faults.Check(FaultFrame) err → mark failed;
   Limiter.Acquire(BucketYouTube,1) before every ffmpeg call; t := Snap ? ChooseTime(req, SceneChanges(req-2, req+2), MaxDrift) : req;
   candidates t, t±0.5 (if PickSharpest) clamped by ClampDrift → keep sharpest;
   success → UpsertFrame{Status:"captured", CapturedSec:&t, ImageJPEG}; *RateLimitedError → cancel group and RETURN it (job waits, captured frames kept);
   other error → UpsertFrame{Status:"failed", WarningCode: FRAME_EXTRACTION_FAILED} + Warn{code, &requested}.
   Progress after each frame.
5. If Verify and Verifier != nil and any captured & !Verified: outs, err := Verify(captured);
   err → Warn{FRAME_VERIFY_SKIPPED}; mark all Verified (keep captions);
   else per out: matches → Verified; !matches && shows → Caption=Revised, Verified; !shows → retry candidate t2 := ClampDrift(req, CapturedSec+2, MaxDrift),
   capture + second batched Verify of all retried frames; still !shows (or capture fails) → Status "omitted", WarningCode FRAME_NOT_RELEVANT, Warn.
6. Any captured frame with empty Caption → Status "omitted" (Constraint B guard). Return StageResult{}.
```
- [ ] **Step 1: Failing tests** (fake FFmpeg returning fixed JPEG bytes, fake Verifier, MemStore):
  - `TestAllFramesCaptured` (3 visuals → 3 captured, `CapturedSec` within 3 s of requested).
  - `TestPartialFailureWarns` (frame 2 capture error → status failed, warning `FRAME_EXTRACTION_FAILED` with `timestamp_sec`, others captured).
  - `TestAllFail` (every capture errors → no error returned, 0 captured).
  - `TestStreamRateLimitReturnsRateLimited` (FFmpeg returns `RateLimitedError` on frame 2 → stage returns RateLimited; frame 1 persisted captured; rerun captures only remaining).
  - `TestResumeSkipsCaptured` (MemStore pre-populated with 2 captured → FFmpeg called only for the third).
  - `TestVerifyReplacesCaption` (matches=false, shows=true → caption = revised).
  - `TestVerifyNotRelevantOmits` (shows=false twice → omitted with `FRAME_NOT_RELEVANT`; retry candidate time ≤ requested+3).
  - `TestVerifyFailureKeepsFrames` (Verifier error → frames kept, `FRAME_VERIFY_SKIPPED` warning).
  - `TestInjectedFrameFailure` (Faults returns `ErrInjectedFrameFailure` → treated as capture failure).
  - `TestDriftNeverExceeded` (property over random scene-change lists: |captured−requested| ≤ 3).
- [ ] **Step 2:** `go test ./internal/pipeline/frames/` → FAIL. **Step 3:** Implement (basic path steps 1–4 first for the 12 Nov milestone, verification by 15 Nov). **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(frames): frame stage with degradation and batched verification`.

### Task B7: Notion API client [B] — 12 Nov

**Files:** Create `internal/notion/client.go`, `errors.go`, `oauth.go`, `pages.go`, `blocks.go`, `search.go`, `uploads.go`; Test `internal/notion/client_test.go` (httptest fake Notion)

**Interfaces — Consumes:** `pipeline.Limiter` (`NotionBucket(token)`), `pipeline.Faults` (`FaultNotion`).
**Produces:**
```go
type Client struct{}
func New(baseURL, version string, lim pipeline.Limiter, f pipeline.Faults, hc *http.Client) *Client // baseURL default https://api.notion.com/v1
type Block = map[string]any
type Page struct{ ID, URL string; Title string; Parent map[string]any; CreatedTime time.Time }
type OAuthToken struct{ AccessToken string `json:"access_token"`; WorkspaceName string `json:"workspace_name"`; WorkspaceIcon string `json:"workspace_icon"`; BotID string `json:"bot_id"`; DuplicatedTemplateID *string `json:"duplicated_template_id"` }
type SearchResult struct{ ID, Title, Type, Icon string; CreatedTime time.Time } // Type: "page" | "database"
func (c *Client) ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (OAuthToken, error) // POST /oauth/token, Basic auth
func (c *Client) Search(ctx context.Context, token, query, objectFilter string) ([]SearchResult, error)  // paginated
func (c *Client) RetrievePage(ctx context.Context, token, id string) (Page, error)
func (c *Client) RetrieveDatabase(ctx context.Context, token, id string) (dataSourceIDs []string, err error)
func (c *Client) TitlePropertyName(ctx context.Context, token, dataSourceID string) (string, error)
func (c *Client) CreatePage(ctx context.Context, token string, parent map[string]any, properties map[string]any, icon string) (Page, error)
func (c *Client) ListChildren(ctx context.Context, token, blockID string) ([]Block, error)             // paginated
func (c *Client) AppendChildren(ctx context.Context, token, blockID string, blocks []Block) error      // batches of ≤100
func (c *Client) ArchiveBlock(ctx context.Context, token, blockID string) error                         // DELETE /blocks/{id}
func (c *Client) QueryDataSource(ctx context.Context, token, dataSourceID string, filter map[string]any) ([]Page, error)
func (c *Client) UploadImage(ctx context.Context, token, filename string, jpeg []byte) (fileUploadID string, err error) // POST /file_uploads then /file_uploads/{id}/send (multipart)
var ErrObjectNotFound = errors.New("notion object not found")
```
Every call: `Faults.Check(FaultNotion)` → `Limiter.Acquire(NotionBucket(token),1)` → request with `Authorization: Bearer`, `Notion-Version`. Mapping: 401/403 → `&FatalError{"NOTION_UNAUTHORIZED"}`; 404 → `ErrObjectNotFound`; 429 → `&RateLimitedError{"notion", Retry-After}`; 5xx/network → `&RetryableError{"NOTION_UNAVAILABLE"}`; 400 → `&RetryableError{"NOTION_BAD_REQUEST"}` with Notion's message (logged without the token).
- [ ] **Step 1: Failing tests** against fake server: `TestHeadersSent` (version + bearer); `TestAppendBatches` (250 blocks → 3 requests of 100/100/50); `TestPaginationChildren` (`has_more` cursor followed); `TestErrorMapping` table (401, 403, 404, 429 with `Retry-After: 2`, 502); `TestUploadImageTwoStep` (create then multipart send with `image/jpeg`); `TestExchangeCodeBasicAuth` (Authorization `Basic base64(id:secret)`); `TestTokenNeverInErrors` (error strings don't contain the token); `TestLimiterBucketPerToken`.
- [ ] **Step 2:** `go test ./internal/notion/` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(notion): API client with rate limiting and error mapping`.

### Task B8: Notion page content builder [B] — 13 Nov

**Files:** Create `internal/pipeline/publish/title.go`, `internal/pipeline/publish/blocks.go`; Test `title_test.go`, `blocks_test.go`

**Interfaces — Consumes:** `synthesis.FormatTimestamp`, `pipeline.Synthesis`, `pipeline.Frame`, `pipeline.Warning`.
**Produces:**
```go
func ShortID(id uuid.UUID) string                         // first 6 hex chars
func PageTitle(videoTitle, shortID string) string         // "<title truncated to 150 runes> — Notes · <shortID>"; empty title → "YouTube video <videoID>" handled by caller
func YouTubeLink(videoID string, sec float64) string      // https://www.youtube.com/watch?v=ID&t=<int sec>s
func SplitRichText(s string, max int) []string            // max 2000, split on rune boundaries (prefer whitespace)
func BuildBlocks(job pipeline.Job, syn pipeline.Synthesis, frames []pipeline.Frame, uploads map[int]string, warnings []pipeline.Warning, now time.Time) []notion.Block
    // layout SRS §5.4.2: link paragraph (Watch on YouTube · Duration mm:ss · Generated date), "Overview" heading_2 + paragraph,
    // per section: heading_2 "N. Heading (mm:ss–mm:ss)" linked to start, bulleted_list_item per bullet,
    // then each frame of that section with Status "captured" and an upload ID: image {type:"file_upload", file_upload:{id}}
    //   with caption "<caption> — Why it matters: <reason> (<mm:ss of CapturedSec>)" (timestamp linked);
    // footer: warning callout ⚠️ only if warnings non-empty (plain words per code), info callout ℹ️ "Screenshots are real frames from the video; captions are AI-written."
```
- [ ] **Step 1: Failing tests:** `TestPageTitle` (long emoji title truncated by runes, suffix intact); `TestSplitRichText` (4500 chars → 3 parts ≤ 2000, rejoined equals input); `TestBlocksOrderAndCaptions` (2 sections, 2 frames → heading, bullets, image under correct section; caption contains caption + reason + `CapturedSec` timestamp, not `RequestedSec`); `TestFramesWithoutUploadOrCaptionSkipped` (Constraint B guard); `TestNoWarningCalloutWhenClean`; `TestWarningCalloutPlainWords` (contains "couldn't be captured" and "07:41"; no raw code strings); `TestTextOnlyPage` (0 frames → valid blocks, no image blocks).
- [ ] **Step 2:** `go test ./internal/pipeline/publish/` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(publish): Notion page block builder`.

### Task B9: Publish stage (parent resolution, idempotent page) [B] — 13 Nov (basic by 12 Nov)

**Files:** Create `internal/pipeline/publish/parent.go`, `internal/pipeline/publish/stage.go`; Test `parent_test.go`, `stage_test.go` (fake `NotionAPI`)

**Interfaces — Consumes:** Task B7 client methods (via interface), Task B8 builders, `pipeline.CheckpointStore`.
**Produces:**
```go
type NotionAPI interface { /* the B7 methods used: Search, RetrievePage, RetrieveDatabase, TitlePropertyName, CreatePage,
    ListChildren, AppendChildren, ArchiveBlock, QueryDataSource, UploadImage */ }
const DefaultParentTitle = "AI Video Notes"
func ResolveParent(ctx context.Context, n NotionAPI, token, requested, envDefault string) (resolved, kind, titleProp string, err error)
    // id := requested || envDefault || exact-title search (oldest created) → none: &FatalError{"NOTION_NO_DESTINATION"}
    // RetrievePage ok → ("page"); ErrObjectNotFound → RetrieveDatabase → first data source + TitlePropertyName → ("data_source");
    // both not found → &FatalError{"NOTION_PARENT_NOT_FOUND"}
type Stage struct{ Notion NotionAPI; DefaultParentID string; UploadReuse time.Duration; MaxImageBytes int; Now func() time.Time }
func (s *Stage) Run(ctx context.Context, job *pipeline.Job, deps pipeline.Deps) (pipeline.StageResult, error)
```
Run algorithm (FR-IDEM-3/4/5):
```
1. Parent: if job.NotionResolvedParent == "" → ResolveParent → Store.SetNotionParent.
2. Uploads: for captured frames: if NotionFileUploadID != "" && now-NotionUploadedAt < UploadReuse → reuse; else (re-encode quality 80→60 if > MaxImageBytes) UploadImage → UpsertFrame with ID + time. Progress.
3. Page: if job.NotionPageID == "":
     if job.NotionCreateStartedAt != nil → adopt: page parent → ListChildren(parent) child_page whose title contains ShortID;
        data_source parent → QueryDataSource(title contains ShortID); found → SetNotionPage and use it.
     if still none → MarkNotionCreateStarted → CreatePage(title=PageTitle, icon 📺) → SetNotionPage immediately.
4. Content: existing := ListChildren(page); ArchiveBlock each; AppendChildren(BuildBlocks(...)).
5. return StageResult{FramesPublished: &n (image blocks appended), PageURL: page URL}
```
- [ ] **Step 1: Failing tests:** `TestResolveExplicitPage`; `TestResolveDatabase` (returns data_source + title prop); `TestResolveDefaultByTitle` (two pages titled "AI Video Notes" → oldest; a page titled "Lecture — Notes · abc123" is never chosen); `TestResolveNone` (`NOTION_NO_DESTINATION`); `TestCreatesPageOnce` (run twice → CreatePage called once, second run reuses `NotionPageID`); `TestAdoptsAfterCrashWindow` (`NotionCreateStartedAt` set, `NotionPageID` empty, fake has child page with short ID → no CreatePage); `TestRerunReplacesContent` (page already has 5 blocks → 5 archived, then full append; final fake page block count == built block count); `TestReusesRecentUploads` / `TestReuploadsExpired`; `TestUnauthorizedIsFatal` *(Review Focus 5)*: fake returns 401 on CreatePage → `FatalError{NOTION_UNAUTHORIZED}`; `TestShortVideoNoFrames` *(Review Focus 2)*: 0 frames → page created, `FramesPublished==0`.
- [ ] **Step 2:** `go test ./internal/pipeline/publish/` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(publish): idempotent Notion publishing with default destination`.

### Task B10: Gateway Notion endpoints [B] — 13 Nov

**Files:** Create `internal/api/notion_handlers.go`; Modify `internal/api/server.go` (register routes when `deps.Notion != nil`); Test `internal/api/notion_handlers_test.go`

**Interfaces — Consumes:** `notion.Client.ExchangeCode`, `notion.Client.Search` (via interface); `config.Gateway` OAuth fields.
**Produces:**
```go
type NotionGateway interface {
    ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (notion.OAuthToken, error)
    Search(ctx context.Context, token, query, objectFilter string) ([]notion.SearchResult, error)
}
// GET  /auth/notion/config   → {"oauth_enabled": bool, "client_id": "..."}   (client_id only when enabled)
// POST /auth/notion/exchange {"code","redirect_uri"} → OAuthToken JSON | 400 OAUTH_CODE_INVALID | 502 NOTION_UNAVAILABLE | 404 when OAuth disabled
// GET  /notion/pages  (Authorization: Bearer <token>) → {"pages":[{"id","title","type","icon"}]} | 401 NOTION_UNAUTHORIZED
```
- [ ] **Step 1: Failing tests:** `TestConfigDisabled` (no client ID → `oauth_enabled:false`, no `client_id`); `TestExchangeOK`; `TestExchangeRejectsForeignRedirect` (redirect_uri ≠ configured → 400); `TestExchangeBadCode` (Notion 400 → 400 `OAUTH_CODE_INVALID`); `TestPagesUnauthorized` (fake returns `FatalError{NOTION_UNAUTHORIZED}` → 401); `TestPagesMissingBearer` (401); `TestNoTokenInLogs` (captured slog output has no token).
- [ ] **Step 2:** `go test ./internal/api/` → FAIL. **Step 3:** Implement; wire `notion.New(...)` with the gateway's Redis limiter into gateway `main`. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(api): Notion OAuth exchange, config, destination listing`.

### Task B11: Preflight doctor [B] — 16 Nov

**Files:** Create `internal/doctor/doctor.go`; Modify `cmd/worker/main.go` (subcommand `worker doctor`, periodic publish), `cmd/gateway/main.go` (Checker.Doctor reads Redis); Test `internal/doctor/doctor_test.go`

**Interfaces — Produces:**
```go
type Result struct{ Name string `json:"name"`; OK bool `json:"ok"`; Detail string `json:"detail,omitempty"`; Fix string `json:"fix,omitempty"` }
type Checks struct{ Text, Vision llm.Provider; Runner youtube.Runner; YT *youtube.Client; KnownVideoID string } // default "jNQXAC9IVRw"
func Run(ctx context.Context, c Checks) []Result // llm_text (Ping), llm_vision (SupportsImages + Ping), yt_dlp (--version), ffmpeg (-version), youtube_reachable (Metadata)
func Publish(ctx context.Context, rdb *redis.Client, workerID string, res []Result, ttl time.Duration) // key "doctor:<id>" JSON, TTL 120s
func ReadAll(ctx context.Context, rdb *redis.Client) (map[string][]Result, error)
```
Fix strings (exact): LLM 401 → `"LLM key rejected — check GEMINI_API_KEY (or your provider's key) and LLM_MODEL"`; bot check → `"YouTube is asking to confirm you're not a bot — see README › Troubleshooting › YouTube cookies"`; vision unsupported → `"LLM_VISION_MODEL must support images, or set FRAME_VERIFY=false"`.
- [ ] **Step 1: Failing tests:** `TestAllOK`; `TestLLMKeyRejected` (Ping returns `FatalError{LLM_CONFIG_ERROR}` → OK=false, Fix as above); `TestBotCheckDetected` (YT returns `RetryableError{YOUTUBE_BOT_CHECK}` → Fix references cookies); `TestPublishReadAll` (miniredis round trip).
- [ ] **Step 2:** FAIL. **Step 3:** Implement; worker runs `Run` at start and every `DOCTOR_INTERVAL`; `/healthz?deep=1` includes `ReadAll`; `scripts/doctor.sh` = `docker compose exec worker /app/worker doctor` pretty-printing results. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(doctor): preflight checks surfaced in healthz deep`.

### Task B12: Extension scaffold, shared client, message mapping [B] — 13 Nov

**Files:** Create `extension/package.json`, `tsconfig.json`, `vite.config.ts`, `manifest.json`, `src/shared/types.ts`, `src/shared/api.ts`, `src/shared/messages.ts`, `src/shared/time.ts`, `src/shared/storage.ts`, `test/chrome-mock.ts`, `scripts/extension-id.sh`; Test `extension/src/shared/*.test.ts`

**Interfaces — Produces:**
```ts
// types.ts — mirrors SRS §6.3/6.4 exactly
export type Status = 'QUEUED'|'TRANSCRIBING'|'SUMMARIZING'|'EXTRACTING_FRAMES'|'PUBLISHING'|'COMPLETED'|'COMPLETED_WITH_WARNINGS'|'FAILED'|'DEAD_LETTERED';
export interface Warning { code: string; timestamp_sec?: number }
export interface JobView { job_id: string; status: Status; progress: number; video_id: string; video_title: string|null; duration_sec: number|null;
  warnings: Warning[]; error: {code: string; message: string}|null; result: Result|null; waiting: Waiting|null; attempt: number }
export interface Result { notion_page_url: string; frames_requested: number; frames_published: number }
export interface Waiting { reason: 'RATE_LIMITED'|'RETRYING'|'BUSY'; provider?: string; next_attempt_at: string }
export interface ResultsView { status: Status; result: Result|null; warnings: Warning[]; error?: {code: string; message: string}|null }
export interface OAuthToken { access_token: string; workspace_name: string; workspace_icon?: string; bot_id: string; duplicated_template_id: string|null }
export interface NotionPage { id: string; title: string; type: 'page'|'database'; icon?: string }
export interface TrackedJob { jobId: string; videoId: string; title: string|null; last: JobView|null; results: ResultsView|null; submittedAt: number }
// api.ts
export class GatewayClient { constructor(baseUrl: string);
  submitJob(youtubeUrl: string, notionToken: string, parentId: string|null, idempotencyKey: string): Promise<{job_id: string; status: Status; video_id: string}>;
  getJob(id: string): Promise<JobView>; getResults(id: string): Promise<ResultsView|'NOT_FINISHED'>;
  notionConfig(): Promise<{oauth_enabled: boolean; client_id?: string}>; exchangeCode(code: string, redirectUri: string): Promise<OAuthToken>;
  notionPages(token: string): Promise<NotionPage[]> }
export class ApiError extends Error { code: string; status: number }
// messages.ts — the ONLY place codes become words
export const STEPS: {status: Status; label: string}[] // Queued, Transcribing, Summarizing, Extracting Frames, Publishing, Done
export function errorMessage(code: string): string      // SRS §6.6 popup column; unknown → "Something went wrong while making your notes. Please try again."
export function warningMessage(w: Warning): string      // e.g. "1 screenshot at 07:41 couldn't be captured."
export function summarizeWarnings(ws: Warning[], r: Result|null): string // "Done, but 1 of 6 screenshots couldn't be captured (at 07:41)."
export function waitingMessage(job: JobView, now: number): string // "Summarizing — waiting (AI service busy), retrying in 28 s"
export function apiErrorMessage(e: ApiError): string
// time.ts
export function formatTimestamp(sec: number): string    // same output as Go FormatTimestamp
// storage.ts
export interface Settings { gatewayUrl: string /* default 'http://localhost:8080' (FR-SETUP-6) */; notionToken: string|null; notionParentId: string|null; workspaceName: string|null }
export async function getSettings(): Promise<Settings>; export async function saveSettings(p: Partial<Settings>): Promise<void>
export async function getJobs(): Promise<TrackedJob[]>; export async function upsertJob(j: TrackedJob): Promise<void> // newest first, cap 20
```
Manifest: MV3, `key` (generated once with `openssl genrsa 2048 | openssl rsa -pubout -outform DER | base64 -w0`, committed), permissions `storage, identity, alarms, notifications`, host permissions `https://www.youtube.com/*`, `http://localhost:8080/*`, `optional_host_permissions: ["http://*/*","https://*/*"]` (custom gateway URL requested at runtime), background `service_worker` module, content script on `https://www.youtube.com/*`, action popup. `scripts/extension-id.sh` prints the ID from the key (for README and `CORS_ALLOWED_ORIGINS`, `NOTION_REDIRECT_URI`).
- [ ] **Step 1: Failing tests (Vitest):** `formatTimestamp` (75 → "01:15", 3725 → "1:02:05"); `errorMessage` for every code in SRS §6.6 returns non-empty text without underscores; unknown code → generic text; `summarizeWarnings` ("Done, but 1 of 6 screenshots couldn't be captured (at 07:41)."); `waitingMessage` (28 s countdown, RATE_LIMITED llm → "AI service busy"; notion → "Notion busy"; youtube → "YouTube busy"); `GatewayClient.getResults` maps 409 → `'NOT_FINISHED'`; non-2xx → `ApiError` with code from body; `upsertJob` caps at 20 newest-first.
- [ ] **Step 2:** `npm --prefix extension test` → FAIL. **Step 3:** Implement; `npm run build` → `dist/` loads in `chrome://extensions` (unpacked) without errors. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(extension): scaffold, gateway client, user-facing messages`.

### Task B13: Content script — Take Notes button [B] — 13 Nov

**Files:** Create `extension/src/content/index.ts`, `extension/src/content/button.ts`, `extension/src/content/button.css`; Test `extension/src/content/button.test.ts` (jsdom)

**Interfaces — Produces:**
```ts
export function videoIdFromLocation(loc: Location): string|null // /watch?v=ID only (11-char regex); else null
export function ensureButton(doc: Document, onClick: () => void): HTMLButtonElement|null // into .ytp-right-controls; idempotent (data-ainote attr); null if no toolbar
export function removeButton(doc: Document): void
export function precheckDuration(doc: Document, maxSec: number): 'ok'|'too_long'|'unknown' // <video>.duration
export function init(win: Window): void // listens yt-navigate-finish + MutationObserver on #movie_player; reads videoId at click time
// message to background: {type: 'TAKE_NOTES', videoId: string, url: string}
```
- [ ] **Step 1: Failing tests:** `ensureButton` twice → one button; no toolbar → null; `videoIdFromLocation` (`/watch?v=dQw4w9WgXcQ&t=10` → id; `/shorts/x` → null; `/` → null); navigation: dispatch `yt-navigate-finish` after changing `location` to a new video → click sends the **new** id (chrome mock records message); non-watch navigation removes button; `precheckDuration` 1300 s → `'too_long'` and clicking shows inline message "Videos longer than 20 minutes aren't supported" with no message sent; double click within 2 s → one message (debounce); button shows "Queued ✓" after background acks.
- [ ] **Step 2:** `npm --prefix extension test` → FAIL. **Step 3:** Implement. **Step 4:** PASS; manual: load unpacked, open two videos in sequence, button present once on each.
- [ ] **Step 5:** Commit `feat(extension): Take Notes button with SPA navigation support`.

### Task B14: Background service worker [B] — 13–14 Nov

**Files:** Create `extension/src/background/index.ts`, `auth.ts`, `jobs.ts`, `poller.ts`; Test `auth.test.ts`, `jobs.test.ts`, `poller.test.ts`

**Interfaces — Consumes:** Task B12 `GatewayClient`, storage, types.
**Produces:**
```ts
// auth.ts
export async function connectWithOAuth(): Promise<void>   // notionConfig → launchWebAuthFlow(authorize URL with client_id, response_type=code, owner=user,
                                                           //   redirect_uri=chrome.identity.getRedirectURL('notion')) → exchangeCode → saveSettings(token, workspace, parent=duplicated_template_id)
export async function connectWithToken(token: string): Promise<void> // notionPages(token) validates; saveSettings
export async function disconnect(): Promise<void>
// jobs.ts
export async function takeNotes(videoId: string, url: string): Promise<{ok: true; jobId: string}|{ok: false; message: string}>
   // no token → open OAuth (if enabled) else return message "Connect Notion in the extension popup first";
   // submitJob with crypto.randomUUID() idempotency key, parent = settings.notionParentId; upsertJob; badge update
// poller.ts
export async function pollOnce(now: number): Promise<void> // GET each non-terminal job; on terminal → getResults once, notification; NOTION_UNAUTHORIZED → disconnect()
export function startFastPolling(): () => void              // every 2 s while a popup port is connected
// index.ts: onMessage TAKE_NOTES → takeNotes; onConnect('popup') → fast polling; chrome.alarms 'poll' periodInMinutes 0.5 → pollOnce
```
- [ ] **Step 1: Failing tests** (chrome mock + fetch mock): `connectWithOAuth` uses redirect from `getRedirectURL('notion')` and stores token + template parent; `connectWithToken` rejects invalid token with friendly message; `takeNotes` without token and OAuth disabled → message, no fetch; `takeNotes` sends `Idempotency-Key` (two calls → two different keys) and stores job; `pollOnce` updates stored job, stops polling terminal jobs, fetches results exactly once; `pollOnce` on `FAILED/NOTION_UNAUTHORIZED` clears token *(Review Focus 5)*; badge text equals count of active jobs; completion triggers `chrome.notifications.create` with title containing video title.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(extension): background auth, submission, polling`.

### Task B15: Popup UI [B] — 14 Nov (polish 17 Nov)

**Files:** Create `extension/src/popup/index.html`, `main.tsx`, `App.tsx`, `JobCard.tsx`, `StepTracker.tsx`, `ConnectPanel.tsx`, `DestinationPicker.tsx`, `Settings.tsx`, `popup.css`; Test `JobCard.test.tsx`, `ConnectPanel.test.tsx`, `DestinationPicker.test.tsx`

**Interfaces — Consumes:** Tasks B12/B14 (messages, storage, `connectWithOAuth/connectWithToken/disconnect` via `chrome.runtime.sendMessage` types `CONNECT_OAUTH | CONNECT_TOKEN | DISCONNECT`).
**Produces:** UI per FR-EXT-20…27 and NFR-UX-2 (360 px wide, light/dark via `prefers-color-scheme`, status conveyed in words).
- [ ] **Step 1: Failing tests** (`@testing-library/preact`): `StepTracker` highlights current step and marks earlier ones done (label text, not color only); `JobCard` waiting → shows `waitingMessage` with countdown; `COMPLETED` → "View in Notion" link `target=_blank` with exact URL; `COMPLETED_WITH_WARNINGS` → summarizeWarnings text visible + link; `FAILED VIDEO_TOO_LONG` → "longer than 20 minutes"; `DEAD_LETTERED` → "after several attempts"; **no rendered text contains `{` or `"code"`** for any fixture (NFR-UX-1); `ConnectPanel` shows OAuth button only when `oauth_enabled`, always shows token field; `DestinationPicker` lists pages, defaults to saved parent, persists change; Settings saves gateway URL and requests optional host permission for non-localhost URLs.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS; manual check in Chrome at 360 px, light and dark.
- [ ] **Step 5:** Commit `feat(extension): popup with progress, results, connection, destination`.

### Task B16: Groq/OpenRouter backup wiring [B] — 16 Nov (SHOULD)

**Files:** Modify `internal/llm/factory.go`; Test `internal/llm/factory_test.go`

- [ ] **Step 1: Failing tests:** `TestFactoryProviders` (`gemini|ollama|groq|openrouter` construct; `groq` uses base `https://api.groq.com/openai/v1`, `openrouter` uses `https://openrouter.ai/api/v1`; unknown → error naming `LLM_PROVIDER`); `TestVisionFallsBackToText` (empty vision provider → same as text); `TestFallbackConfigured` (`LLM_FALLBACK_PROVIDER=groq` → `Guarded.Fallback` non-nil).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS. Manual: one real job with `LLM_PROVIDER=groq` completes.
- [ ] **Step 5:** Commit `feat(llm): groq and openrouter backups`.

---

## Phase Z — Integration, demo, delivery [A+B]

### Task Z1: Wire real stages; happy path end-to-end [A+B] — 12 Nov (milestone), extension path 14 Nov

**Files:** Modify `cmd/worker/main.go`, `cmd/gateway/main.go`; Create `scripts/demo/happy.sh`, `docs/test-videos.md`

**Interfaces — Consumes:** every Stage (`transcript.Stage`, `synthesis.Stage`, `frames.Stage`, `publish.Stage`), `llm.FromConfig`, `ratelimit.New`, `faults.New`, `notion.New`, `youtube.NewClient`.

- [ ] **Step 1:** Worker `main`: build limiter, faults, YouTube client, LLM (text + vision), Notion client; `stages := map[pipeline.Status]pipeline.Stage{TRANSCRIBING: …, SUMMARIZING: …, EXTRACTING_FRAMES: …, PUBLISHING: …}`; pass to `worker.New`.
- [ ] **Step 2:** `docs/test-videos.md`: three public captioned lecture videos with slides (~5, ~12, ~19 min), one > 20 min, one private, one without captions — IDs agreed by both.
- [ ] **Step 3:** `scripts/demo/happy.sh <url>` submits via curl (token from `NOTION_TEST_TOKEN` env), prints each status change, then the results JSON.
- [ ] **Step 4:** Run on the ~19-min video → Expected: statuses in order, `COMPLETED` (or with warnings), page opens, every image has a caption, duration logged. Record end-to-end time in `docs/test-videos.md` (target ≤ 90 s, NFR-PERF-2). **Gemini decision gate (SRS §11.6):** if quota blocks this run, switch `LLM_PROVIDER` today.
- [ ] **Step 5:** Repeat from the extension (after B13–B15): click Take Notes → popup steps → View in Notion.
- [ ] **Step 6:** Commit `feat: wire real pipeline end-to-end`.

### Task Z2: Demo scripts and runbook [A+B] — 16 Nov

**Files:** Create `deploy/demo.env`, `scripts/demo/{health,burst,kill-worker,force-429,dlq,frames-fail,too-long,scale}.sh`, `docs/demo-runbook.md`

Each script uses `curl` + `jq` against `localhost:8080`, `ADMIN_TOKEN` from `.env`, and prints a PASS/FAIL line for its expected outcome (SRS §15.4 table).
- [ ] **Step 1:** `deploy/demo.env`: `RETRY_DELAYS=3s,6s,12s,24s`, `FAULT_STAGE_DELAY_MS=1500`; README notes `docker compose --env-file .env --env-file deploy/demo.env up -d`.
- [ ] **Step 2:** `kill-worker.sh`: submit job, poll until `EXTRACTING_FRAMES`, find holder via `jobs.lease_owner` (`docker compose exec postgres psql …`), `docker kill -s KILL <container with that hostname>`, measure seconds until `progress` increases again → print `recovered in Ns` and PASS if ≤ 15 and job ends with one page (count child pages titled with its short ID via `/notion/pages` search).
- [ ] **Step 3:** `burst.sh`: 6 submissions back-to-back (6 test videos ≤ 20 min), wait all terminal, PASS if all `COMPLETED*` and `provider_429_total` from `/healthz` unchanged.
- [ ] **Step 4:** `force-429.sh [llm|notion|youtube]` (PUT `/admin/faults` rate 0.7, submit, show `waiting` JSON lines, reset faults, PASS on completion); `dlq.sh <private-url>` (PASS when `DEAD_LETTERED` < 60 s with demo env and job listed in `/admin/dlq`); `frames-fail.sh` (rate 0.5 → `COMPLETED_WITH_WARNINGS`; then `frame_fail_all=true` → text-only page, `frames_published==0`); `too-long.sh` (`FAILED VIDEO_TOO_LONG`); `scale.sh N` (`workers_alive == N`); `health.sh`.
- [ ] **Step 5:** Run every script twice on a fresh `docker compose down -v && up` → all PASS (acceptance criterion 2). Fix bugs found (each fix gets a regression test in the owning package).
- [ ] **Step 6:** `docs/demo-runbook.md`: presentation order, what to say for each, expected screen. Commit `chore(demo): failure-mode scripts and runbook`.

### Task Z3: Setup tooling, README, release [A+B] — 18 Nov

**Files:** Create `.env.example`, `scripts/setup.sh`, `scripts/setup.ps1`, `.github/workflows/release.yml`; Modify `README.md`, `deploy/docker-compose.yml` (image refs)

- [ ] **Step 1:** `.env.example`: every variable from SRS §13 with default and a one-line comment, required ones marked `# REQUIRED`.
- [ ] **Step 2:** `setup.sh`/`setup.ps1`: copy `.env.example` → `.env` if missing; fill `TOKEN_ENC_KEY` (`openssl rand -base64 32` / .NET RNG) and `ADMIN_TOKEN` only when empty; prompt for `GEMINI_API_KEY` when empty; print next steps. Test: run twice → second run changes nothing (`diff` empty).
- [ ] **Step 3:** Release workflow on tag `v*`: build & push `ghcr.io/<owner>/ainote-gateway:<tag>` and `ainote-worker:<tag>`; build extension; attach `ainote-extension-<tag>.zip`. Compose uses `image: ghcr.io/<owner>/ainote-…:${AINOTE_TAG:-latest}` with `build:` fallback.
- [ ] **Step 4:** README sections exactly as SRS §14.3 items 1–10 plus §14.4: Quick mode (internal integration + sharing a page titled "AI Video Notes") and Full OAuth mode with the printed extension ID, redirect URI, required capabilities (read/insert/update content), and how to attach the public "AI Video Notes" template page (FR-NOT-3); base URL `http://localhost:8080`; default destination rule (SRS §5.4.3); env var table; curl examples for every endpoint; failure demo scripts; troubleshooting (YouTube cookies, quotas, OAuth redirect mismatch, port conflicts).
- [ ] **Step 5:** Commit `docs: README, setup scripts, release pipeline`; tag `v1.0.0-rc1`; confirm images pull and the zip loads.

### Task Z4: Clean-machine rehearsal and acceptance [A+B] — 19 Nov

- [ ] **Step 1:** The teammate who did **not** write Z3 follows the README on a machine with no prior state (fresh clone, `docker compose down -v`, extension removed, new Notion workspace, new Gemini key) using Quick mode; time from clone to first page ≤ 15 min (FR-SETUP-9). Record friction points; fix docs.
- [ ] **Step 2:** Repeat with Full OAuth mode.
- [ ] **Step 3:** Walk Appendix A of the SRS row by row; mark each ✅ with the task/test that proves it. Any ❌ blocks the release.
- [ ] **Step 4:** Run `docs/demo-runbook.md` end-to-end twice.
- [ ] **Step 5:** Tag `v1.0.0`; commit `dist/` on the tag; submit on 20 Nov.

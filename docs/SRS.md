# Software Requirements Specification
## Distributed Multimodal Video Note-Taking Agent

| | |
|---|---|
| **Version** | 1.2 (open questions resolved: graders self-host; captioned videos only) |
| **Date** | 9 November 2026 |
| **Submission deadline** | 20 November 2026 |
| **Source spec** | `Note Taking agent.pdf` ("Project 2 — Specification") |
| **Team** | Person A — Platform & Reliability · Person B — Pipeline & Client |

---

## Table of contents

1. [Introduction](#1-introduction)
2. [Overall description](#2-overall-description)
3. [System architecture](#3-system-architecture)
4. [Job lifecycle and state machine](#4-job-lifecycle-and-state-machine)
5. [Functional requirements](#5-functional-requirements)
6. [External interface requirements (REST API)](#6-external-interface-requirements-rest-api)
7. [Data design](#7-data-design)
8. [Messaging design](#8-messaging-design)
9. [Failure handling and reliability](#9-failure-handling-and-reliability)
10. [Rate limiting](#10-rate-limiting)
11. [LLM design](#11-llm-design)
12. [Non-functional requirements](#12-non-functional-requirements)
13. [Configuration](#13-configuration)
14. [Deployment and deliverables](#14-deployment-and-deliverables)
15. [Testing and verification](#15-testing-and-verification)
16. [Team split and module contracts](#16-team-split-and-module-contracts)
17. [Schedule](#17-schedule)
18. [Risks and mitigations](#18-risks-and-mitigations)
19. [Design decisions log](#19-design-decisions-log)
20. [Future scope (post-submission)](#20-future-scope-post-submission)
21. [Appendix A — Requirements traceability matrix](#appendix-a--requirements-traceability-matrix)
22. [Appendix B — Glossary](#appendix-b--glossary)

---

## 1. Introduction

### 1.1 Purpose
This document specifies everything needed to build, test, and demonstrate a distributed, asynchronous system that turns a YouTube video into structured study notes — summarized text plus real video frames of important diagrams/slides — and publishes them to a Notion page. It is the single reference both team members build against. Every requirement in the source specification is captured here and traced in [Appendix A](#appendix-a--requirements-traceability-matrix).

### 1.2 Scope
**In scope for submission (20 Nov):** exactly the source specification — browser extension, API gateway, message broker, worker pool, the four-stage pipeline, the REST API, the four required failure modes, Constraints A and B, Docker Compose deployment, README, and health check.

**Out of scope for submission:** everything in [§20 Future scope](#20-future-scope-post-submission) (tracing, dashboards, Q&A over notes, Kubernetes, etc.). These are deliberately deferred and must not delay the submission.

### 1.3 Goals
1. **Pass every graded criterion** of the source spec, demonstrably and live.
2. **Be resume-grade:** a clean Go codebase that shows real distributed-systems engineering (at-least-once delivery, idempotency, backoff, dead-lettering, shared rate limiting, crash recovery).
3. **Cost ₹0 / $0** to build and demo.

### 1.4 Non-goals
- Producing the "best" notes. Grading checks structure and Constraints A/B, not summary brilliance; a free model is acceptable.
- Supporting videos longer than 20 minutes.
- Multi-user accounts, billing, or a hosted public service.

### 1.5 Anti-goals (would be a failure even if it "works")
- Any image on a Notion page that is **not** a real frame captured from the source video (AI-generated, redrawn, substituted, or a thumbnail).
- Any image without an LLM-written caption.
- Raw JSON shown to the end user anywhere in the extension.
- A job silently lost, stuck forever, retried forever, or duplicated on Notion.
- The pipeline doing work inside the `POST /jobs` request.
- Triggering a 429 from the LLM provider during a burst of submissions.

### 1.6 Requirement keywords
**MUST** = required by the source spec or by this design for correctness; failing it fails the project. **SHOULD** = strongly expected for a polished submission; drop only if the schedule forces it. **COULD** = nice to have if time remains.

Requirement IDs: `FR-xxx` functional, `NFR-xxx` non-functional, `IR-xxx` interface.

### 1.7 Resolved questions
| # | Question | Answer (team, 9 Nov) | Consequence |
|---|---|---|---|
| OQ-1 | Do graders run the stack themselves or watch our demo? | **Graders run it themselves**, from the README, with their own keys. | Setup must be near-frictionless: one-command setup and preflight tooling, a no-OAuth-registration path, prebuilt images, and a prebuilt extension ([§14.4](#144-grader-self-setup)). |
| OQ-2 | Are graded videos guaranteed to have captions? | **Yes** — only videos with captions/transcripts are in scope. | Caption-less videos fail cleanly with `NO_TRANSCRIPT`; speech-to-text stays future scope. |

---

## 2. Overall description

### 2.1 User story
A student watching a lecture on YouTube clicks **Take Notes** in the player toolbar. The extension submits a job. The popup shows the job moving through *Queued → Transcribing → Summarizing → Extracting Frames → Publishing → Done*. When it finishes, the popup shows **View in Notion**, which opens a page containing headed sections of bullet notes and real screenshots of the important slides, each with a caption explaining what it shows and why it matters. The student can click Take Notes on several videos in a row; each becomes an independent job, and the queue drains without errors.

### 2.2 Users
| User | Interaction |
|---|---|
| **End user (student)** | Uses only the browser extension. Never calls the API by hand. |
| **Grader** | Uses the extension, calls the REST API directly with `curl`, kills/scales containers, submits private videos, reads the README. |
| **Developers (Person A, Person B)** | Run the stack locally, use fault-injection and demo scripts. |

### 2.3 Operating environment
- Developer laptops (Windows/macOS/Linux) with Docker Desktop and Google Chrome.
- Backend runs entirely in Docker Compose on the laptop. Internet access required (YouTube, LLM provider, Notion).

### 2.4 Assumptions and dependencies
- The video has captions (manual or auto-generated) available through YouTube. Videos without any captions fail with `NO_TRANSCRIPT` (speech-to-text is [future scope](#20-future-scope-post-submission)).
- The grader can create a Notion account and authorize the integration.
- The LLM provider is reachable. Primary provider is the Gemini Flash free tier; backups are defined in [§11.6](#116-provider-strategy-and-backups).
- `yt-dlp` can access YouTube from the demo network (see risk R2).

---

## 3. System architecture

### 3.1 Components
Four independently running pieces (source spec §1.1), each a separate process/container, plus the supporting state stores:

| Component | Tech | Responsibility | Container(s) |
|---|---|---|---|
| **Browser extension** | TypeScript, Chrome Manifest V3, Vite | "Take Notes" button, Notion OAuth, job submission, progress popup | — (loaded unpacked) |
| **API Gateway** | Go (`net/http`) | Validate & accept jobs, expose status/results, Notion OAuth code exchange, health check, admin/DLQ endpoints, outbox relay. **Never runs pipeline stages.** | `gateway` |
| **Message broker** | RabbitMQ 3.13+ (management plugin) | Durable queues, at-least-once delivery, redelivery on crash, delayed retries, DLQ | `rabbitmq` |
| **Worker pool** | Go | Pull jobs, run the four pipeline stages, checkpoint, retry/dead-letter | `worker` (≥ 2 replicas) |
| **State store** | PostgreSQL 16 | Jobs, stage checkpoints, frames, events, outbox | `postgres` |
| **Coordination store** | Redis 7 | Global rate-limit token buckets, worker liveness, runtime fault flags | `redis` |
| **Local LLM (dev only)** | Ollama | Development provider; not used in the demo | `ollama` (Compose profile `dev`) |

Killing or scaling any one container MUST NOT require touching the others (NFR-REL-1).

### 3.2 Architecture diagram

```
 ┌──────────────────────── Chrome ────────────────────────┐
 │  Content script ──msg──► Background (service worker) ◄──► Popup │
 │  (Take Notes btn)        (OAuth, submit, poll)                  │
 └───────────────────────────────┬────────────────────────────────┘
                                 │ HTTPS/HTTP JSON
                                 ▼
                        ┌─────────────────┐   OAuth code exchange
                        │   API Gateway   │──────────────────────► Notion OAuth
                        │      (Go)       │
                        └──┬──────────┬───┘
               insert job  │          │ publish (via outbox, confirms)
               + outbox    ▼          ▼
                     ┌──────────┐  ┌──────────────────────────────────┐
                     │ Postgres │  │ RabbitMQ                          │
                     │  (state) │  │  jobs.work ─► jobs.retry.<delay>  │
                     └────▲─────┘  │      │  ▲          (TTL → back)   │
                          │        │      │  └──────────────────────── │
            checkpoints,  │        │      └─► jobs.dlq                 │
            status, frames│        └───────────┬──────────────────────┘
                          │                    │ consume (prefetch 1, manual ack)
                     ┌────┴────────────────────▼────┐      ┌───────┐
                     │  Worker × N (Go)              │◄────►│ Redis │ token buckets,
                     │  Transcribe → Summarize →     │      └───────┘ liveness, faults
                     │  Extract Frames → Publish     │
                     └──┬─────────┬──────────┬───────┘
                        ▼         ▼          ▼
                  YouTube     LLM provider   Notion API
                (yt-dlp,     (Gemini /      (pages, blocks,
                 ffmpeg)      backups)       file uploads)
```

### 3.3 Repository layout (monorepo)

```
/cmd/gateway/            main for the gateway
/cmd/worker/             main for the worker
/internal/config/        env parsing + validation (fail fast on bad config)
/internal/api/           HTTP handlers, request validation, error bodies
/internal/jobs/          job model, state machine, Postgres repository
/internal/outbox/        transactional outbox relay
/internal/queue/         RabbitMQ topology, publisher (confirms), consumer
/internal/worker/        worker runtime: claim, lease, stage dispatch, ack/retry/DLQ
/internal/ratelimit/     Redis token-bucket limiter (Lua)
/internal/faults/        fault-injection flags (env + Redis runtime overrides)
/internal/pipeline/      Stage interface + shared types + error taxonomy
/internal/pipeline/transcript/   yt-dlp metadata + captions
/internal/pipeline/synthesis/    chunking, prompting, merge, validation
/internal/pipeline/frames/       ffmpeg capture, scene snap, sharpness, verification
/internal/pipeline/publish/      Notion page assembly + uploads
/internal/llm/           Provider interface + gemini/, ollama/, groq/, openrouter/, claude/
/internal/notion/        Notion API client (OAuth exchange, pages, blocks, uploads)
/internal/crypto/        AES-GCM token encryption
/migrations/             SQL migrations
/extension/              TypeScript MV3 extension (Vite build)
/scripts/demo/           kill-worker, burst, force-429, dlq, scale scripts
/deploy/                 docker-compose.yml, Dockerfiles, rabbitmq/ config
/docs/                   SRS.md, decisions.md, demo-runbook.md
README.md
```

### 3.4 Worker topology
**One worker type runs all four stages** (source spec §10 leaves this open). Rationale: matches `docker compose up --scale worker=N`, keeps the crash-recovery story simple (one message per job), and checkpoints make any stage resumable on any worker. Per-stage specialised workers are [future scope](#20-future-scope-post-submission). Each worker container processes `WORKER_CONCURRENCY` jobs at a time (default **1**, so "kill the worker processing job X" is unambiguous in the demo).

---

## 4. Job lifecycle and state machine

### 4.1 Statuses (source spec §4)
| Status | Meaning | Terminal |
|---|---|---|
| `QUEUED` | Accepted, not yet claimed by a worker | no |
| `TRANSCRIBING` | Fetching metadata + timestamped transcript | no |
| `SUMMARIZING` | LLM synthesis (chunked if needed) | no |
| `EXTRACTING_FRAMES` | Capturing (and verifying) frames | no |
| `PUBLISHING` | Uploading frames and assembling the Notion page | no |
| `COMPLETED` | Page published, all requested frames embedded | yes |
| `COMPLETED_WITH_WARNINGS` | Page published; ≥ 1 frame omitted or a non-fatal issue occurred | yes |
| `FAILED` | Terminated by a non-retryable error | yes |
| `DEAD_LETTERED` | Retry budget exhausted; routed to the DLQ | yes |
| `CANCELLED` | User stopped the job before Notion page creation began | yes |

### 4.2 Transitions
| From | To | Trigger |
|---|---|---|
| — | `QUEUED` | Job accepted by `POST /jobs` and stored (enqueued via outbox) |
| `QUEUED` | `TRANSCRIBING` | A worker claims the job |
| `TRANSCRIBING` | `SUMMARIZING` | Transcript checkpoint saved |
| `SUMMARIZING` | `EXTRACTING_FRAMES` | Synthesis checkpoint saved |
| `EXTRACTING_FRAMES` | `PUBLISHING` | Frame stage finished (0 or more frames captured) |
| `PUBLISHING` | `COMPLETED` / `COMPLETED_WITH_WARNINGS` | Page fully published; with-warnings if any frame omitted or warning recorded |
| any non-terminal | `FAILED` | Non-retryable error |
| any non-terminal | `DEAD_LETTERED` | Retry budget exhausted, or broker delivery limit exceeded |
| any non-terminal, including a retry wait | `CANCELLED` | User cancellation wins the database lock before the first Notion page-creation checkpoint, and no page ID or URL is recorded |

```
            ┌────────┐ claim ┌──────────────┐ ok ┌─────────────┐ ok ┌───────────────────┐ ok ┌────────────┐
 POST ────► │ QUEUED ├──────►│ TRANSCRIBING ├───►│ SUMMARIZING ├───►│ EXTRACTING_FRAMES ├───►│ PUBLISHING │
            └────────┘       └──────┬───────┘    └──────┬──────┘    └─────────┬─────────┘    └─────┬──────┘
                                    │ retryable error: stays in same status, "waiting" sub-state      │
                                    │ fatal error ──────────────► FAILED                               ├─► COMPLETED
                                    │ budget exhausted ─────────► DEAD_LETTERED                        └─► COMPLETED_WITH_WARNINGS
```

### 4.3 Rules
- **FR-SM-1 (MUST)** Transitions are enforced in the database with conditional updates (`UPDATE … WHERE id = $1 AND status = ANY($allowed_from)`). An illegal transition is a bug and is logged at error level; it never silently overwrites state.
- **FR-SM-2 (MUST)** Statuses only move forward through the pipeline order. A retry does **not** reset the status to `QUEUED`; the job stays in the stage it was in, with a `waiting` sub-state ([§6.3](#63-get-jobsjob_id--poll-status)).
- **FR-SM-3 (MUST)** On claim, the worker derives the next stage to run from the saved checkpoints (not from the message), so a redelivered job resumes from the first incomplete stage.
- **FR-SM-4 (MUST)** Terminal statuses are final. A message for a terminal job is acknowledged and dropped.
- **FR-SM-5 (MUST)** Every transition appends a row to `job_events` (timeline for debugging and the demo).
- **FR-SM-6 (MUST)** `progress` (0–100) is monotonic non-decreasing:

| Phase | Progress range |
|---|---|
| `QUEUED` | 0 |
| `TRANSCRIBING` | 5 → 20 |
| `SUMMARIZING` | 20 → 50 (advances per chunk) |
| `EXTRACTING_FRAMES` | 50 → 80 (advances per frame) |
| `PUBLISHING` | 80 → 99 (advances per upload) |
| Terminal | 100 |

---

## 5. Functional requirements

### 5.1 Browser extension (source spec §2)

#### 5.1.1 Content script
- **FR-EXT-1 (MUST)** Runs on `https://www.youtube.com/*`. On watch pages (`/watch?v=…`), injects a **Take Notes** button into the player toolbar (`.ytp-right-controls`), styled to match the native controls.
- **FR-EXT-2 (MUST)** On click, reads the current video ID from the page URL and sends `{type: "TAKE_NOTES", videoId, url}` to the background script.
- **FR-EXT-3 (MUST)** Keeps working when the user navigates from one video to another without a full page reload. Listens for YouTube's `yt-navigate-finish` event **and** uses a `MutationObserver` on the player as a fallback; re-injects the button idempotently (never two buttons) and always reads the video ID at click time, never a cached one.
- **FR-EXT-4 (SHOULD)** Pre-check: if the page's `<video>` element reports `duration > MAX_VIDEO_SECONDS`, show an inline message "Videos longer than 20 minutes aren't supported" and do not submit. (The server still enforces Constraint A authoritatively — FR-PIPE-3.)
- **FR-EXT-5 (SHOULD)** After click, the button briefly shows a confirmation state ("Queued ✓") and is debounced for 2 s to prevent accidental double submission.
- **FR-EXT-6 (SHOULD)** Removes the button on non-watch pages (Shorts, home, search).

#### 5.1.2 Background script (service worker)
- **FR-EXT-10 (MUST)** One-time Notion OAuth: uses `chrome.identity.launchWebAuthFlow` to open Notion's authorize URL with redirect URI `https://<extension-id>.chromiumapp.org/notion`. Receives the authorization `code`, sends it to the gateway's `POST /auth/notion/exchange`, and stores the returned access token in `chrome.storage.local`. The user is not re-prompted on later jobs.
- **FR-EXT-11 (MUST)** The extension ID is pinned with a `key` in `manifest.json` so the OAuth redirect URI is stable and can be registered in the Notion integration settings.
- **FR-EXT-11a (MUST)** Alternative connection for graders (FR-SETUP-1 Mode A): the popup offers **Connect with integration token**, validates the pasted token via `GET /notion/pages`, and stores it like an OAuth token. **Connect Notion** (OAuth) is shown when `GET /auth/notion/config` reports OAuth is enabled.
- **FR-EXT-12 (MUST)** On `TAKE_NOTES`: if no token, opens the OAuth flow first, then submits. Submits `POST /jobs` with `youtube_url`, `notion_token`, and `notion_parent_id` (the user's chosen destination, [FR-EXT-24](#513-popup-ui)). Sends a fresh `Idempotency-Key` header per click (IR-API-6).
- **FR-EXT-13 (MUST)** Tracks all submitted jobs (job ID, video ID, title, last status) in `chrome.storage.local` so state survives the service worker being stopped.
- **FR-EXT-14 (MUST)** Relays status updates to the popup: polls `GET /jobs/{id}` every 2 s for active jobs while the popup is open, and every 30 s via `chrome.alarms` while it is closed. Stops polling a job once it is terminal, then fetches `GET /jobs/{id}/results` once.
- **FR-EXT-15 (MUST)** If the gateway returns 401-class Notion errors through a job (`NOTION_UNAUTHORIZED`), clears the stored token and prompts re-authorization on the next click.
- **FR-EXT-16 (SHOULD)** Shows the number of active jobs as the toolbar icon badge.
- **FR-EXT-17 (SHOULD)** Sends a Chrome notification when a job reaches a terminal status, with a "View in Notion" action on success.
- **FR-EXT-18 (MUST)** Gateway base URL is configurable in the popup's settings (default `http://localhost:8080`).

#### 5.1.3 Popup UI
- **FR-EXT-20 (MUST)** Lists the user's recent jobs (newest first, up to 20), each showing the video title (or ID until the title is known) and a human-readable step tracker: **Queued → Transcribing → Summarizing → Extracting Frames → Publishing → Done**, with the current step highlighted and a progress bar.
- **FR-EXT-21 (MUST)** While a job is waiting to retry, shows a plain-language waiting state, e.g. "Summarizing — waiting (AI service busy), retrying in 28 s". It must read as waiting, not erroring.
- **FR-EXT-22 (MUST)** On success shows a one-click **View in Notion** button opening the absolute page URL in a new tab.
- **FR-EXT-23 (MUST)** Shows failures and "completed with warnings" clearly, with plain-language messages mapped from error/warning codes ([§6.6](#66-error-and-warning-codes)), e.g. "Done, but 1 of 6 screenshots couldn't be captured (at 7:41)." or "This video is longer than 20 minutes, which isn't supported." **Raw JSON is never shown.** Unknown codes map to a generic friendly message.
- **FR-EXT-24 (MUST)** A **Notion destination** picker (populated from `GET /notion/pages` after sign-in), defaulting per [§5.4.3](#543-default-destination). The choice is persisted.
- **FR-EXT-25 (SHOULD)** Settings section: gateway URL, Notion connection status with "Disconnect" and "Reconnect" buttons.
- **FR-EXT-26 (SHOULD)** Dead-lettered jobs show "We couldn't process this video after several attempts (it may be private or unavailable)."
- **FR-EXT-27 (COULD)** "Clear finished jobs" action.

### 5.2 API Gateway
- **FR-GW-1 (MUST)** Implements the REST API in [§6](#6-external-interface-requirements-rest-api).
- **FR-GW-2 (MUST)** `POST /jobs` validates input, inserts the job (`QUEUED`) **and** an outbox row in one Postgres transaction, returns `202` immediately. No pipeline work happens inside the request (no YouTube, LLM, or Notion calls).
- **FR-GW-3 (MUST)** An outbox relay inside the gateway publishes unsent outbox rows to RabbitMQ with publisher confirms, marks them sent, and retries on failure. The relay is triggered immediately after insert and also sweeps every `OUTBOX_SWEEP_INTERVAL` (default 2 s), so a job is never lost if the broker is briefly down or the gateway crashes after commit.
- **FR-GW-4 (MUST)** The Notion token is encrypted (AES-256-GCM, key `TOKEN_ENC_KEY`) before being placed in the outbox/message. It is **never** written to Postgres in plaintext, never logged, and never returned by any endpoint. The outbox row's encrypted token is deleted once published.
- **FR-GW-5 (MUST)** `GET` endpoints are read-only and idempotent; they never mutate job state.
- **FR-GW-6 (MUST)** CORS allows the extension origin (`chrome-extension://<id>`) and any origins in `CORS_ALLOWED_ORIGINS`.
- **FR-GW-7 (MUST)** Graceful shutdown on SIGTERM: stop accepting, finish in-flight requests (≤ 10 s), flush the outbox relay.

### 5.3 Pipeline stages (source spec §3)
All stages run in the worker. Each stage is **idempotent**: re-running it after a crash produces the same durable result and never duplicates external side effects.

#### 5.3.1 Stage 1 — Transcript extraction (`TRANSCRIBING`)
- **FR-PIPE-1 (MUST)** Fetches video metadata (`yt-dlp -J --skip-download`): title, duration, availability. Saves `video_title` and `duration_sec` on the job as soon as known.
- **FR-PIPE-2 (MUST)** Fetches the timestamped transcript: manual English captions preferred, then auto-generated English, then any available language (`--write-subs --write-auto-subs --sub-format json3`). Parses into ordered segments `{start_sec, end_sec, text}`, merges tiny fragments, normalizes whitespace, removes `[Music]`-style noise markers.
- **FR-PIPE-3 (MUST) — Constraint A.** If `duration_sec > MAX_VIDEO_SECONDS` (default 1200), the job fails immediately and deterministically with `VIDEO_TOO_LONG` (`FAILED`, non-retryable). This check happens **before** downloading captions, so no processing is attempted.
- **FR-PIPE-4 (MUST)** No captions in any language → `FAILED` with `NO_TRANSCRIPT` (non-retryable).
- **FR-PIPE-5 (MUST)** Private, unavailable, or otherwise unreachable videos are classified **retryable** (they may be transient), so after `MAX_ATTEMPTS` they are dead-lettered — the source spec names this as the natural DLQ trigger.
- **FR-PIPE-6 (MUST)** Output checkpoint: `job_artifacts(kind='transcript')`.

#### 5.3.2 Stage 2 — LLM synthesis (`SUMMARIZING`)
- **FR-PIPE-10 (MUST)** Prompts the LLM to produce: a title, a short overview, ordered sections (heading + summary bullets + time range), and a list of **visual moments** — timestamps where visually important diagrams/slides appear, each with a short **caption** (what is shown) and **reason** (why it was flagged). Schema in [§11.2](#112-output-schema).
- **FR-PIPE-11 (MUST)** If the transcript exceeds `LLM_MAX_INPUT_TOKENS`, it is split into chunks on segment boundaries (with a small overlap), each chunk is synthesized, and partial results are merged **preserving section order** ([§11.4](#114-chunking-and-merge)). Each chunk result is checkpointed individually so a crash resumes mid-stage.
- **FR-PIPE-12 (MUST)** Output is validated strictly: invalid JSON → one repair attempt (error fed back to the model) → still invalid → retryable error. Visual moments with timestamps outside `[0, duration_sec]` or with an empty caption/reason are dropped and recorded as warning `VISUAL_DROPPED_INVALID`.
- **FR-PIPE-13 (MUST)** Visual moments are de-duplicated (within `FRAME_DEDUP_WINDOW_SEC`, default 5 s) and capped at `MAX_FRAMES` (default 8), keeping the highest-priority ones spread across sections.
- **FR-PIPE-14 (MUST)** Output checkpoint: `job_artifacts(kind='synthesis')`; `frames_requested` = number of visual moments after validation, dedupe, and cap.

#### 5.3.3 Stage 3 — Frame extraction (`EXTRACTING_FRAMES`)
- **FR-PIPE-20 (MUST)** Resolves a direct stream URL (`yt-dlp -g`, ≤ 720p video-only or muxed mp4) once per stage run (URLs expire; never cached across runs).
- **FR-PIPE-20a (MUST) — Stream throttling is a YouTube 429; a refused URL is not.** Every ffmpeg read of the stream acquires a token from the `youtube` bucket first and sends a browser User-Agent (YouTube's video servers refuse ffmpeg's default agent for some videos). If ffmpeg's stderr shows **HTTP 429**, the capture is not treated as a frame failure: the stage stops and returns `RateLimited{provider: "youtube"}` so the job waits and retries (already-captured frames are kept by checkpoint). If it shows **HTTP 403** (the host refused that URL), the stage fetches a fresh stream URL and retries, at most 3 times per stage run; a 403 after that fails only that frame (`FRAME_EXTRACTION_FAILED`), so the page still publishes. All other capture errors also become `FRAME_EXTRACTION_FAILED`.
- **FR-PIPE-21 (MUST)** For each visual moment, seeks to the timestamp and captures **one frame directly from the video stream** with ffmpeg (`ffmpeg -ss <t> -i <url> -frames:v 1 -q:v 2 out.jpg`, input-seeking for speed). Frames are processed with bounded concurrency `FRAME_CONCURRENCY` (default 4) and a timeout `FRAME_TIMEOUT` (default 30 s) on each ffmpeg call; time spent waiting for `youtube` tokens is not counted, so frames of concurrent jobs wait for the shared bucket instead of failing.
- **FR-PIPE-22 (SHOULD) — Slide snap.** Runs ffmpeg scene detection in a window `[t − 2 s, t + 2 s]` (`select='gt(scene,SCENE_THRESHOLD)'`); if a scene change is found, captures ~0.5 s after the change nearest to `t` (avoids mid-transition frames).
- **FR-PIPE-23 (SHOULD) — Sharpness pick.** Captures up to 3 candidates around the chosen time (±0.5 s) and keeps the sharpest (variance of a gradient/Laplacian filter computed in Go).
- **FR-PIPE-23a (MUST) — Timestamp fidelity (Constraint B).** All adjustments (snap, sharpness, the verification retry in FR-PIPE-24) keep the captured frame within **`MAX_FRAME_DRIFT_SEC` (default 3 s)** of the LLM-identified timestamp. The actual capture time is stored as `captured_sec`, and **captions, timestamp links, and `warnings[].timestamp_sec` always use `captured_sec`**, so every image is labelled with the exact moment it was taken from. The LLM-identified time is kept as `requested_sec` for debugging.
- **FR-PIPE-24 (MUST, agreed) — Frame verification.** Sends all captured frames for the job, with their captions, to a vision-capable LLM in **one batched call** ([§11.5](#115-frame-verification)). Per frame it returns: `shows_visual` (is a diagram/slide/visual actually on screen), `matches_caption`, and a `revised_caption` written from the image. Policy:
  - matches → keep caption;
  - visual present but caption mismatched → replace the caption with `revised_caption` (still LLM-written; reason kept);
  - no visual (e.g. talking head) → capture one more candidate at `t + 2 s` (within the drift limit) and re-verify it in a second batched call; if still no visual, omit the frame with warning `FRAME_NOT_RELEVANT`.
  - If verification itself fails (provider error after its own retries), frames are kept with original captions and warning `FRAME_VERIFY_SKIPPED` is recorded (verification must never sink the job).
  - Can be disabled with `FRAME_VERIFY=false`.
- **FR-PIPE-25 (MUST) — Graceful degradation.** A frame whose capture fails is **omitted** (never substituted) with warning `FRAME_EXTRACTION_FAILED {timestamp_sec}`. If every frame fails, the stage still succeeds with 0 frames and the job publishes a text-only page.
- **FR-PIPE-26 (MUST) — Constraint B.** The only code path that produces images is ffmpeg reading the source video stream. There is no image generation, no thumbnails, no placeholders. Every kept frame has a non-empty LLM-written caption; a frame without one is dropped before publishing.
- **FR-PIPE-27 (MUST)** Checkpoint: each frame (JPEG bytes, requested/captured time, caption, reason, status) is stored in `job_frames` as it completes, so a crash mid-stage only redoes unfinished frames.

#### 5.3.4 Stage 4 — Notion assembly (`PUBLISHING`)
- **FR-PIPE-30 (MUST)** Resolves the destination parent (page or database) per [§5.4.3](#543-default-destination).
- **FR-PIPE-31 (MUST)** Uploads each kept frame via the Notion File Upload API (create upload → send bytes) and records the `file_upload_id` per frame (skipping ones already uploaded on a rerun, unless the upload has expired, in which case re-upload).
- **FR-PIPE-32 (MUST)** Assembles the page ([§5.4.2](#542-page-layout)): title, video link, overview, a heading per section, bullet points, and image blocks with captions placed in the section their timestamp falls in.
- **FR-PIPE-33 (MUST) — Idempotent publishing** ([§9.3](#93-idempotency-and-no-duplicates-on-notion)): page creation happens at most once per job; content is written so a rerun converges to the same page, never a duplicate page or duplicated blocks.
- **FR-PIPE-34 (MUST)** On success, stores the absolute page URL, sets `frames_published`, and transitions to `COMPLETED` or `COMPLETED_WITH_WARNINGS`.
- **FR-PIPE-35 (MUST)** Notion `401`/`403` → `FAILED` with `NOTION_UNAUTHORIZED`; parent not found / not shared → `FAILED` with `NOTION_PARENT_NOT_FOUND`. Notion `429` → rate-limit retry. Notion `5xx`/network → retryable.

### 5.4 Notion integration

#### 5.4.1 OAuth
- **FR-NOT-1 (MUST)** A **public** Notion integration is registered with redirect URI `https://<pinned-extension-id>.chromiumapp.org/notion`.
- **FR-NOT-2 (MUST)** `POST /auth/notion/exchange` exchanges the code for an access token using `NOTION_CLIENT_ID` / `NOTION_CLIENT_SECRET` (HTTP Basic) and returns `{access_token, workspace_name, workspace_icon, bot_id, duplicated_template_id}` to the extension. The client secret never leaves the gateway. The gateway does not store the token.
- **FR-NOT-3 (SHOULD)** The integration is configured with a Notion template page "AI Video Notes", which Notion duplicates into the user's workspace during authorization; its ID (`duplicated_template_id`) becomes the default destination in the extension.

#### 5.4.2 Page layout
```
📺 <Video title> — Notes                       (page title; icon 📺)
  🔗 Watch on YouTube (link) · Duration 18:47 · Generated <date>
  ── Overview ──
  <2–4 sentence overview>
  ## 1. <Section heading>   (00:00–03:12, heading links to that time on YouTube)
   • bullet
   • bullet
   [image: real frame @ 02:41]
   caption: "<what it shows> — Why it matters: <reason> (02:41 ▶)"
  ## 2. <Section heading> …
  ── Footer (callout) ──
  ⚠️ (only if warnings) "1 screenshot at 07:41 couldn't be captured."
  ℹ️ "Screenshots are real frames from the video; captions are AI-written."
```
- **FR-NOT-10 (MUST)** Each image block's caption contains the LLM-written explanation of what is shown and why it was flagged.
- **FR-NOT-11 (SHOULD)** Timestamps in headings and captions are links to `https://www.youtube.com/watch?v=<id>&t=<sec>s`.
- **FR-NOT-12 (MUST)** Respects Notion limits: ≤ 100 blocks per append request (batched), rich-text segments ≤ 2000 characters (split), image files well under the upload size limit (JPEG quality reduced if a frame exceeds `NOTION_MAX_IMAGE_BYTES`).

#### 5.4.3 Default destination
When `notion_parent_id` is omitted (source spec §5), the worker resolves a **fixed** parent in this order (stated in the README):
1. `NOTION_DEFAULT_PARENT_ID` if set in the environment (useful for graders using an internal-integration token);
2. otherwise, the page titled exactly **"AI Video Notes"** shared with the integration — this is the template page Notion duplicates into the user's workspace during sign-in (FR-NOT-3), or a page the user creates with that title and shares. Found via Notion search (`query: "AI Video Notes"`, `filter: page`, exact title match; if several, the oldest by `created_time`). Our own notes pages are never titled this way, so the default can never drift onto a previously generated page;
3. if neither exists → `FAILED` with `NOTION_NO_DESTINATION` ("Share a page called 'AI Video Notes' with the app").

The extension always sends an explicit `notion_parent_id` (default in the picker: the duplicated template page).

- **FR-NOT-20 (MUST)** `GET /notion/pages` (with `Authorization: Bearer <notion_token>`) returns pages/databases shared with the integration (`[{id, title, type, icon}]`) for the destination picker. The gateway proxies the call and does not store the token. Gateway Notion calls (this endpoint and the OAuth exchange) go through the same Redis Notion limiter as workers.

#### 5.4.4 Notion API version and database parents
- **FR-NOT-30 (MUST)** All Notion calls send one pinned `Notion-Version` (`NOTION_VERSION`, default `2025-09-03`), set once in the Notion client.
- **FR-NOT-31 (MUST)** Parents may be a **page** or a **database** (source spec: "page-or-db-id"). Resolution: try `GET /pages/{id}`; if not a page, `GET /databases/{id}` → take its first data source (`data_sources[0].id`), fetch the data source schema, and find the name of its `title` property. Pages are then created with parent `{page_id}` or `{data_source_id}` respectively, and the result is cached on the job (`notion_resolved_parent`, `notion_parent_kind`, `notion_title_prop`).
- **FR-NOT-32 (MUST)** The short-ID adoption lookup in FR-IDEM-3 lists child pages (`GET /blocks/{page}/children`, `child_page` blocks) for page parents, and queries the data source with a `title contains <short-id>` filter for database parents.

### 5.5 Admin and operability
- **FR-ADM-1 (MUST)** `GET /healthz` checks Postgres, RabbitMQ, and Redis ([§6.7](#67-get-healthz)).
- **FR-ADM-2 (MUST)** DLQ inspection: `GET /admin/dlq` lists dead-lettered jobs with their last error and attempt history; the RabbitMQ management UI (`:15672`) also shows `jobs.dlq`. Protected by `ADMIN_TOKEN`.
- **FR-ADM-3 (SHOULD)** `POST /admin/dlq/{job_id}/replay` resets the job's attempt counters and re-enqueues it (requires a fresh Notion token in the body, since the original was discarded).
- **FR-ADM-4 (SHOULD)** `GET /admin/faults` / `PUT /admin/faults` read and set runtime fault-injection flags in Redis, so failures can be triggered live without restarting containers ([§9.6](#96-fault-injection)).
- **FR-ADM-5 (MUST)** Structured JSON logs (`log/slog`) on every component with `job_id`, `stage`, `attempt`, `worker_id`, and error codes. Secrets are redacted.

---

## 6. External interface requirements (REST API)

Base URL: `http://localhost:8080` (configurable). All bodies are JSON (`Content-Type: application/json`). Field names are `snake_case`. Timestamps are RFC 3339 UTC.

### 6.1 Endpoint summary
| Method | Path | Purpose | Spec |
|---|---|---|---|
| `POST` | `/jobs` | Submit a job | required |
| `GET` | `/jobs/{job_id}` | Poll status | required |
| `GET` | `/jobs/{job_id}/results` | Fetch outcome | required |
| `GET` | `/healthz` | Health check | required |
| `POST` | `/auth/notion/exchange` | OAuth code → token | needed by extension |
| `GET` | `/auth/notion/config` | Whether OAuth is configured (`{"oauth_enabled": bool, "client_id": "..."}`) | needed by extension |
| `GET` | `/notion/pages` | Destination picker | needed by extension |
| `GET` | `/admin/dlq` | DLQ inspection | ours |
| `POST` | `/admin/dlq/{job_id}/replay` | Replay a dead-lettered job | ours (SHOULD) |
| `GET`/`PUT` | `/admin/faults` | Runtime fault flags | ours (SHOULD) |

### 6.2 `POST /jobs` — submit a job
**Request**
```json
{
  "youtube_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
  "notion_token": "secret_... or ntn_...",
  "notion_parent_id": "optional-page-or-db-id"
}
```
Optional header: `Idempotency-Key: <uuid>`.

**Response — `202 Accepted`**, header `Location: /jobs/{job_id}`
```json
{ "job_id": "8f9c1d2e-…", "status": "QUEUED", "video_id": "dQw4w9WgXcQ" }
```

- **IR-API-1 (MUST)** Accepted URL forms: `youtube.com/watch?v=ID` (with or without `www.`/`m.`, extra params allowed), `youtu.be/ID`, `youtube.com/embed/ID`, `youtube.com/shorts/ID`, `youtube.com/live/ID`. `ID` must match `^[A-Za-z0-9_-]{11}$`. Anything else → `400 INVALID_URL`.
- **IR-API-2 (MUST)** Missing/empty `notion_token` → `400 MISSING_NOTION_TOKEN`. Malformed JSON → `400 INVALID_JSON`. Unknown fields are ignored. Body > 16 KB → `413 PAYLOAD_TOO_LARGE`. Wrong content type → `415 UNSUPPORTED_MEDIA_TYPE`.
- **IR-API-3 (MUST)** `notion_parent_id`, if present, must be a Notion ID (32 hex chars, with or without dashes, or a Notion URL from which it is extracted) → else `400 INVALID_NOTION_PARENT`.
- **IR-API-4 (MUST)** Returns within 200 ms p95 (no external calls).
- **IR-API-5 (MUST)** Error body shape for every 4xx/5xx: `{"error": {"code": "INVALID_URL", "message": "youtube_url must be a YouTube video link"}}`.
- **IR-API-6 (SHOULD)** If the same `Idempotency-Key` is reused within 24 h, returns the original job (`202`, same body) instead of creating a new one. Different clicks use different keys, so a burst of clicks produces independent jobs (source spec §9).
- **IR-API-7 (MUST)** Each submission creates an independent job even for the same video (no cross-job deduplication).

### 6.3 `GET /jobs/{job_id}` — poll status
**Response — `200 OK`**
```json
{
  "job_id": "8f9c1d2e-…",
  "status": "EXTRACTING_FRAMES",
  "can_cancel": true,
  "progress": 68,
  "video_id": "dQw4w9WgXcQ",
  "video_title": "Lecture 4: Consensus",
  "duration_sec": 1147,
  "warnings": [],
  "error": null,
  "result": null,
  "waiting": null,
  "attempt": 1,
  "created_at": "2026-11-12T10:00:00Z",
  "updated_at": "2026-11-12T10:01:07Z"
}
```
While waiting for a retry:
```json
"waiting": { "reason": "RATE_LIMITED", "provider": "llm", "next_attempt_at": "2026-11-12T10:01:37Z" }
```
- **IR-API-10 (MUST)** Contains at least every field shown in source spec §5 (`job_id, status, progress, video_title, duration_sec, warnings, error, result`). Extra fields are additive.
- **IR-API-11 (MUST)** Read-only and idempotent — polling never mutates job state.
- **IR-API-12 (MUST)** Unknown or malformed `job_id` → `404 JOB_NOT_FOUND`.
- **IR-API-13 (MUST)** `result` is populated (same object as `/results`) once the job is `COMPLETED`/`COMPLETED_WITH_WARNINGS`; `error` is `{code, message}` once `FAILED`/`DEAD_LETTERED`.
- **IR-API-14 (COULD)** `?include=events` adds the `job_events` timeline.

#### Cancel a job with `POST /jobs/{id}/cancel`

Send an empty POST for the job UUID. The `can_cancel` field in the status response indicates eligibility at the time of polling. The gateway checks it again when processing the request. Cancellation is available during publishing preparation, including frame uploads, until the worker records its intent to create the Notion page.

| Response | Meaning |
|---|---|
| `200 OK` | The current job view shows `CANCELLED`, `can_cancel: false`, and null `waiting`, `error`, and `result`. Repeating a successful request is idempotent. |
| `404 JOB_NOT_FOUND` | The job ID is malformed or unknown. |
| `409 JOB_NOT_CANCELLABLE` | Page creation has started, a page is already recorded, or the job finished in another terminal state. |
| `403 ORIGIN_FORBIDDEN` | The request supplies a browser origin outside the gateway allowlist. |

Cancellation clears the lease and retry state. A worker interrupts active work when it detects the lost lease. Late retry deliveries are acknowledged without restarting the job. The results endpoint returns `200` with status `CANCELLED`, warnings and a null result.

### 6.4 `GET /jobs/{job_id}/results` — fetch the outcome
**Response — `200 OK`** (terminal job)
```json
{
  "status": "COMPLETED_WITH_WARNINGS",
  "result": {
    "notion_page_url": "https://www.notion.so/Lecture-4-Consensus-Notes-1a2b3c…",
    "frames_requested": 6,
    "frames_published": 5
  },
  "warnings": [ { "code": "FRAME_EXTRACTION_FAILED", "timestamp_sec": 461 } ]
}
```
- **IR-API-20 (MUST)** Before the job is terminal → `409 JOB_NOT_FINISHED` with the standard error body (and current `status` in the message). It never long-polls.
- **IR-API-21 (MUST)** On success, `notion_page_url` is a working, **absolute** Notion URL.
- **IR-API-22 (MUST)** For `FAILED`/`DEAD_LETTERED`: `200` with `{"status": "FAILED", "result": null, "warnings": [...], "error": {"code", "message"}}`.
- **IR-API-23 (MUST)** Unknown job → `404 JOB_NOT_FOUND`.

### 6.5 `POST /auth/notion/exchange` and `GET /notion/pages`
```
POST /auth/notion/exchange   {"code": "...", "redirect_uri": "https://<id>.chromiumapp.org/notion"}
→ 200 {"access_token": "...", "workspace_name": "...", "workspace_icon": "...", "bot_id": "...", "duplicated_template_id": "..."|null}
→ 400 OAUTH_CODE_INVALID | 502 NOTION_UNAVAILABLE

GET /notion/pages   Authorization: Bearer <notion_token>
→ 200 {"pages": [{"id": "...", "title": "...", "type": "page|database", "icon": "📺"}]}
→ 401 NOTION_UNAUTHORIZED
```

### 6.6 Error and warning codes
**API error codes**

| Code | HTTP | Meaning |
|---|---|---|
| `INVALID_JSON` | 400 | Body is not valid JSON |
| `INVALID_URL` | 400 | Not a recognised YouTube video URL |
| `MISSING_NOTION_TOKEN` | 400 | `notion_token` missing or empty |
| `INVALID_NOTION_PARENT` | 400 | `notion_parent_id` malformed |
| `PAYLOAD_TOO_LARGE` | 413 | Body > 16 KB |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Not JSON |
| `JOB_NOT_FOUND` | 404 | Unknown job |
| `JOB_NOT_FINISHED` | 409 | Results requested before terminal |
| `UNAUTHORIZED` | 401 | Missing/invalid admin token |
| `INVALID_LIMIT` | 400 | `GET /admin/dlq` `limit` is not a positive integer |
| `JOB_NOT_DEAD_LETTERED` | 409 | Replay requested for a job that is not `DEAD_LETTERED` |
| `INVALID_FAULTS` | 400 | `PUT /admin/faults` has an unknown key or an out-of-range / malformed value |
| `INTERNAL` | 500 | Unexpected error (details only in logs) |

**Job error codes** (`error.code` on `FAILED` / `DEAD_LETTERED`)

| Code | Status | Retryable | Popup message |
|---|---|---|---|
| `VIDEO_TOO_LONG` | FAILED | no | "This video is longer than 20 minutes, which isn't supported." |
| `NO_TRANSCRIPT` | FAILED | no | "This video has no captions, so notes can't be generated." |
| `NOTION_UNAUTHORIZED` | FAILED | no | "Notion access was revoked. Please reconnect Notion." |
| `NOTION_PARENT_NOT_FOUND` | FAILED | no | "The chosen Notion page isn't shared with the app." |
| `NOTION_NO_DESTINATION` | FAILED | no | "Share at least one Notion page with the app." |
| `LLM_CONFIG_ERROR` | FAILED | no | "The AI service isn't configured correctly." |
| `RETRIES_EXHAUSTED` | DEAD_LETTERED | — | "We couldn't process this video after several attempts (it may be private or unavailable)." |
| `RATE_LIMIT_RETRIES_EXHAUSTED` | DEAD_LETTERED | — | "Services stayed busy for too long. Please try again later." |
| `DELIVERY_LIMIT_EXCEEDED` | DEAD_LETTERED | — | "Processing kept crashing on this video." |

**Warning codes** (`warnings[]`, any → `COMPLETED_WITH_WARNINGS`)

| Code | Extra fields | Meaning |
|---|---|---|
| `FRAME_EXTRACTION_FAILED` | `timestamp_sec` | Capture failed; frame omitted |
| `FRAME_NOT_RELEVANT` | `timestamp_sec` | Verification found no visual; frame omitted |
| `VISUAL_DROPPED_INVALID` | `timestamp_sec` | LLM gave an invalid timestamp/caption; dropped |
| `FRAME_VERIFY_SKIPPED` | — | Verification unavailable; frames kept unverified |

Informational notes that do **not** trigger with-warnings are kept in `job_events` only (e.g. auto-generated captions used, caption revised by verification).

### 6.7 `GET /healthz`
```json
200 {"status": "ok", "checks": {"postgres": "ok", "rabbitmq": "ok", "redis": "ok"}, "workers_alive": 2, "version": "1.0.0"}
503 {"status": "degraded", "checks": {"postgres": "ok", "rabbitmq": "error: connection refused", "redis": "ok"}, "workers_alive": 0}
```
- **IR-API-30 (MUST)** Returns `200` only if Postgres (`SELECT 1`), RabbitMQ (channel open + passive declare of `jobs.work`), and Redis (`PING`) are all reachable within 2 s; else `503`.
- **IR-API-31 (SHOULD)** `workers_alive` counts live worker heartbeat keys in Redis (informational; does not affect status code).
- Workers expose their own `:8081/healthz` used by the Docker healthcheck.

---

## 7. Data design

### 7.1 PostgreSQL schema (initial migration)
```sql
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
```
Migrations run automatically at gateway start-up (`golang-migrate`), guarded by an advisory lock.

### 7.2 Data retention
- Frame bytes (`image_jpeg`) are cleared 24 h after a job is terminal (sweeper in gateway); metadata is kept for the demo.
- Encrypted tokens exist only in RabbitMQ messages and unpublished outbox rows — never in `jobs`.

### 7.3 Redis keys
| Key | Type | Purpose | TTL |
|---|---|---|---|
| `rl:{provider}:{scope}` | hash | Token bucket (`tokens`, `ts`) | auto |
| `worker:alive:{worker_id}` | string | Liveness heartbeat | `WORKER_HEARTBEAT_TTL` (6 s) |
| `faults` | hash | Runtime fault-injection overrides | none |

---

## 8. Messaging design

### 8.1 RabbitMQ topology
| Entity | Type | Settings | Purpose |
|---|---|---|---|
| `jobs` | direct exchange, durable | — | Main exchange |
| `jobs.work` | **quorum** queue | DLX → `jobs.dlx`; delivery limit applied by **policy** (below), not a queue argument | Work queue; delivery limit catches crash loops (poison messages) |
| `jobs.retry.<delay>` | classic durable queue, one per `RETRY_DELAYS` entry (e.g. `jobs.retry.10s`) | `x-message-ttl = delay`, `x-dead-letter-exchange = jobs`, `x-dead-letter-routing-key = work` | Delayed retry: message waits, then returns to `jobs.work` |
| `jobs.retry.defer` | classic durable | `x-message-ttl = DEFER_DELAY` (default 3 s), `x-dead-letter-exchange = jobs`, `x-dead-letter-routing-key = work` | Short re-try when another live worker still holds the job's lease. **Expired messages return to `jobs.work` — never dropped.** |
| `jobs.dlx` | fanout exchange, durable | — | Dead-letter exchange |
| `jobs.dlq` | classic durable queue bound to `jobs.dlx` | no TTL, **never consumed by code** | Dead-lettered jobs kept for manual inspection (RabbitMQ UI, `GET /admin/dlq`) |
| `jobs.dlq.audit` | classic durable queue bound to `jobs.dlx` | — | Copy of every dead-lettered message, consumed by the gateway's DLQ watcher to update job status (FR-RET-6) |

- **FR-MQ-0 (MUST)** RabbitMQ image is pinned to `rabbitmq:3.13-management` (4.x changes quorum-queue defaults). The delivery limit is set with a **policy** (`jobs-work-limits`: pattern `^jobs\.work$`, `delivery-limit = DELIVERY_LIMIT`, applied via `rabbitmqctl set_policy` / a definitions file at start-up), because changing a queue argument on an existing durable queue fails with `406 PRECONDITION_FAILED` and would stop the stack from starting. Retry queue names encode their delay, so changing `RETRY_DELAYS` creates new queues instead of conflicting with old ones.

Per-delay retry queues (rather than per-message TTL on one queue) avoid head-of-line blocking where a 90 s message would hold up a 10 s one.

### 8.2 Message format
```json
{
  "v": 1,
  "job_id": "8f9c1d2e-…",
  "notion_token_enc": "base64(nonce|ciphertext)",
  "enqueued_at": "2026-11-12T10:00:00Z",
  "reason": "new|retry|defer|replay"
}
```
Persistent (`delivery_mode=2`), `content_type=application/json`, `message_id=job_id`. Attempt counters live in Postgres, not the message (single source of truth).

### 8.3 Delivery semantics
- **FR-MQ-1 (MUST)** Publishers (gateway outbox relay, worker retry/DLQ publishes) use **publisher confirms**; a publish counts only once confirmed.
- **FR-MQ-2 (MUST)** Consumers use **manual acknowledgement** and `prefetch = WORKER_CONCURRENCY`.
- **FR-MQ-3 (MUST)** A message is acknowledged only **after** the stage results it represents are durably committed to Postgres, or after its retry/DLQ copy is confirmed by the broker. Never before.
- **FR-MQ-4 (MUST)** If a worker dies (SIGKILL, OOM), the OS closes its TCP connection and RabbitMQ immediately redelivers every unacknowledged message to a healthy worker. On a silent network loss, RabbitMQ detects it after missed heartbeats (≈ 2 × `AMQP_HEARTBEAT`, default 10 s → ~20 s).
- **FR-MQ-5 (MUST)** Consumers reconnect automatically with backoff if the broker restarts; topology is (re)declared idempotently on connect by both gateway and worker.

---

## 9. Failure handling and reliability

This section implements source spec §6. Each failure mode must be **demonstrable live** (scripts in [§15.4](#154-demo-scenarios-graded-live)).

### 9.1 Error taxonomy (the contract between stages and the worker runtime)
Stages return errors classified into exactly one class:

| Class | Examples | Worker action |
|---|---|---|
| `RateLimited{provider, retry_after}` | HTTP 429 from Gemini/backup LLM, Notion, YouTube (yt-dlp "HTTP Error 429") | Rate-limit retry (§9.2) — does **not** consume the failure budget |
| `Retryable{code}` | Network errors, timeouts, 5xx, invalid LLM JSON after repair, private/unavailable video, yt-dlp generic failure | Failure retry (§9.2) |
| `Fatal{code}` | `VIDEO_TOO_LONG`, `NO_TRANSCRIPT`, `NOTION_UNAUTHORIZED`, `NOTION_PARENT_NOT_FOUND`, `NOTION_NO_DESTINATION`, `LLM_CONFIG_ERROR` | `FAILED` immediately, ack |
| (warnings) | Per-frame failures | Not errors — recorded as warnings, stage continues |

Unclassified errors default to `Retryable` (fail safe: bounded, ends in DLQ, never infinite).

### 9.2 Retries, backoff, and dead-lettering
- **FR-RET-1 (MUST)** Two independent, bounded, configurable budgets:
  - **Failure budget:** `MAX_ATTEMPTS` (default 5 total attempts).
  - **Rate-limit budget:** `MAX_RATE_LIMIT_RETRIES` (default 8).
- **FR-RET-2 (MUST)** Backoff schedule `RETRY_DELAYS` (default `10s,30s,90s,270s`; demo profile `3s,6s,12s,24s`). The n-th retry uses the n-th delay (last value repeats). For `RateLimited`, the delay is `max(schedule delay, Retry-After)`, rounded up to the nearest configured retry queue.
- **FR-RET-3 (MUST)** Retry procedure: (1) increment the relevant counter, set `waiting_reason`, `waiting_provider`, `next_attempt_at`, append `job_events` — commit; (2) publish the message to `jobs.retry.<delay>` with confirms; (3) ack the original. A crash between steps can only cause a duplicate delivery, which the lease (§9.4) and idempotent stages absorb.
- **FR-RET-4 (MUST)** While waiting, the job keeps its current stage status (e.g. `SUMMARIZING`) and `GET /jobs/{id}` exposes `waiting`; the popup shows a waiting state, not an error (source spec §6).
- **FR-RET-5 (MUST)** Budget exhausted → status `DEAD_LETTERED` with `RETRIES_EXHAUSTED` or `RATE_LIMIT_RETRIES_EXHAUSTED`, message published to `jobs.dlx` (→ `jobs.dlq`), original acked.
- **FR-RET-6 (MUST)** Crash loops: if a job's message exceeds the quorum queue's delivery limit (because workers keep dying on it), RabbitMQ dead-letters it to `jobs.dlx`, which fans out to both `jobs.dlq` (kept for inspection) and `jobs.dlq.audit`. The gateway's DLQ watcher consumes only `jobs.dlq.audit` and marks such jobs `DEAD_LETTERED` with `DELIVERY_LIMIT_EXCEEDED` (if not already terminal). Explicit dead-lettering by workers (FR-RET-5) publishes to `jobs.dlx` the same way, so every dead-lettered job is visible in `jobs.dlq`.
- **FR-RET-7 (MUST)** Demonstrable timing: with the demo profile, a private video reaches `DEAD_LETTERED` in under 1 minute; with defaults, under ~7 minutes.

### 9.3 Idempotency and "no duplicates on Notion"
- **FR-IDEM-1 (MUST)** Every stage writes its result with an upsert keyed by `(job_id, kind)` / `(job_id, idx)`; re-running a stage overwrites, never appends duplicates.
- **FR-IDEM-2 (MUST)** Stage completion and the status transition to the next stage are committed in **one transaction**, so "result saved" and "status advanced" can never disagree.
- **FR-IDEM-3 (MUST) — Notion page creation at most once:**
  1. If `notion_page_id` is set → reuse the page.
  2. Else set `notion_create_started_at = now()` (commit), create the page **with a title containing the job's short ID** (e.g. `Lecture 4 — Notes · a1b2c3`), then save `notion_page_id`/`url` immediately (commit).
  3. On a rerun where `notion_create_started_at` is set but `notion_page_id` is null (crash in the tiny window between create and save), search the parent's child pages for that short-ID title; if found, adopt it instead of creating another.
- **FR-IDEM-4 (MUST) — Content written convergently:** publishing writes the page body as "replace all children": on a rerun it first archives any existing child blocks of the page, then appends the full block list. A crash halfway through appending therefore cannot leave duplicate sections after a successful rerun.
- **FR-IDEM-5 (MUST)** Frame uploads are recorded per frame (`notion_file_upload_id`) and skipped on rerun unless older than `NOTION_UPLOAD_REUSE_WINDOW` (default 50 min; Notion expires unattached uploads after ~1 h).
- **FR-IDEM-6 (MUST)** `POST /jobs` with a repeated `Idempotency-Key` returns the original job (IR-API-6).

### 9.4 Worker crashes and leases
- **FR-CRASH-1 (MUST)** On receiving a message, the worker **claims** the job with a conditional update:
  ```sql
  UPDATE jobs SET lease_owner = $worker, lease_until = now() + $LEASE_TTL
  WHERE id = $job AND status NOT IN (terminal)
    AND (lease_owner IS NULL OR lease_owner = $worker OR lease_until < now()
         OR NOT $owner_alive)   -- liveness checked in Redis beforehand
  ```
- **FR-CRASH-2 (MUST)** Each worker writes `worker:alive:{id}` every `WORKER_HEARTBEAT_INTERVAL` (2 s) with TTL `WORKER_HEARTBEAT_TTL` (6 s), and renews leases of jobs it holds every `LEASE_TTL/3`. A job held by a dead worker (heartbeat key missing) can be claimed immediately; otherwise after `LEASE_TTL` (default 30 s) as a fallback.
- **FR-CRASH-3 (MUST)** If the lease is held by a live worker (a duplicate delivery), the message is moved to `jobs.retry.defer` (does not consume any budget; capped by `MAX_DEFERRALS`, default 30) and acked.
- **FR-CRASH-4 (MUST)** Graceful shutdown on SIGTERM: stop consuming, let the current stage finish up to `SHUTDOWN_GRACE` (25 s), then `nack(requeue=true)`; release the lease.
- **FR-CRASH-5 (MUST)** Recovery target: after `docker kill -s KILL <worker>` mid-job, another worker resumes the job within **≤ 15 s**. Timeline: redelivery is immediate (connection closes on SIGKILL); the dead worker's liveness key may survive up to 6 s, so the new worker may defer once or twice through `jobs.retry.defer` (3 s each, returning to `jobs.work`); the claim succeeds once the key expires (≤ ~9 s). Only the interrupted stage and only its unfinished frames are redone, and the job completes with exactly one Notion page.

### 9.5 Graceful degradation
- **FR-DEG-1 (MUST)** Partial frame failure → publish text + successful frames, status `COMPLETED_WITH_WARNINGS`, `warnings[]` lists each omitted timestamp.
- **FR-DEG-2 (MUST)** All frames fail → publish a text-only page, `COMPLETED_WITH_WARNINGS`.
- **FR-DEG-3 (MUST)** Frame verification failure never fails the job (FR-PIPE-24).

### 9.6 Fault injection
For live demos and tests. Flags are read from env at start-up and can be overridden at runtime through Redis (`PUT /admin/faults`), checked on every call.

| Flag | Effect |
|---|---|
| `FAULT_LLM_429_RATE` (0–1) | Probability an LLM call returns a synthetic 429 (with `Retry-After`) before calling the provider |
| `FAULT_NOTION_429_RATE` | Same for Notion calls |
| `FAULT_YOUTUBE_429_RATE` | Same for yt-dlp calls |
| `FAULT_FRAME_FAIL_RATE` | Probability a frame capture fails |
| `FAULT_FRAME_FAIL_ALL` (bool) | Every frame capture fails (text-only page demo) |
| `FAULT_STAGE_DELAY_MS` | Artificial delay per frame, to make "kill during frame extraction" easy to time |

Synthetic 429s go through exactly the same code path as real ones. Fault injection is **off by default** and logged loudly when on.

---

## 10. Rate limiting

- **FR-RL-1 (MUST)** A global token-bucket limiter in Redis, shared by all workers, implemented as an atomic Lua script (refill by elapsed time, take N tokens or return wait time).
- **FR-RL-2 (MUST)** Buckets:

| Bucket | Default | Scope |
|---|---|---|
| `llm:requests` | `LLM_RPM` (default 8/min — set ≈ 80 % of the provider's actual limit) | global |
| `llm:tokens` | `LLM_TPM` (default 200 000/min) — estimated input + max output tokens | global |
| `llm:daily` | `LLM_RPD` (default **200**; set to ≈ 80 % of the provider's daily quota shown in its console, `0` = off only for Ollama) | global, resets at the provider's reset time (configurable `LLM_RPD_RESET_TZ`, default Pacific time for Gemini) |
| `notion:{hash(token)}` | `NOTION_RPS` (default 2.5/s; Notion allows ~3/s average) | per integration token |
| `youtube` | `YOUTUBE_RPS` (default 4/s, burst of one second) | global |

- **FR-RL-3 (MUST)** Before every external call, the caller acquires tokens. If the wait is ≤ `LIMITER_MAX_WAIT` (default 60 s), it blocks (context-cancellable). If longer, the stage returns a local `RateLimited{provider, retry_after}`, which goes through the normal rate-limit retry path — the worker is freed for other work and the user sees "waiting".
- **FR-RL-4 (MUST)** Goal (source spec §9): a burst of ≥ 6 jobs drains **without a single 429 from the LLM provider** — per-minute *or* daily — under the configured limits. Each job uses at most `chunks + 2` LLM calls (synthesis + up to two batched verifications), so a 6-job burst needs ≈ 12–20 calls. Every real provider 429 is counted (`provider_429_total`, shown in `GET /healthz` details) so this can be proven in the demo.
- **FR-RL-5 (MUST)** If Redis is unreachable, workers stop taking new jobs (fail closed; `healthz` reports it) rather than calling providers unthrottled.
- **FR-RL-6 (MUST)** Limits are configurable via env and listed in the README (source spec §8).

---

## 11. LLM design

### 11.1 Provider interface
```go
type Provider interface {
    Name() string
    // Generate returns JSON text conforming to schema.
    GenerateJSON(ctx context.Context, req JSONRequest) (JSONResponse, error)
    // Vision: same, with images attached.
    SupportsImages() bool
}
type JSONRequest struct {
    System     string
    Prompt     string
    Images     []Image          // JPEG bytes + label
    Schema     *jsonschema.Schema
    MaxOutputTokens int
}
```
- **FR-LLM-1 (MUST)** Implementations: `gemini` (primary), `ollama` (dev). **SHOULD:** `groq`, `openrouter`. **COULD:** `claude`. Selected by `LLM_PROVIDER`; vision calls use `LLM_VISION_PROVIDER` (defaults to `LLM_PROVIDER`; must support images).
- **FR-LLM-2 (MUST)** Each provider maps its errors into the taxonomy (§9.1): 429 → `RateLimited` (parsing `Retry-After` / provider retry info), 5xx/timeout → `Retryable`, 401/403/invalid model → `Fatal(LLM_CONFIG_ERROR)`.
- **FR-LLM-3 (SHOULD)** Outage failover: if `LLM_FALLBACK_PROVIDER` is set and the primary returns 5xx/timeouts (not 429s) twice in a row for a call, the call is retried once on the fallback provider (which has its own rate-limit bucket).
- **FR-LLM-4 (MUST)** Uses each provider's native structured-output mode where available (Gemini response schema, Groq/OpenRouter JSON mode, Ollama `format`), plus our own validation (FR-PIPE-12).
- **FR-LLM-5 (MUST)** All prompts live in versioned files (`internal/pipeline/synthesis/prompts/*.tmpl`) — not inline strings.

### 11.2 Output schema
```json
{
  "title": "string",
  "overview": "string (2-4 sentences)",
  "sections": [
    { "heading": "string", "start_sec": 0, "end_sec": 192,
      "bullets": ["string", "..."] }
  ],
  "visuals": [
    { "timestamp_sec": 161, "section_index": 0,
      "caption": "What is on screen (1 sentence)",
      "reason": "Why it was flagged / why it matters (1 sentence)",
      "priority": 1 }
  ]
}
```

### 11.3 Prompting rules
- Input transcript is formatted as `[mm:ss] text` lines so the model can cite real times.
- The model is told to flag only moments where the speaker refers to something **on screen** (cues: "as you can see", "this diagram", "on this slide", "here we have", equations, code, charts) and to prefer the moment the visual is fully shown.
- Bullets must be faithful to the transcript (no outside facts); 3–7 bullets per section; 3–10 sections for a 20-minute video.
- Notes language: `NOTES_LANGUAGE` (default English).

### 11.4 Chunking and merge
- Token estimate: `ceil(chars / 4)`. If transcript ≤ `LLM_MAX_INPUT_TOKENS` (default 6000, safe for 8K-context backups) → single call.
- Else split into chunks on segment boundaries of ≤ `LLM_MAX_INPUT_TOKENS`, with 30 s overlap. Each chunk call is told its time range and asked for the same schema.
- Merge: concatenate sections in chunk order; drop the overlap duplicates (a section whose `start_sec` falls inside the previous chunk's covered range is merged into the previous section); concatenate visuals, then dedupe (FR-PIPE-13). A final light call produces the overall `title` and `overview` from the section headings (COULD skip: use the first chunk's title and join overviews).
- Each chunk result is checkpointed as `synthesis_chunk:<n>`.

### 11.5 Frame verification
One call per job: all captured frames (downscaled to ≤ 768 px wide) labeled `#0..#n`, plus each frame's caption and reason. Returns `[{index, shows_visual, matches_caption, revised_caption}]`. Policy in FR-PIPE-24. Counts against the same LLM buckets.

### 11.6 Provider strategy and backups
| Order | Provider | Cost | Role |
|---|---|---|---|
| 1 | **Gemini Flash (free tier)** | $0 | Primary for integration testing and the demo. Supports images (verification). |
| — | **Ollama (local)** | $0 | Development, so daily quota isn't spent while debugging. Not used in the demo. |
| 2 | **Groq (free tier)** | $0 | Backup if Gemini's limits/availability fail. |
| 3 | **OpenRouter free models** | $0 | Second backup (limits to verify at setup). |
| 4 | **Claude Haiku 5.5** | ≈ $0.0015/video, ~$5 total | Last resort if no free option works. |

**Decision gate (12 Nov):** one real 20-minute video must complete through Gemini with clean JSON. If quotas block testing, switch `LLM_PROVIDER` the same day. Fault injection (§9.6) makes the 429 demo work on any provider.

---

## 12. Non-functional requirements

### 12.1 Performance
- **NFR-PERF-1 (MUST)** `POST /jobs` p95 ≤ 200 ms; `GET` endpoints p95 ≤ 50 ms.
- **NFR-PERF-2 (SHOULD)** End-to-end latency for a ~20-minute video on an idle system: **target ≤ 90 s, upper bound 3 min** (dominated by LLM calls and frame seeks).
- **NFR-PERF-3 (SHOULD)** A burst of 6 videos (clicked in a row) with 2 workers fully drains within ~10 min under free-tier limits, with zero LLM-provider 429s.
- **NFR-PERF-4 (MUST)** Frames are captured by seeking in the remote stream; the full video is never downloaded.

### 12.2 Reliability
- **NFR-REL-1 (MUST)** Killing or scaling any single container does not require touching others; the system recovers automatically when it returns.
- **NFR-REL-2 (MUST)** No job is ever lost (outbox + durable queues + persistent messages + manual acks).
- **NFR-REL-3 (MUST)** No job ends with two Notion pages or duplicated content.
- **NFR-REL-4 (MUST)** Every job reaches a terminal state in bounded time (bounded budgets + delivery limit).
- **NFR-REL-5 (MUST)** Gateway and workers restart automatically (`restart: unless-stopped`) and wait for dependencies to be healthy (`depends_on: condition: service_healthy`).

### 12.3 Security
- **NFR-SEC-1 (MUST)** Notion client secret only in gateway env. Notion access tokens only in `chrome.storage.local`, encrypted in transit through the broker, never persisted in plaintext, never logged.
- **NFR-SEC-2 (MUST)** No secrets committed: `.env` is git-ignored; `.env.example` documents every variable.
- **NFR-SEC-3 (MUST)** Subprocesses (`yt-dlp`, `ffmpeg`) are invoked with argument arrays (never a shell string) and the video ID is validated by regex first — no command injection.
- **NFR-SEC-4 (MUST)** Admin endpoints require `Authorization: Bearer $ADMIN_TOKEN`.
- **NFR-SEC-5 (SHOULD)** Extension requests only the permissions it needs: `storage`, `identity`, `alarms`, `notifications`, host permissions for `youtube.com` and the gateway URL.

### 12.4 Maintainability and code quality
- **NFR-MAIN-1 (MUST)** Go 1.23+, `gofmt`, `go vet`, `golangci-lint` clean; TypeScript strict mode.
- **NFR-MAIN-2 (MUST)** Each pipeline stage and provider is behind an interface with a fake for tests.
- **NFR-MAIN-3 (SHOULD)** CI on GitHub Actions: lint, unit tests, integration tests (testcontainers), extension build.
- **NFR-MAIN-4 (MUST)** Config is validated at start-up; a missing required variable fails fast with a clear message.

### 12.5 Usability
- **NFR-UX-1 (MUST)** The user never sees raw JSON, stack traces, or internal codes in the extension.
- **NFR-UX-2 (SHOULD)** Popup works at 360 px wide, supports light/dark mode, and every status uses words, not only colour.
- **NFR-UX-3 (MUST)** Times are shown as `mm:ss` (or `h:mm:ss`).

### 12.6 Portability
- **NFR-PORT-1 (MUST)** Whole backend starts with one command on Windows, macOS, and Linux via Docker Compose. Worker image bundles pinned versions of `yt-dlp` and `ffmpeg`.

---

## 13. Configuration

All configuration is via environment variables (`.env`, documented in `.env.example` and the README).

| Variable | Default | Used by | Purpose |
|---|---|---|---|
| `GATEWAY_PORT` | `8080` | gateway | Listen port |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | gateway | Used to build `Location` headers |
| `DATABASE_URL` | `postgres://notes:notes@postgres:5432/notes?sslmode=disable` | both | Postgres connection |
| `AMQP_URL` | `amqp://guest:guest@rabbitmq:5672/` | both | **Broker connection string** |
| `AMQP_HEARTBEAT` | `10s` | both | Broker heartbeat |
| `REDIS_URL` | `redis://redis:6379/0` | both | Redis connection |
| `TOKEN_ENC_KEY` | — (required, 32-byte base64) | both | Notion token encryption |
| `ADMIN_TOKEN` | — (required) | gateway | Admin endpoints |
| `CORS_ALLOWED_ORIGINS` | `chrome-extension://<pinned-id>` | gateway | CORS |
| `NOTION_CLIENT_ID` | empty (required for OAuth Mode B) | gateway | **Notion OAuth client ID** |
| `NOTION_CLIENT_SECRET` | empty (required for OAuth Mode B) | gateway | **Notion OAuth client secret** |
| `NOTION_REDIRECT_URI` | `https://<pinned-id>.chromiumapp.org/notion` | gateway | OAuth redirect |
| `NOTION_DEFAULT_PARENT_ID` | empty | worker | Default destination override (§5.4.3) |
| `NOTION_RPS` | `2.5` | gateway, worker | Notion rate limit |
| `NOTION_VERSION` | `2025-09-03` | gateway, worker | Pinned Notion API version |
| `NOTION_MAX_IMAGE_BYTES` | `4500000` | worker | Max frame upload size |
| `LLM_PROVIDER` | `gemini` | worker | **LLM provider** (`gemini`, `ollama`, `groq`, `openrouter`, `claude`) |
| `LLM_MODEL` | provider-specific, e.g. a current Gemini Flash model | worker | **LLM model name** |
| `LLM_VISION_PROVIDER` / `LLM_VISION_MODEL` | = `LLM_PROVIDER` / `LLM_MODEL` | worker | Verification model |
| `LLM_FALLBACK_PROVIDER` / `LLM_FALLBACK_MODEL` | empty | worker | Outage failover |
| `GEMINI_API_KEY` / `GROQ_API_KEY` / `OPENROUTER_API_KEY` / `ANTHROPIC_API_KEY` | — | worker | **LLM API key** (only the selected provider's is required) |
| `OLLAMA_URL` | `http://ollama:11434` | worker | Local provider |
| `LLM_RPM` | `8` | worker | **Configured LLM rate limit** (requests/min) |
| `LLM_TPM` | `200000` | worker | LLM tokens/min |
| `LLM_RPD` | `200` | worker | LLM requests/day (≈ 80 % of provider daily quota; `0` = off) |
| `LLM_RPD_RESET_TZ` | `America/Los_Angeles` | worker | When the daily bucket resets |
| `LLM_MAX_INPUT_TOKENS` | `6000` | worker | Chunking threshold |
| `LLM_TIMEOUT` | `90s` | worker | Per-call timeout |
| `LIMITER_MAX_WAIT` | `60s` | worker | Block vs. requeue threshold |
| `YOUTUBE_RPS` | `4` | worker | YouTube rate limit (yt-dlp calls and ffmpeg stream reads) |
| `YTDLP_COOKIES_FILE` | empty | worker | Optional cookies if YouTube blocks requests |
| `MAX_VIDEO_SECONDS` | `1200` | worker, extension | Constraint A |
| `MAX_FRAMES` | `8` | worker | Frame cap |
| `FRAME_CONCURRENCY` | `4` | worker | Parallel frame captures |
| `FRAME_TIMEOUT` | `30s` | worker | Timeout per ffmpeg call (token waits excluded) |
| `FRAME_VERIFY` | `true` | worker | Verification on/off |
| `SCENE_THRESHOLD` | `0.3` | worker | Slide-snap sensitivity |
| `MAX_FRAME_DRIFT_SEC` | `3` | worker | Max distance of captured frame from LLM timestamp |
| `FRAME_DEDUP_WINDOW_SEC` | `5` | worker | Visual dedupe |
| `NOTES_LANGUAGE` | `English` | worker | Output language |
| `MAX_ATTEMPTS` | `5` | worker | **Failure retry budget** |
| `MAX_RATE_LIMIT_RETRIES` | `8` | worker | **429 retry budget** |
| `RETRY_DELAYS` | `10s,30s,90s,270s` | worker, gateway (topology) | **Backoff schedule** |
| `DEFER_DELAY` | `3s` | worker, gateway (topology) | Lease-busy deferral |
| `MAX_DEFERRALS` | `30` | worker | Deferral cap |
| `DELIVERY_LIMIT` | `10` | gateway (policy) | Crash-loop cap, applied as a RabbitMQ policy |
| `LEASE_TTL` | `30s` | worker | Job lease |
| `WORKER_HEARTBEAT_INTERVAL` / `_TTL` | `2s` / `6s` | worker | Liveness |
| `WORKER_CONCURRENCY` | `1` | worker | Jobs per worker container |
| `SHUTDOWN_GRACE` | `25s` | worker | Graceful stop |
| `OUTBOX_SWEEP_INTERVAL` | `2s` | gateway | Outbox relay |
| `FAULT_*` | off | worker | Fault injection (§9.6) |
| `LOG_LEVEL` | `info` | both | Logging |

A `demo.env` overlay sets the fast demo profile (`RETRY_DELAYS=3s,6s,12s,24s`, `FAULT_STAGE_DELAY_MS=1500`).

---

## 14. Deployment and deliverables

### 14.1 Docker Compose
- **FR-DEP-1 (MUST)** One `deploy/docker-compose.yml` brings up `gateway`, `rabbitmq` (with management UI on `:15672`), `postgres`, `redis`, and `worker` with **`deploy.replicas: 2`** by default.
- **FR-DEP-2 (MUST)** `docker compose up --scale worker=N` works (workers have no fixed host ports or container names; each derives `worker_id` from its hostname).
- **FR-DEP-3 (MUST)** Every service has a healthcheck; gateway and workers start only after their dependencies are healthy.
- **FR-DEP-4 (MUST)** Named volumes for Postgres and RabbitMQ data, so `docker compose restart` keeps jobs and queued messages.
- **FR-DEP-5 (SHOULD)** Multi-stage Dockerfiles; the worker image contains pinned `yt-dlp` and `ffmpeg`.
- **FR-DEP-6 (SHOULD)** A `Makefile` (or `Taskfile`) with `up`, `down`, `logs`, `demo-*`, `test`, `ext-build`.

### 14.2 Extension
- **FR-DEP-10 (MUST)** Loadable as an unpacked extension after **one documented build command** (`npm --prefix extension ci && npm --prefix extension run build` → `extension/dist/`). The built `dist/` is also committed in release tags so graders can load it with no build step.

### 14.3 README (source spec §8)
The README MUST state, so a grader can run everything **without reading source code**:
1. Prerequisites (Docker, Chrome, a Notion account, a Gemini API key).
2. **Base URL** (`http://localhost:8080`).
3. Quick start: copy `.env.example`, fill the required values, `docker compose up -d`, check `/healthz`, build/load the extension, connect Notion.
4. **Default Notion destination behaviour** (§5.4.3).
5. **Every environment variable** (table from §13), clearly marking required ones: LLM API key + model name, configured rate limit, Notion OAuth client credentials, broker connection string, retry/backoff parameters.
6. How to register the Notion public integration (redirect URI, capabilities, template).
7. API reference with `curl` examples for all endpoints.
8. How to demo each failure mode (scripts in [§15.4](#154-demo-scenarios-graded-live)).
9. Architecture diagram and the design-choice justifications (from [§19](#19-design-decisions-log)).
10. Troubleshooting (YouTube bot checks, quota exhaustion, OAuth redirect mismatch).

### 14.4 Grader self-setup
Graders run the system on their own machines with their own keys (OQ-1). Getting from clone to first Notion page must take **≤ 15 minutes** for someone who has never seen the code.

- **FR-SETUP-1 (MUST) — Two Notion connection modes, both documented:**
  - **Mode A, Quick (recommended for graders):** create a Notion *internal* integration (2 minutes, no app review or redirect URI), share an "AI Video Notes" page with it, and paste its token into the popup's **Connect with integration token** field. The extension stores it exactly like an OAuth token. Requires no `NOTION_CLIENT_*` variables.
  - **Mode B, Full OAuth (the spec's one-time OAuth flow):** register a Notion *public* integration with redirect URI `https://<pinned-extension-id>.chromiumapp.org/notion` (the ID is the same on every machine because the manifest `key` pins it; the README prints it), then set `NOTION_CLIENT_ID` / `NOTION_CLIENT_SECRET`. The popup shows **Connect Notion** only when the gateway reports OAuth is configured (`GET /auth/notion/config` → `{"oauth_enabled": true|false}`).
  - Both modes exercise the same job pipeline; the README explains that Mode B is the spec's OAuth flow and Mode A is a shortcut.
- **FR-SETUP-2 (MUST) — One-command setup:** `scripts/setup.sh` (and `scripts/setup.ps1` for Windows) copies `.env.example` → `.env`, generates `TOKEN_ENC_KEY` and `ADMIN_TOKEN`, prompts for the LLM API key, and leaves optional values blank. Running it again never overwrites existing values.
- **FR-SETUP-3 (MUST) — Preflight check:** `GET /healthz?deep=1` (and `scripts/doctor.sh`) additionally verifies that the LLM key and model are valid (a zero-cost model-info call, no generation), that `yt-dlp` and `ffmpeg` run inside the worker image, that YouTube is reachable from the worker (metadata fetch for a known public video), and whether Notion OAuth is configured. Each failure gives a one-line fix ("GEMINI_API_KEY rejected — check the key in AI Studio"). Plain `/healthz` stays infra-only and fast.
- **FR-SETUP-4 (SHOULD) — Prebuilt images:** gateway and worker images are published to GitHub Container Registry on each release tag (free with GitHub). `docker-compose.yml` uses them by default, with `build:` as a fallback, so graders skip the Go and ffmpeg build.
- **FR-SETUP-5 (MUST) — Prebuilt extension:** each release attaches a zip of `extension/dist/` (and `dist/` is committed on the release tag), so graders load it unpacked with **no build step**; the one-command build stays documented for developers.
- **FR-SETUP-6 (MUST) — Gateway URL defaults** to `http://localhost:8080`, so a grader on a standard setup changes nothing in the extension.
- **FR-SETUP-7 (MUST) — Free-tier friendly defaults:** `LLM_RPM`, `LLM_RPD`, and the frame-verification switch default to values safe for a fresh Gemini free-tier key. The README says how to raise them for a paid key and how to switch provider (`LLM_PROVIDER`).
- **FR-SETUP-8 (MUST) — YouTube network issues:** the README's troubleshooting section explains the "confirm you're not a bot" error and how to supply `YTDLP_COOKIES_FILE`. Doctor output detects the error and points to that section.
- **FR-SETUP-9 (MUST) — Clean-machine rehearsal:** before submission, a teammate who didn't write the setup docs follows the README on a machine with no prior state (fresh clone, `docker compose down -v`, extension removed) and records the time to first page. It must be ≤ 15 minutes with no help.

---

## 15. Testing and verification

### 15.1 Unit tests (both)
- URL parsing (all forms in IR-API-1, plus invalid cases).
- State machine: every allowed/forbidden transition.
- Error classification for each provider/tool error.
- Transcript json3 parsing; chunking and merge (order preserved, overlap deduped).
- LLM output validation and visual dedupe/cap.
- Token-bucket Lua script (against a real Redis via testcontainers).
- Notion block builder (limits: 100 blocks, 2000-char rich text).
- Popup status/error → message mapping (extension, Vitest).

### 15.2 Integration tests (`testcontainers-go`: Postgres, RabbitMQ, Redis; fake stages/providers)
- Happy path end-to-end with fake stages.
- Retry: stage returns `Retryable` twice → job completes; attempts = 3.
- Rate limit: stage returns `RateLimited` → waiting exposed in GET; failure budget untouched.
- DLQ: always-`Retryable` → `DEAD_LETTERED` after `MAX_ATTEMPTS`, message in `jobs.dlq`.
- Crash: kill the consumer goroutine/connection mid-stage → redelivered → resumed from checkpoint; stage side-effect counters show no duplicate publish.
- Outbox: broker down at submit → job published when it returns.
- Duplicate delivery: same message delivered twice concurrently → lease makes one defer; one page.
- `GET` endpoints never change `updated_at`.

### 15.3 End-to-end tests (real services, run manually / nightly)
- 3 reference videos: ~5 min, ~12 min, ~19–20 min with slides; plus a > 20 min video, a private video, a no-caption video.
- Check: page exists, headings + bullets present, every image has a caption, `frames_published ≤ frames_requested`, timestamps sane.
- **Self-check for Constraint B (COULD):** a script that fetches the published page and asks the vision LLM whether each image looks like a video frame matching its caption — mirroring the grader's LLM-as-judge.

### 15.4 Demo scenarios (graded live) — `scripts/demo/`
| Script | Demonstrates | Expected outcome |
|---|---|---|
| `health.sh` | Sanity check | `/healthz` 200 with all checks ok, `workers_alive ≥ 2` |
| `happy.sh <url>` | Full flow via API | Status walk-through printed; Notion URL |
| `burst.sh` | 6 jobs in quick succession | All complete; `provider_429_total` stays 0 |
| `kill-worker.sh` | SIGKILL the worker holding a job in `EXTRACTING_FRAMES` | Job resumes on another worker ≤ 15 s; one Notion page |
| `force-429.sh [llm\|notion\|youtube]` | Sets fault rate via `/admin/faults` | Popup shows waiting + countdown; job completes |
| `dlq.sh <private-url>` | Private video | `DEAD_LETTERED` within ~1 min (demo profile); visible in `/admin/dlq` and RabbitMQ UI |
| `frames-fail.sh` | `FAULT_FRAME_FAIL_RATE=0.5`, then `FAULT_FRAME_FAIL_ALL` | `COMPLETED_WITH_WARNINGS`; then text-only page |
| `too-long.sh <url>` | > 20 min video | `FAILED VIDEO_TOO_LONG` without downloading captions |
| `scale.sh N` | `docker compose up -d --scale worker=N` | `workers_alive = N` |

A written `docs/demo-runbook.md` walks through these in order for the presentation.

### 15.5 Acceptance criteria (definition of done for submission)
1. Every MUST requirement implemented; traceability matrix (Appendix A) all ✅.
2. All demo scripts pass twice in a row on a clean `docker compose up`.
3. Three reference videos ≤ 20 min publish correct pages; > 20 min video rejected.
4. A teammate who didn't write the README can start the stack from it alone.

---

## 16. Team split and module contracts

### 16.1 Ownership (≈ 8 working days each)
| Person A — Platform & Reliability | Days | Person B — Pipeline & Client | Days |
|---|---|---|---|
| Repo skeleton, Compose, Dockerfiles, `/healthz` | 0.5 | Transcript stage (yt-dlp metadata, captions, json3 parser, Constraint A) | 1 |
| Postgres schema, migrations, job repository, state machine | 1 | LLM layer: Provider interface, Gemini + Ollama, prompts, chunk/merge, validation | 2 |
| Gateway API (§6), validation, idempotency key, outbox relay | 1.5 | Frame stage: ffmpeg capture, slide snap, sharpness, batched verification | 1.5 |
| RabbitMQ topology, publisher confirms, consumer, retry/DLQ, DLQ watcher | 1.5 | Notion client + publish stage (uploads, blocks, idempotent page), OAuth endpoints, `/notion/pages` | 1.5 |
| Worker runtime: claim/lease/heartbeat, stage dispatch, checkpoints, graceful shutdown | 1.5 | Extension: content script, background (OAuth, submit, poll), popup UI | 2 |
| Redis rate limiter + fault injection + admin endpoints + demo scripts | 2 | Groq/OpenRouter adapters (SHOULD) | — |

**Shared:** README, demo runbook, end-to-end testing, rehearsal. Each person reviews the other's pull requests.

### 16.2 Contracts to agree on Day 1 (the seams)
**(1) REST API** — §6 (extension ↔ gateway).

**(2) Stage interface** (worker runtime ↔ stages):
```go
package pipeline

type Stage interface {
    Name() Status                       // TRANSCRIBING, SUMMARIZING, ...
    // Run executes the stage idempotently. It may save *intermediate* checkpoints
    // (chunk results, individual frames) via deps.Store as idempotent upserts, and
    // returns the stage's final result. It never changes job status itself.
    Run(ctx context.Context, job *Job, deps Deps) (StageResult, error)
}

// StageResult is committed by the worker runtime together with the status
// transition in ONE Postgres transaction (FR-IDEM-2):
//   tx: save Artifact (+ counters) → UPDATE jobs SET status = next … WHERE status = current
type StageResult struct {
    ArtifactKind    string // "transcript" | "synthesis" | "" (frames/publish save via Store)
    Artifact        any
    FramesRequested *int
    FramesPublished *int
    PageURL         string // publish stage only
}

type Deps struct {
    Store    CheckpointStore      // Person A implements (Postgres)
    Limiter  Limiter              // Person A implements (Redis)
    Faults   Faults               // Person A implements
    Progress func(pct int)        // Person A implements (monotonic)
    Warn     func(w Warning)      // Person A implements
    Token    string               // decrypted Notion token (publish stage only)
    Log      *slog.Logger
}

type CheckpointStore interface {
    SaveArtifact(ctx context.Context, jobID uuid.UUID, kind string, v any) error
    LoadArtifact(ctx context.Context, jobID uuid.UUID, kind string, v any) (found bool, err error)
    UpsertFrame(ctx context.Context, f Frame) error
    ListFrames(ctx context.Context, jobID uuid.UUID) ([]Frame, error)
    SetVideoMeta(ctx context.Context, jobID uuid.UUID, title string, durationSec int) error
    SetNotionPage(ctx context.Context, jobID uuid.UUID, pageID, url string) error
    MarkNotionCreateStarted(ctx context.Context, jobID uuid.UUID) error
    SetFramesRequested(ctx context.Context, jobID uuid.UUID, n int) error
    SetFramesPublished(ctx context.Context, jobID uuid.UUID, n int) error
}

type Limiter interface {
    Acquire(ctx context.Context, bucket string, n int) error // returns RateLimited if wait > max
}
```
**(3) Error taxonomy** — §9.1, as Go types:
```go
type RateLimitedError struct{ Provider string; RetryAfter time.Duration }
type RetryableError   struct{ Code string; Err error }
type FatalError       struct{ Code string; Msg string }
```
**(4) Data shapes** — `Transcript`, `Synthesis` (§11.2), `Frame` (§7.1), `Warning{Code, TimestampSec}`.

Until the real implementations land, Person A builds against **fake stages** (sleep + configurable errors) and Person B tests stages with an **in-memory CheckpointStore and no-op limiter**.

---

## 17. Schedule

| Date | Person A | Person B | Milestone |
|---|---|---|---|
| **9 Nov** | Agree contracts (§16.2); repo skeleton, Compose, healthz | Agree contracts; Notion integration registered; yt-dlp/ffmpeg spikes | Contracts frozen |
| 10 Nov | Schema + repository + state machine | Transcript stage | |
| 11 Nov | Gateway API + outbox | LLM layer (Gemini + Ollama), synthesis | |
| **12 Nov** | RabbitMQ topology + worker runtime (fake stages) | Frame stage (basic capture) + publish stage (basic) | **Happy path end-to-end (API → Notion page)**; Gemini decision gate |
| 13 Nov | Retry/DLQ + leases + heartbeats | Extension: content script + background + OAuth | |
| 14 Nov | Redis limiter | Extension popup | **Happy path from the extension** |
| 15 Nov | Fault injection + admin endpoints | Slide snap + sharpness + verification | |
| 16 Nov | Demo scripts; crash-recovery tuning | Idempotent publish hardening; chunking | All failure modes demonstrable |
| 17 Nov | Integration tests | Unit tests; popup polish | |
| 18 Nov | README, setup scripts, doctor, GHCR images (together) | E2E with 20-min videos; prebuilt extension zip (together) | Feature freeze |
| 19 Nov | Clean-machine rehearsal (FR-SETUP-9), demo rehearsal, fixes | | Release tag |
| **20 Nov** | Submit | | **Submission** |

---

## 18. Risks and mitigations

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | Gemini free-tier limits too low / changed / unavailable | Med | High | Provider interface; Ollama for dev; Groq/OpenRouter/Claude Haiku backups; decision gate 12 Nov (§11.6) |
| R2 | YouTube blocks `yt-dlp` ("confirm you're not a bot") or breaks extraction | Med | High | Pin and be ready to update yt-dlp; run from a home network; optional `YTDLP_COOKIES_FILE`; YouTube rate limiter; classify as retryable |
| R3 | Stream URL expiry / slow seeks make frame stage slow | Low | Med | Resolve URL per stage run; 720p cap; input seeking; bounded concurrency; per-frame timeout |
| R4 | Graders get stuck in setup on their own machines (Notion OAuth registration, keys, Docker builds) | **High** | **High** | §14.4: internal-token Mode A, setup script, deep preflight check, prebuilt images and extension, clean-machine rehearsal |
| R10 | Grader's network or fresh free-tier key behaves differently from ours (YouTube bot check, lower quotas) | Med | High | Conservative free-tier defaults; doctor detects bot-check; cookies option documented; provider switch documented |
| R5 | Duplicate Notion pages under crash timing edge cases | Low | High | §9.3 three-layer idempotency + integration test |
| R6 | LLM returns poor timestamps (frame shows wrong thing) | Med | Med | Prompt cues, slide snap, batched verification with revised captions |
| R7 | Schedule slip (learning while building) | High | High | Contracts on day 1 so nobody blocks; happy path by 12 Nov; SHOULD/COULD items are droppable; 19 Nov buffer |
| R8 | Laptop resources (Docker + Chrome + ffmpeg) | Low | Med | Worker concurrency 1; 720p frames; Ollama only in `dev` profile |
| R9 | Free-tier provider uses inputs for training | — | Low | Inputs are public YouTube transcripts; noted in README |

---

## 19. Design decisions log
(Source spec §10 — "be ready to justify your choices".)

| Decision | Choice | Why | Alternatives considered |
|---|---|---|---|
| Language | Go | Concurrency primitives, small static binaries, in demand for backend roles | Python (slower, weaker typing), Node |
| Broker | RabbitMQ (quorum work queue) | Native acks, redelivery on connection loss, DLX, TTL-based delayed retry, delivery limits for poison messages, management UI for the demo | Redis Streams (manual pending-entry reclaim, no native DLQ), NATS JetStream (good, less common in job listings) |
| Job state | Postgres with checkpoints + events | Transactions make "save result + advance status" atomic; durable checkpoints enable resume | Redis (weaker durability), state in messages (lost on DLQ, hard to query) |
| Enqueue | Transactional outbox | No lost jobs if broker is down or gateway crashes after commit | Publish-then-commit (lost/phantom jobs) |
| Global LLM limit | Redis token buckets via Lua, shared by all workers, ~80 % of provider limit | Atomic, cross-process, cheap; prevents 429s instead of reacting to them | Per-worker limits (sum exceeds provider limit when scaled), broker-level throttling (too coarse) |
| Retry | Per-delay TTL queues + DLX | Exponential backoff without a scheduler, no head-of-line blocking | Delayed-message plugin (extra plugin), sleeping in the worker (wastes worker, lost on crash) |
| Crash safety | Ack after durable commit; leases + Redis heartbeats; idempotent stages | At-least-once delivery is only safe if redelivery is harmless | Ack-on-receive (loses jobs on crash) |
| Topology | One worker type, all stages | Simplest scaling story; checkpoints give per-stage resume anyway | Per-stage workers (future scope: independent scaling of CPU-heavy frames) |
| Transcript | yt-dlp captions (json3) | Free, timestamped, gives duration in the same step | YouTube Data API (captions need OAuth as owner), Whisper (slow; future) |
| Frames | ffmpeg seek on stream URL + scene snap + sharpness | Real frames only (Constraint B), no full download | Downloading the whole video (slow, disk heavy), thumbnails (not real frames at the timestamp) |
| LLM | Gemini Flash free tier behind an interface | $0, supports images for verification, tight limits showcase the limiter | Paid models (cost), local only (no real 429s, slow) |
| Token handling | Passed per job, AES-GCM encrypted in broker, never stored | Matches spec ("gateway never has to store it") while keeping messages safe at rest | Plaintext in message, storing tokens server-side |

---

## 20. Future scope (post-submission)
Ordered roughly by resume value. None of this is required for the 20 Nov submission.

**Engineering showcase**
1. **Per-stage worker pools** (transcript / LLM / frames / publish queues) with independent autoscaling.
2. **OpenTelemetry tracing** across gateway → broker → worker → providers (Jaeger).
3. **Prometheus metrics + Grafana dashboard** (queue depth, stage latency, 429s avoided, DLQ size).
4. **Server-Sent Events** for live status instead of polling.
5. **Kubernetes + Helm**, with KEDA autoscaling workers on queue depth.
6. **Chaos test suite** (Toxiproxy for network faults, k6 for load) in CI.
7. **Object storage (MinIO/S3)** for frames instead of Postgres `BYTEA`.

**Product**
8. Whisper speech-to-text fallback for videos without captions.
9. Videos > 20 minutes via hierarchical (map-reduce) summarization; playlists.
10. Q&A / chat over your notes (embeddings with pgvector).
11. Flashcards and Anki export; quizzes.
12. Other destinations: Obsidian/Markdown, Google Docs.
13. Web dashboard with user accounts and job history.
14. True video understanding (multimodal model watching the video) to catch slides that are never mentioned aloud.

---

## Appendix A — Requirements traceability matrix
Every requirement in the source specification, mapped to this SRS.

| # | Source spec requirement | SRS reference |
|---|---|---|
| 1 | §1 Turn a YouTube video into notes: summarized text + real frames of diagrams/slides, published to Notion | §2.1, §5.3, §5.4 |
| 2 | §1 Driven entirely from a browser extension; user never calls the API by hand | FR-EXT-1…27 |
| 3 | §1 Queue-backed worker pool | §3.1, §8 |
| 4 | §1 Respect rate limits of every external API | §10 (LLM, Notion, YouTube buckets) |
| 5 | §1.1 Four independently running pieces, each separate process/container; kill/scale one without touching others | §3.1, NFR-REL-1, FR-DEP-1/2 |
| 6 | §1.1 Gateway accepts jobs, exposes status/results, does not do pipeline work | FR-GW-1/2 |
| 7 | §1.1 Broker: durable queue, at-least-once delivery, redelivery on crash | §8.1, FR-MQ-1…5 |
| 8 | §1.1 Worker pool ≥ 2 independent processes | FR-DEP-1 (`replicas: 2`) |
| 9 | §2 Content script in YouTube tab injects "Take Notes" into the player toolbar on watch pages | FR-EXT-1 |
| 10 | §2 On click captures current video ID, sends to background | FR-EXT-2 |
| 11 | §2 Must keep working across video-to-video navigation | FR-EXT-3 |
| 12 | §2 Background: one-time Notion OAuth, stores token, not re-prompted | FR-EXT-10/11, FR-NOT-1/2 |
| 13 | §2 Background submits jobs, relays status to popup | FR-EXT-12…14 |
| 14 | §2 Popup shows Queued → Transcribing → Summarizing → Extracting Frames → Publishing → Done | FR-EXT-20 |
| 15 | §2 One-click "View in Notion" on success | FR-EXT-22 |
| 16 | §2 Failures and "completed with warnings" shown clearly; never raw JSON | FR-EXT-21/23/26, NFR-UX-1 |
| 17 | §3.1 Fetch timestamped transcript; learn true duration; enforce Constraint A | FR-PIPE-1…6 |
| 18 | §3.2 LLM produces summary bullets + timestamps of important visuals | FR-PIPE-10, §11.2 |
| 19 | §3.2 Each flagged timestamp gets a caption: what is shown and why flagged | FR-PIPE-10, §11.2 (`caption` + `reason`) |
| 20 | §3.2 Chunk long transcripts, merge keeping section order | FR-PIPE-11, §11.4 |
| 21 | §3.3 Seek to each timestamp, capture one frame directly from the video stream | FR-PIPE-20/21 |
| 22 | §3.4 Upload frames; assemble page (headings, bullets, images with captions) via Notion API | FR-PIPE-30…34, §5.4.2 |
| 23 | §4 Status set QUEUED … DEAD_LETTERED with stated meanings | §4.1 |
| 24 | §4 Transition table | §4.2, FR-SM-1…6 |
| 25 | §5 `POST /jobs` request/response shape, 202 + `Location` | §6.2 |
| 26 | §5 Validates URL, returns immediately, no pipeline work in request | IR-API-1…4, FR-GW-2 |
| 27 | §5 Invalid input → 4xx with `{"error": {"code","message"}}` | IR-API-5, §6.6 |
| 28 | §5 `notion_token` supplied per submission; gateway never stores it | FR-GW-4, NFR-SEC-1 |
| 29 | §5 Omitted `notion_parent_id` → default destination, stated in README | §5.4.3, §14.3 item 4 |
| 30 | §5 `GET /jobs/{id}` shape (status, progress, title, duration, warnings, error, result) | §6.3, IR-API-10 |
| 31 | §5 Polling read-only and idempotent; unknown id → 404 | IR-API-11/12, FR-GW-5 |
| 32 | §5 `GET /jobs/{id}/results` shape (url, frames_requested, frames_published, warnings) | §6.4 |
| 33 | §5 Results before terminal → 409, not long-poll | IR-API-20 |
| 34 | §5 Success returns working absolute Notion URL | IR-API-21 |
| 35 | §6 429s from any of the three external APIs re-queued with exponential backoff; user sees waiting | §9.2, FR-RET-1…4, FR-EXT-21 |
| 36 | §6 Worker crash mid-job → broker reassigns unacked message; no job lost or duplicated on Notion | §9.4, FR-MQ-3/4, §9.3 |
| 37 | §6 Ack only after stage work durably complete; stage redelivery idempotent | FR-MQ-3, FR-IDEM-1…5 |
| 38 | §6 Partial frame failure → publish text + successful frames, COMPLETED_WITH_WARNINGS | FR-DEG-1, FR-PIPE-25 |
| 39 | §6 All frames fail → text-only page | FR-DEG-2 |
| 40 | §6 DLQ after bounded, configurable attempts; demonstrable in reasonable time | FR-RET-1/2/5/7, FR-ADM-2 |
| 41 | §7A Support videos up to 20 min; reject longer deterministically, don't process | FR-PIPE-3, FR-EXT-4 |
| 42 | §7B Every image is a real frame at the identified timestamp; never AI-generated/substituted; omitted on failure | FR-PIPE-21/25/26 |
| 43 | §7B Every frame paired with an LLM-written caption (what + why) | FR-PIPE-26, FR-NOT-10 |
| 44 | §8 Unpacked extension, no build step or single documented build command | FR-DEP-10 |
| 45 | §8 Gateway, broker, ≥ 2 workers via one docker-compose; `--scale worker=N`; stop/start individual containers | FR-DEP-1…4 |
| 46 | §8 README: base URL, default Notion destination, every env var (LLM key + model, rate limit, Notion OAuth creds, broker URL, retry/backoff) | §14.3, §13 |
| 47 | §8 Grader can run the whole stack from README alone | §14.3, §14.4 (FR-SETUP-1…9), acceptance #4 |
| 48 | §8 Health-check endpoint confirming gateway, broker, state store reachable | FR-ADM-1, §6.7 |
| 49 | §9 Client experience | §5.1, NFR-UX-* |
| 50 | §9 End-to-end latency near 20-min cap | NFR-PERF-2 |
| 51 | §9 Burst of jobs drains without LLM 429s; each click an independent job | FR-RL-4, IR-API-6/7, NFR-PERF-3 |
| 52 | §9 Fault tolerance: recovery time and correctness after worker kill | FR-CRASH-5 |
| 53 | §9 Constraints A and B correct on held-out + submitted videos (LLM-as-judge) | FR-PIPE-3/24/26, §15.3 |
| 54 | §10 Justify: broker, state model, LLM rate-limit coordination, worker topology, provider/transcript/frame methods, extension tooling | §19 |

---

## Appendix B — Glossary
| Term | Meaning |
|---|---|
| **At-least-once delivery** | The broker may deliver a message more than once but never zero times; consumers must be idempotent. |
| **Ack / nack** | Consumer tells the broker a message is done (delete it) / not done (requeue or dead-letter it). |
| **Checkpoint** | A stage's saved output in Postgres, letting a later run skip completed work. |
| **DLQ / DLX** | Dead-letter queue / exchange — where messages go after exhausting retries. |
| **Idempotent** | Running an operation twice has the same effect as running it once. |
| **Lease** | A time-limited claim a worker holds on a job so two workers don't process it concurrently. |
| **Outbox** | A table written in the same transaction as the job; a relay publishes its rows to the broker. |
| **Quorum queue** | A RabbitMQ queue type with stronger durability and a per-message delivery limit. |
| **Token bucket** | Rate-limiting algorithm: tokens refill at a fixed rate; each request spends tokens. |
| **Constraint A / B** | Source spec §7: 20-minute limit / real frames with captions only. |

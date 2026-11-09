# Contributing — team guide

Two-person final-year project, due **20 November 2026**. Read this first, then [`docs/SRS.md`](docs/SRS.md) (what we're building) and [`docs/tasks/README.md`](docs/tasks/README.md) (who builds what).

| | Person A — Platform & Reliability | Person B — Pipeline & Client |
|---|---|---|
| Who | Aryan ([@Oct8Hacker](https://github.com/Oct8Hacker)) | Om ([@OmAghera](https://github.com/OmAghera)) |
| Issues | [`track-A`](https://github.com/Oct8Hacker/ai-note/issues?q=is%3Aissue+label%3Atrack-A) | [`track-B`](https://github.com/Oct8Hacker/ai-note/issues?q=is%3Aissue+label%3Atrack-B) |
| Area | gateway, Postgres, RabbitMQ, worker runtime, rate limiter, fault injection | transcript, LLM, frames, Notion publishing, Chrome extension |

Shared work (integration, demo, README) is labelled [`integration`](https://github.com/Oct8Hacker/ai-note/issues?q=is%3Aissue+label%3Aintegration). Everything is in the [Submission milestone](https://github.com/Oct8Hacker/ai-note/milestone/1).

## What already exists (on `build/v1`)

- **`internal/pipeline/`** — the shared contracts both halves build against: `Status`, `Job`, `Transcript`, `Synthesis`, `Frame`, `Warning`, the error types (`RateLimitedError`, `RetryableError`, `FatalError`), the `Stage` interface, `Deps`, `CheckpointStore`, `Limiter`, `Faults`. **These are frozen** — changing them needs both people's approval in the PR.
- **`internal/pipeline/pipelinetest/`** — test fakes for stage tests: `NewMemStore()` (records warnings and progress), `NoopLimiter`, `NoFaults`, `FakeStage`, `TestDeps(store)`. Track B stage tests use these, so you never need Postgres, RabbitMQ or Redis to test a stage.
- **Track A, tasks A1–A5** — config, Docker Compose stack, Postgres schema + job repository, state machine, gateway job API.

## Person B: where to start

Work the issues roughly in this order (each task file lists exactly what to build and which tests prove it):

1. **B1 → B2** yt-dlp wrapper, transcript stage
2. **B3 → B4** LLM providers, synthesis stage
3. **B5 → B6** ffmpeg frame capture, frame stage
4. **B7 → B8 → B9** Notion client, page builder, publish stage
5. **B12 → B13 → B14 → B15** Chrome extension (TypeScript — independent of the Go work, can run in parallel)
6. **B10, B11, B16** gateway Notion endpoints, doctor, Groq/OpenRouter backups (B10/B11 touch Track A code — coordinate in the PR)

Your stages implement `pipeline.Stage`: `Run(ctx, job, deps) (StageResult, error)`. Return the right error type — the worker runtime (Track A) decides retry / dead-letter / fail from it.

## Setup

Needs: **Go ≥ 1.23**, **Docker Desktop**, **Node ≥ 20**, **ffmpeg**, **yt-dlp** (Windows: `winget install GoLang.Go Gyan.FFmpeg yt-dlp.yt-dlp`).

```bash
git clone https://github.com/Oct8Hacker/ai-note.git
cd ai-note
git switch build/v1
go test ./...                        # unit tests
go test -tags=integration ./...      # integration tests (Docker must be running)
./scripts/lint.sh                    # golangci-lint exactly as CI runs it (via Docker)
cp .env.example .env                 # then fill in values; never commit .env
docker compose -f deploy/docker-compose.yml --env-file .env up -d --build
curl localhost:8080/healthz
```

API keys (Gemini, Notion) go **only** in your local `.env`. Never commit them or paste them in issues, PRs or chats.

## Learning first, then code (both of us)

We use AI to write code (Aryan: Claude Code, Om: Codex), but **we learn before we build**. For every task:

1. **Learn the prerequisites** — ask your AI to teach every concept/tool the task needs (it will do this automatically: see [`AGENTS.md`](AGENTS.md)). No code until you understand it.
2. **Say go** — the AI then implements the task **one step at a time** (tests first, then code), explaining each step.
3. **Walkthrough** — the AI walks you through the finished code so you can explain it in the viva.
4. Only then move to the next task.

## Workflow for every task

1. Assign yourself the issue.
2. Branch from `build/v1`: `git switch -c task/b1`.
3. Write the tests from the task file first; run them and see them fail.
4. Implement until they pass. `go vet ./...` and `go test ./...` must be clean.
5. Commit with a conventional message: `feat(transcript): …`, `fix(api): …`, `test(…)`, `docs(…)`.
6. Open a PR **into `build/v1`** with `Closes #<issue>` in the description. The other person reviews before merge.
7. Mark the task ✅ in `docs/tasks/README.md` in the same PR.

Rules of thumb:
- Stay inside your task's files. If you need to change a shared contract or the other track's code, say so in the PR.
- Keep go.mod at `go 1.23`; if `go get` bumps it, pin an older version of that dependency.
- Videos longer than 20 minutes are rejected (`MAX_VIDEO_SECONDS=1200`); every image on Notion must be a real ffmpeg frame with an LLM-written caption — see SRS §7.

## Using AI assistants

Our course allows full LLM use. Keep the `Co-Authored-By` trailer on AI-assisted commits, and make sure you can explain every line you merge — the viva will ask.

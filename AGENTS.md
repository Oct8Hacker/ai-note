# AGENTS.md — instructions for AI coding agents (Codex, Claude Code, …)

This is a two-person final-year project, due **20 Nov 2026**. The humans must be able to explain every line in their viva, so **learning comes before code**. These rules apply to every AI agent working in this repo.

| | Person A — Platform & Reliability | Person B — Pipeline & Client |
|---|---|---|
| Who | Aryan ([@Oct8Hacker](https://github.com/Oct8Hacker)), uses Claude Code | Om ([@OmAghera](https://github.com/OmAghera)), uses Codex |
| Tasks | A1–A12: **all done and merged** | B1–B16: **all done and merged** (Om, PRs #29–#37, #39–#45) |
| Packages | `internal/{config,api,jobs,queue,outbox,worker,dlqwatch,ratelimit,faults,health,crypto}`, `cmd/`, `deploy/`, `migrations/` | `internal/youtube`, `internal/llm`, `internal/notion`, `internal/pipeline/{transcript,synthesis,frames,publish}`, `internal/doctor`, `extension/` |

Z1–Z4 (integration, demo, release) are joint and in progress: code and docs are on `build/v1`; the live runs and the clean-machine rehearsal remain (see `docs/acceptance.md`). Bug fixes found during Z go in the owning package with a regression test.

**Read first:** `docs/SRS.md` (what we build; requirement IDs FR-…, IR-…), `docs/tasks/README.md` (task list and GitHub issue numbers), then the task file `docs/tasks/<ID>.md`.

## Workflow for every task — in order, no skipping

1. **Identify the task.** The human names a task ID (e.g. `B1`). Its spec is `docs/tasks/<ID>.md`. Its GitHub issue number is in `docs/tasks/README.md` (B1–B16 = #8–#23).
2. **Teach the prerequisites first — write NO code yet.** Read the task file and the "Notes from Track A" section in it if there is one. Then teach the human every concept, tool and library the task uses:
   - what it is, in plain words;
   - why this task needs it, tied to this project;
   - a tiny runnable example where it helps;
   - the shape of what the task will build (files, types, functions, tests).

   Then **stop and wait for the human to say "go"**. Answer their questions first. Do not create or edit any file before the go.
3. **Implement step by step with TDD.** For each numbered step in the task file:
   - write the tests the task file lists, run them, show they fail (RED);
   - write the minimal code to make them pass, run them, show they pass (GREEN);
   - briefly explain what you wrote and why.

   Never build anything the task file doesn't ask for.
4. **Verify before committing.** Every command must pass:
   ```bash
   go vet ./...
   go test ./...
   go test -tags=integration -p 1 ./...   # Docker must be running
   bash scripts/lint.sh                   # CI's exact linter (golangci-lint v2.1.6 via Docker) — 0 issues
   ```
   For extension tasks (B12–B15), also run `npm --prefix extension test` and `npm --prefix extension run build`.
5. **Walk the human through the finished code**, file by file with line references, so they can explain it. Only then move to the next task.

## Hard rules

- **Frozen contracts.** Do not change `internal/pipeline/` (its types, error types, `Stage`, `Deps`, `CheckpointStore`, `Limiter`, `Faults`, bucket constants) or `internal/pipeline/pipelinetest/`. If a change seems necessary, stop and tell the human; both people must approve it in the PR.
- **Stay in your track.** Track B code goes in the Track B packages above. Only B10 and B11 touch Track A files, and only the files their task lists. See "Shared files" below.
- **Stage tests use fakes.** Use `internal/pipeline/pipelinetest` (`NewMemStore()`, `TestDeps(store)`, `NoopLimiter`, `NoFaults`, `FakeStage`). Stage tests must never need Postgres, RabbitMQ or Redis.
- **Errors are the contract with the worker.** Return `*pipeline.RateLimitedError` for 429s, `*pipeline.RetryableError` for transient failures, and `*pipeline.FatalError` for permanent ones. The codes are listed in SRS §6.6. The worker decides what happens next from the type alone:
  - `RateLimitedError`: retried without spending the failure budget (`MAX_RATE_LIMIT_RETRIES`).
  - `RetryableError`: retried up to `MAX_ATTEMPTS`, then dead-lettered.
  - `FatalError`: the job fails immediately.
  - Any other error is treated as retryable.
- **Constraint A.** Videos longer than `MAX_VIDEO_SECONDS` (1200) are rejected before any processing.
- **Constraint B.** Every image on Notion must be a real frame captured by ffmpeg from the source video, paired with an LLM-written caption. Never generate, fetch or substitute images.
- **Subprocesses** (`yt-dlp`, `ffmpeg`): use `exec.CommandContext` with an argument slice, never a shell string.
- **Secrets.** API keys and Notion tokens live only in the local `.env`, which is git-ignored. Never commit, log, print or paste them.
- **`go.mod` stays at `go 1.23`.** If `go get` bumps it, pin an older version of that dependency (`go get pkg@vX.Y.Z`) and check `go.mod` again.
- **`internal/config/`: only ADD fields.** Never rename or change existing ones.

## What Track A already gives you (use it, don't rebuild it)

| You need | Use | Where |
|---|---|---|
| Rate limiting | `deps.Limiter.Acquire(ctx, bucket, n)` with the constants `pipeline.BucketLLMRequests`, `BucketLLMTokens`, `BucketLLMDaily`, `BucketLLMFallbackRequests`, `BucketYouTube`, and `pipeline.NotionBucket(token)` | `internal/ratelimit` (Redis token buckets, wired in `cmd/worker/main.go`) |
| Counting provider 429s | `ratelimit.Incr429(ctx, rdb)`. Pass it to `llm.FromConfig` as the `on429 func(ctx)` hook; don't import `ratelimit` from `internal/llm`. | `internal/ratelimit/metrics.go` |
| Fault injection | `deps.Faults.Check(ctx, pipeline.FaultLLM / FaultNotion / FaultYouTube / FaultFrame)`, `deps.Faults.FrameDelay(ctx)` | `internal/faults` |
| Saving progress so retries resume | `deps.Store` (`SaveArtifact`, `LoadArtifact`, `UpsertFrame`, `ListFrames`, `SetNotion*`, …) | `internal/jobs` (Postgres) |
| Progress bar and warnings | `deps.Progress(pct)`, `deps.Warn(pipeline.Warning{…})` | `internal/worker/deps.go` |
| The user's Notion token | `deps.NotionToken`: decrypted, and **non-empty only in the PUBLISHING stage** | `internal/worker/runtime.go` |
| Health and doctor output | `health.Checker.Doctor` field (currently nil; B11 sets it) | `internal/health/checker.go` |

Since Z1 the worker runs the **real stages**, built by `realStages()` in `cmd/worker/stages.go`. Stage unit tests still use fakes; `scripts/demo/happy.sh <url>` runs one real video end to end.

## Shared files (Track A owns them — touch only when your task says so)

- `cmd/gateway/main.go`, `cmd/worker/main.go`, `internal/api/server.go`, `internal/config/`.
- B10 adds a `Notion NotionGateway` field to `api.ServerDeps` and registers its routes only when it is non-nil. That is the same pattern `Admin` and `Faults` already use in `internal/api/server.go`.
- B11 edits both `main.go` files.
- For both, `git pull --rebase origin build/v1` right before starting, and say in the PR that Track A files changed.

## Codex notes

- **Use the Codex CLI or IDE extension on your own machine, not Codex Cloud, for anything that runs tests.** The integration tests and `scripts/lint.sh` need Docker. A cloud sandbox can't run them, and CI will fail what you couldn't check.
- If you do use a cloud task, run `go vet ./...` and `go test ./...` there. Before merging, run the integration tests and lint locally.
- The sandbox needs network access for `go mod download` and `npm ci` the first time.
- Tools on the machine: **Go ≥ 1.23**, **Docker Desktop**, **Node ≥ 20**, **ffmpeg**, **yt-dlp**. On Windows: `winget install GoLang.Go Gyan.FFmpeg yt-dlp.yt-dlp`.

## Setup

```bash
git clone https://github.com/Oct8Hacker/ai-note.git
cd ai-note
git switch build/v1
go test ./...                        # unit tests
go test -tags=integration -p 1 ./... # integration tests (Docker must be running)
bash scripts/lint.sh                 # lint exactly as CI runs it
scripts/setup.sh                     # creates .env and generates its secrets; never commit .env
docker compose -f deploy/docker-compose.yml --env-file .env up -d --build
curl localhost:8080/healthz
```

## Git

- **Order:** B1 → B2 → B3 → B4 → B5 → B6 → B7 → B8 → B9. The extension tasks (B12 → B13 → B14 → B15) are independent and can come any time. B16 comes after B3, B10 after B7, and B11 after B1 and B3.
- **One branch per task:** `task/bN`.
  - Start it from the latest `build/v1`.
  - If the task needs an earlier B task whose PR isn't merged yet, start from that task's branch instead. After the earlier PR merges, run `git rebase origin/build/v1`.
- **Commits are Om's own** (his normal git identity). Use conventional messages: `feat(transcript): …`, `fix(…)`, `test(…)`, `docs(…)`. End each AI-written commit with `Co-Authored-By: Codex <noreply@openai.com>`.
- **PRs:** open a PR into `build/v1` with `Closes #<issue>` in the description. CI must be green, and Aryan reviews before merge. Merge with a **merge commit or rebase**; squash is disabled.
- **`go.sum` conflicts:** after rebasing, run `go mod tidy` and commit. Never hand-edit `go.sum`.
- **Task status:** don't edit the status column in `docs/tasks/README.md`. The merged PR closes the issue, and Aryan updates the table.
- Stage files by explicit path (`git add path/to/file`), never `git add -A` or `git add .`.
- Never force-push to `build/v1`, and never push to `main`.

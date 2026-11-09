# AGENTS.md — instructions for AI coding agents (Codex, Claude Code, etc.)

This repo is a two-person final-year project (due **20 Nov 2026**). The humans must be able to explain every line in their viva, so **learning comes before code**. These rules apply to every AI agent working in this repo.

## The workflow for every task — follow it in order, no skipping

**1. Identify the task.** The human names a task ID (e.g. `B1`). Its full spec is `docs/tasks/<ID>.md`; requirement IDs (FR-…, IR-…) refer to `docs/SRS.md`. The GitHub issue for it is listed in `docs/tasks/README.md`.

**2. Teach the prerequisites first — write NO code yet.**
Read the task file, then give the human a lesson on every concept, tool and library the task depends on:
- what each one is, in plain words;
- why this task needs it, tied to this project;
- a tiny runnable example or command where it helps;
- the exact shape of what the task will build (files, types, functions, tests).

Then **stop and wait for the human to say go.** Answer their questions first. Do not create, edit or scaffold any file before the go.

**3. Implement step by step — one step at a time.**
Each task file has numbered steps. For each step:
- do only that step;
- tests first: write the tests listed in the task file, run them, show they fail (RED);
- then the minimal code to make them pass, run them, show they pass (GREEN);
- briefly explain what you wrote and why, then move to the next step.
Never jump ahead, and never build anything the task file doesn't ask for.

**4. Verify before committing.**
```bash
go vet ./...
go test ./...
go test -tags=integration ./...   # when the task has integration tests (Docker must be running)
bash scripts/lint.sh              # CI's exact linter (golangci-lint v2.1.6 via Docker) — must report 0 issues
```
For extension tasks: `npm --prefix extension test` and `npm --prefix extension run build`.

**5. Walk the human through the finished code** (file by file, with line references) so they can explain it. Only then move to the next task.

## Hard rules

- **Frozen contracts:** `internal/pipeline/` (types, error types, `Stage`, `Deps`, `CheckpointStore`, `Limiter`, `Faults`) is shared by both tracks. Do not change it — if a change seems necessary, stop and tell the human; it needs both people's approval in the PR.
- **Stay in your lane:** Person B (Om) owns Track B (`internal/youtube`, `internal/llm`, `internal/notion`, `internal/pipeline/{transcript,synthesis,frames,publish}`, `internal/doctor`, `extension/`). Person A (Aryan) owns Track A. Don't edit the other track's files unless the task says so (B10/B11 touch the gateway — mention it in the PR).
- **Stage tests use fakes:** use `internal/pipeline/pipelinetest` (`NewMemStore()`, `TestDeps()`, `NoopLimiter`, `NoFaults`) — stage tests never need Postgres, RabbitMQ or Redis.
- **Errors are the contract with the worker:** return `*pipeline.RateLimitedError` for 429s, `*pipeline.RetryableError` for transient failures, `*pipeline.FatalError` for permanent ones (codes are listed in SRS §6.6).
- **Constraint A:** videos longer than `MAX_VIDEO_SECONDS` (1200) are rejected before any processing.
- **Constraint B:** every image on Notion must be a real frame captured by ffmpeg from the source video, paired with an LLM-written caption. Never generate, fetch or substitute images.
- **Subprocesses** (`yt-dlp`, `ffmpeg`): `exec.CommandContext` with an argument slice — never a shell string.
- **Secrets:** API keys and Notion tokens live only in the local `.env` (git-ignored). Never commit, log, print or paste them.
- **go.mod stays at `go 1.23`.** If `go get` bumps it, pin an older version of that dependency.

## Working in parallel (two people, two AIs)

Aryan (Claude Code) and Om (Codex) work at the same time on different tracks. To avoid conflicts:

- **Rebase often:** `git pull --rebase origin build/v1` before starting a task and again before opening its PR. Keep PRs small and merge them quickly.
- **`go.mod` / `go.sum`:** after rebasing, run `go mod tidy` and commit. Resolve any `go.sum` conflict by re-running `go mod tidy` — never hand-edit it.
- **Do not edit the status column in `docs/tasks/README.md`.** Task status is tracked by the GitHub issues (a merged PR with `Closes #N` closes the issue) and the milestone.
- **Shared wiring files** (`cmd/gateway/main.go`, `cmd/worker/main.go`, `internal/api/server.go`, `internal/config/`): only touch them when your task says so. B10 starts after A10/A11 are merged; B11 after A9/A11. In `internal/config/` only ADD fields — never rename or change existing ones.
- **`internal/pipeline/` is frozen** (see Hard rules).

## Git

- Branch per task from `build/v1`: `task/<id>` (e.g. `task/b1`).
- Conventional commits: `feat(transcript): …`, `fix(…)`, `test(…)`, `docs(…)`.
- Keep an AI attribution trailer on AI-written commits (e.g. `Co-Authored-By: Codex <noreply@openai.com>`).
- Open a PR into `build/v1` with `Closes #<issue>`; CI must be green; the other person reviews before merge.
- Never force-push, never push to `main`.

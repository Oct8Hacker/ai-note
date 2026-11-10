# CLAUDE.md — instructions for Claude Code in this repo

This is a final-year project (due **20 Nov 2026**). Aryan (GitHub `Oct8Hacker`) and Om (`OmAghera`) must be able to explain every line in their viva. Claude Code writes the code for both tracks. The spec is `docs/SRS.md`, the plan is `docs/superpowers/plans/2026-11-09-video-notes-agent.md`, and the per-task files are in `docs/tasks/`.

## Workflow for every task

1. **Teach the prerequisites first**, unless Aryan has waived this for the task. Cover each concept, tool and library the task uses: what it is, why this task needs it, and a tiny example. Then wait for "go".
2. **Implement step by step with TDD.** Write the tests from the task file, run them and watch them fail (RED). Then write the minimal code to make them pass (GREEN). Never build anything the task doesn't ask for.
3. **Verify before committing:**
   ```bash
   go vet ./...
   go test ./...
   go test -tags=integration ./...   # Docker must be running
   bash scripts/lint.sh              # CI's exact linter (golangci-lint v2.1.6 via Docker) — 0 issues
   ```
   For extension tasks, also run `npm --prefix extension test` and `npm --prefix extension run build`.
4. **Walk through the finished code** (file by file, with line references) when Aryan asks.

## Hard rules

- **Frozen contracts.** Do not change `internal/pipeline/` (its types, error types, `Stage`, `Deps`, `CheckpointStore`, `Limiter` and `Faults`). If a change seems necessary, stop and ask.
- **Stage tests use fakes.** Use `internal/pipeline/pipelinetest` (`NewMemStore()`, `TestDeps()`, `NoopLimiter`, `NoFaults`). Stage tests must never need Postgres, RabbitMQ or Redis.
- **Errors are the contract with the worker.** Return `*pipeline.RateLimitedError` for 429s, `*pipeline.RetryableError` for transient failures, and `*pipeline.FatalError` for permanent ones. The codes are listed in SRS §6.6.
- **Constraint A.** Videos longer than `MAX_VIDEO_SECONDS` (1200) are rejected before any processing.
- **Constraint B.** Every image on Notion must be a real frame captured by ffmpeg from the source video, paired with an LLM-written caption. Never generate, fetch or substitute images.
- **Subprocesses** (`yt-dlp`, `ffmpeg`): use `exec.CommandContext` with an argument slice, never a shell string.
- **Secrets.** API keys and Notion tokens live only in the local `.env`, which is git-ignored. Never commit, log, print or paste them.
- **`go.mod` stays at `go 1.23`.** If `go get` bumps it, pin an older version of that dependency.
- **`internal/config/`: only ADD fields.** Never rename or change existing ones.
- **Never commit `docs/LEARNING-RESOURCES.md`** (it is kept local on purpose). Stage files by explicit path, never with `git add -A`.

## Git

- **Track A and Z tasks** are committed straight to `build/v1` and pushed after review. Close the task's issue and check CI.
- **Track B tasks (B1–B16)**:
  - Each task gets its own branch, `task/bN`, stacked in order on the previous B branch.
  - Commit with `--author="Om Aghera <184139381+OmAghera@users.noreply.github.com>"`. Aryan stays the committer.
  - Open a PR into `build/v1` with `Closes #<issue>`.
  - Om reviews and merges each PR with a merge commit or a rebase. Squash merging is disabled.
- Use conventional commits: `feat(…)`, `fix(…)`, `test(…)`, `docs(…)`. End every commit with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Never force-push, never push to `main`, and never merge a PR without Aryan's say-so.

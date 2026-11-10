# Task index

One file per task, extracted from the [implementation plan](../superpowers/plans/2026-11-09-video-notes-agent.md). Requirement IDs (FR-…, IR-…) refer to [docs/SRS.md](../SRS.md).

Every open task is a GitHub issue (A6–A12 = #1–#7, B1–B16 = #8–#23, Z1–Z4 = #24–#27) — see [CONTRIBUTING.md](../../CONTRIBUTING.md). Status is updated as each task passes review. **Owner:** A = Platform & Reliability (**Aryan**), B = Pipeline & Client (teammate), A+B = both.

| Task | Title | Owner | Target | Status |
|---|---|---|---|---|
| [0.1](0.1.md) | Repository bootstrap | A+B | 9 Nov | ✅ done |
| [0.2](0.2.md) | Pipeline contracts and test helpers | A+B | 9 Nov | ✅ done |
| [A1](A1.md) | Configuration | A | 9 Nov | ✅ done |
| [A2](A2.md) | Compose stack, Dockerfiles, `/healthz` | A | 9 Nov | ✅ done |
| [A3](A3.md) | Schema, migrations, job repository core | A | 10 Nov | ✅ done |
| [A4](A4.md) | State machine, stage commits, retries, checkpoint store | A | 10 Nov | ✅ done |
| [A5](A5.md) | Gateway job API | A | 11 Nov | ✅ done |
| [A6](A6.md) | RabbitMQ topology, policy, publisher, consumer | A | 12 Nov | ✅ done |
| [A7](A7.md) | Outbox relay | A | 12 Nov | ✅ done |
| [A8](A8.md) | Worker liveness and leases | A | 12 Nov | ✅ done |
| [A9](A9.md) | Worker runtime | A | 12–13 Nov | ✅ done |
| [A10](A10.md) | DLQ watcher and admin endpoints | A | 13 Nov | ✅ done |
| [A11](A11.md) | Redis rate limiter | A | 14 Nov | ✅ done |
| [A12](A12.md) | Fault injection | A | 15 Nov | 🔨 in progress |
| [B1](B1.md) | YouTube tool wrapper | B | 10 Nov | ⏳ pending |
| [B2](B2.md) | Transcript stage | B | 10 Nov | ⏳ pending |
| [B3](B3.md) | LLM providers and guarded client | B | 11 Nov | ⏳ pending |
| [B4](B4.md) | Synthesis stage | B | 11 Nov | ⏳ pending |
| [B5](B5.md) | Frame capture primitives | B | 12 Nov | ⏳ pending |
| [B6](B6.md) | Frame stage with verification | B | 15 Nov (basic capture path by 12 Nov) | ⏳ pending |
| [B7](B7.md) | Notion API client | B | 12 Nov | ⏳ pending |
| [B8](B8.md) | Notion page content builder | B | 13 Nov | ⏳ pending |
| [B9](B9.md) | Publish stage (parent resolution, idempotent page) | B | 13 Nov (basic by 12 Nov) | ⏳ pending |
| [B10](B10.md) | Gateway Notion endpoints | B | 13 Nov | ⏳ pending |
| [B11](B11.md) | Preflight doctor | B | 16 Nov | ⏳ pending |
| [B12](B12.md) | Extension scaffold, shared client, message mapping | B | 13 Nov | ⏳ pending |
| [B13](B13.md) | Content script — Take Notes button | B | Take Notes button [B] — 13 Nov | ⏳ pending |
| [B14](B14.md) | Background service worker | B | 13–14 Nov | ⏳ pending |
| [B15](B15.md) | Popup UI | B | 14 Nov (polish 17 Nov) | ⏳ pending |
| [B16](B16.md) | Groq/OpenRouter backup wiring | B | 16 Nov (SHOULD) | ⏳ pending |
| [Z1](Z1.md) | Wire real stages; happy path end-to-end | A+B | 12 Nov (milestone), extension path 14 Nov | ⏳ pending |
| [Z2](Z2.md) | Demo scripts and runbook | A+B | 16 Nov | ⏳ pending |
| [Z3](Z3.md) | Setup tooling, README, release | A+B | 18 Nov | ⏳ pending |
| [Z4](Z4.md) | Clean-machine rehearsal and acceptance | A+B | 19 Nov | ⏳ pending |

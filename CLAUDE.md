# CLAUDE.md — instructions for Claude Code in this repo

@AGENTS.md

The shared rules are in `AGENTS.md` above. These additions apply to Claude Code, which Aryan uses.

## Aryan's track

- Track A (A1–A12) is done. **Do not write Track B code (B1–B16).** Om builds it with Codex. Claude Code's role there is to **review Om's PRs** against Track A's interfaces, the task file and SRS, when Aryan asks.
- Z1–Z4 are joint. Start them only when Aryan says so, and only after the B tasks they depend on are merged.
- Aryan may waive the prerequisite lesson for a task. Owed lessons and walkthroughs (A1–A12) are given when he asks.

## Git (Aryan's commits)

- A and Z tasks and repo docs are committed straight to `build/v1` and pushed after review. Close the task's issue and check CI.
- End every commit with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Never merge a PR without Aryan's say-so.
- **Never commit `docs/LEARNING-RESOURCES.md`** (kept local on purpose).

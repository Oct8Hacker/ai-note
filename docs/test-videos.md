# Test videos

The videos both of us use for the Z1 happy path, the Z2 demo scripts and the Z4 rehearsal. Pick public lecture videos that show **slides** and have **captions** (manual or auto). Before adding a video, check it in the browser: it plays without signing in, isn't age-restricted, and the CC button shows captions.

| # | Purpose | Expected result | Video URL | Length | Agreed by |
|---|---|---|---|---|---|
| 1 | Short lecture with slides (~5 min) | `COMPLETED` | _TBD_ | | |
| 2 | Medium lecture with slides (~12 min) | `COMPLETED` | _TBD_ | | |
| 3 | Long lecture with slides (~19 min, under 20 min) | `COMPLETED`; this is the timing run | _TBD_ | | |
| 4 | Over 20 min | `FAILED`, `VIDEO_TOO_LONG`, before captions are fetched (Constraint A) | _TBD_ | | |
| 5 | Private or unavailable | `DEAD_LETTERED`, `RETRIES_EXHAUSTED` after the retry budget | _TBD_ | | |
| 6 | No captions at all | `FAILED`, `NO_TRANSCRIPT` | _TBD_ | | |

## Timing runs (NFR-PERF-2: ≤ 90 s end to end)

Run with `NOTION_TEST_TOKEN=… scripts/demo/happy.sh <url>`. The script prints the end-to-end time.

| Date | Video # | Destination (page/database) | LLM provider | Final status | End-to-end (s) | Frames requested / published | Notes |
|---|---|---|---|---|---|---|---|
| | | | | | | | |

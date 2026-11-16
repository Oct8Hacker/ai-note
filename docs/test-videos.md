# Test videos

The videos both of us use for the Z1 happy path, the Z2 demo scripts and the Z4 rehearsal. Pick public lecture videos that show **slides** and have **captions** (manual or auto). Before adding a video, check it in the browser: it plays without signing in, isn't age-restricted, and the CC button shows captions.

| # | Purpose | Expected result | Video URL | Length | Agreed by |
|---|---|---|---|---|---|
| 1 | Short lecture with slides (~5 min) | `COMPLETED` | https://www.youtube.com/watch?v=RT-hUXUWQ2I (proposed: Bubble Sort, CS50 Shorts; manual English captions) | 5:55 | Aryan ☐ Om ☐ |
| 2 | Medium lecture with slides (~12 min) | `COMPLETED` | https://www.youtube.com/watch?v=-4E2-0sxVUM (proposed: Computer Vision, Crash Course CS #35; manual English captions) | 11:09 | Aryan ☐ Om ☐ |
| 3 | Long lecture with slides (~19 min, under 20 min) | `COMPLETED`; this is the timing run | https://www.youtube.com/watch?v=BnJ013X02b8 (proposed: Command Line, CS50 Shorts; manual English captions) | 17:52 | Aryan ☐ Om ☐ |
| 4 | Over 20 min | `FAILED`, `VIDEO_TOO_LONG`, before captions are fetched (Constraint A) | https://www.youtube.com/watch?v=6FkRvTtUc-o (proposed; `too-long.sh` PASS 13 Nov) | 60:51 | Aryan ☐ Om ☐ |
| 5 | Private or unavailable | `DEAD_LETTERED`, `RETRIES_EXHAUSTED` after the retry budget | https://www.youtube.com/watch?v=aaaaaaaaaaa (proposed: a valid-format ID that doesn't exist, so yt-dlp reports "Video unavailable"; `dlq.sh` PASS 13 Nov) | — | Aryan ☐ Om ☐ |
| 6 | No captions at all | `FAILED`, `NO_TRANSCRIPT` | _TBD_ | | |

## Timing runs (NFR-PERF-2: ≤ 90 s end to end)

Run with `NOTION_TEST_TOKEN=… scripts/demo/happy.sh <url>`. The script prints the end-to-end time.

| Date | Video # | Destination (page/database) | LLM provider | Final status | End-to-end (s) | Frames requested / published | Notes |
|---|---|---|---|---|---|---|---|
| 2026-11-16 | 1 (Bubble Sort) | page (AI Video Notes) | gemini / gemini-3-flash-preview | `COMPLETED` | 80 | 8 / 8 | No warnings. All frames verified. |
| 2026-11-16 | 1 (Bubble Sort), **from the extension** | page (AI Video Notes) | gemini / gemini-3-flash-preview | `COMPLETED` | 90 | 7 / 7 | No warnings. Z1 step 5. |
| 2026-11-16 | 3 (Command Line) | page (AI Video Notes) | gemini / gemini-3.5-flash-lite | `COMPLETED` | 11 | 0 / 0 | flash-lite flagged no visual moments; yt-dlp 2025.10.22 at the time. |
| 2026-11-16 | 3 (Command Line), **from the extension** | page (AI Video Notes) | gemini / gemini-3-flash-preview | `COMPLETED` | 110 | 8 / 8 | No warnings, no retries. Stages: transcript 8 s, summary 15 s, frames 74 s, publish 12 s. Over the 90 s target, inside the 3 min bound; frame capture from the 18-min stream dominates. |
| 2026-11-16 | lec22 Equivalence Relations (16:40, extra), from the extension | page (AI Video Notes) | gemini / gemini-3-flash-preview | `COMPLETED_WITH_WARNINGS` | 111 | 8 / 8 | `FRAME_VERIFY_SKIPPED`: the verification call failed, so frames kept their original captions (FR-PIPE-24). |
| | | | | | | | |

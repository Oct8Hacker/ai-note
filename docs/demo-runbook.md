# Demo runbook

The live demo, in presentation order. Every script is in `scripts/demo/` and ends with one `PASS <script>: …` or `FAIL <script>: …` line. Video URLs come from [`test-videos.md`](test-videos.md).

## Before the demo (10 min)

1. Start Docker Desktop. Then start the stack fresh with the demo profile:
   ```bash
   docker compose -f deploy/docker-compose.yml down -v
   docker compose -f deploy/docker-compose.yml --env-file .env --env-file deploy/demo.env up -d --build
   ```
   The demo profile (`deploy/demo.env`) sets short retries (3/6/12/24 s) and a 1.5 s delay per frame. Without it, `dlq.sh` takes ~7 minutes and `kill-worker.sh` may miss the frame stage.
2. Export the Notion token in the terminal you'll demo from (never on screen):
   ```bash
   export NOTION_TEST_TOKEN=...      # internal integration token
   ```
3. Open in the browser: the "AI Video Notes" page in Notion, the RabbitMQ UI at http://localhost:15672 (guest/guest), and a YouTube tab with video #1.
4. **Turn off any VPN** (Cloudflare WARP included) on the demo laptop. With WARP on, YouTube refused captions for hours on 17–18 Nov.
   Dry run: `scripts/demo/health.sh` must PASS.
5. **LLM quota:** a round makes about 13 LLM jobs, and each job makes two calls (summary and frame verification). With `gemini-3-flash-preview` on the free tier (5 requests/min, 20/day), set `LLM_RPM=4` (one under Google's limit, see README §5) and run verification on `gemini-3.5-flash-lite` (`LLM_VISION_MODEL`), so a round costs the main model about 13 requests. Check the quota left in Google AI Studio first. The Groq backup covers outages, not a used-up quota.

## Presentation order

| # | Command | What to say | What the audience sees |
|---|---|---|---|
| 1 | `scripts/demo/health.sh` | "Gateway, Postgres, RabbitMQ and Redis are up, two workers are heartbeating, and no provider has rate-limited us yet." | JSON with every check `ok`, `workers_alive: 2`, `provider_429_total: 0`; PASS |
| 2 | Extension: open video #1, click **Take Notes** | "The extension submits the video to the gateway, which stores the job and queues it through the outbox." | Popup steps through Transcribing → Summarizing → Extracting frames → Publishing → **View in Notion** |
| 3 | Open the Notion page | "Every image is a real frame captured by ffmpeg from the video at the timestamp in its caption (Constraint B). Timestamps link back to that second of the video." | Headings, bullets, frames with captions |
| 4 | `scripts/demo/happy.sh <video #2>` | "Same flow through the API, with each status change and the end-to-end time." | Status lines, results JSON, `end-to-end: Ns`; ≤ 90 s target |
| 5 | `scripts/demo/scale.sh 3`, then `scale.sh 2` | "Workers are stateless. Scaling is one compose command, and health sees each worker's heartbeat." | `workers_alive=3`, then `=2`; PASS twice |
| 6 | `scripts/demo/burst.sh <#1> <#2> <#3>` | "Six jobs at once share one Redis rate limiter across all workers, so we never exceed a provider's limit." | Six jobs progress together; `provider_429_total unchanged`; PASS |
| 7 | `scripts/demo/kill-worker.sh <video #2>` | "I SIGKILL the worker holding this job mid-frame-extraction. RabbitMQ redelivers the message at once; as soon as the dead worker's 6-second heartbeat expires, another worker takes the lease and resumes from the last checkpoint. The job still ends with exactly one page." | `killed worker …`, `took over in Ns` (≤ 15; about 6–8 s), `progress moved …`, `one Notion page`; PASS |
| 8 | `scripts/demo/force-429.sh llm <video #1>` (also `notion`, `youtube`) | "70 % of LLM calls now return 429. The job waits with a countdown instead of failing, and rate-limit retries don't use up its failure budget." Show the popup's waiting state if the extension is open. | `{"status":…,"waiting":{…}}`, `fault off`, completed; PASS |
| 9 | `scripts/demo/frames-fail.sh <video #1>` | "If some frames fail we publish with warnings. If all fail we still publish a text-only page and never substitute an image." | First job `COMPLETED_WITH_WARNINGS`, second `frames_published: 0`; PASS |
| 10 | `scripts/demo/too-long.sh <video #4>` | "Videos over 20 minutes are rejected before any processing (Constraint A)." | `FAILED VIDEO_TOO_LONG`; PASS |
| 11 | `scripts/demo/dlq.sh <video #5>` | "A private video can never succeed. After its retries it is dead-lettered and kept for inspection and replay." Show the `jobs.dlq` queue in the RabbitMQ UI. | `DEAD_LETTERED after Ns` (< 60) and listed in `/admin/dlq`; PASS |
| 12 | Extension: **Take Notes** on video #1, then **Cancel job** while it is Transcribing, Summarizing or Extracting frames | "A job can be cancelled until the worker starts creating its Notion page. The worker holding it loses its lease, stops at once and never writes another checkpoint, so no page appears. After page creation starts the button disappears and the gateway answers 409, so a cancel can never leave half a page." | Popup shows **Cancelled**; no new Notion page |

## If something fails

| Symptom | Fix |
|---|---|
| `health.sh`: a check is not `ok` | `docker compose -f deploy/docker-compose.yml ps`, then the logs of that service. `scripts/doctor.sh` runs the worker's preflight checks. |
| Jobs wait on `youtube` for a long time, subtitles get "HTTP Error 429", or yt-dlp says "Sign in to confirm you're not a bot" | Turn off any VPN (Cloudflare WARP included): YouTube limits shared VPN addresses. If it persists, set `YTDLP_COOKIES_FILE` (README → Troubleshooting) and restart the workers. |
| Jobs wait on `llm` without faults on | The free quota is used up. Switch `LLM_MODEL`, `LLM_PROVIDER` or the API key, then restart the workers. |
| `dlq.sh` takes minutes | The demo profile isn't loaded; restart with `--env-file deploy/demo.env`. |
| `kill-worker.sh`: "job reached PUBLISHING before EXTRACTING_FRAMES" or no lease owner | The demo profile isn't loaded, or the video has very few visual moments. Use a longer slide-heavy video. |
| A fault seems stuck on | `curl -X PUT localhost:8080/admin/faults -H "Authorization: Bearer $ADMIN_TOKEN" -d '{"llm_429_rate":"0","notion_429_rate":"0","youtube_429_rate":"0","frame_fail_rate":"0","frame_fail_all":"false"}'` |

## Acceptance check (SRS §15.5 item 2)

Run every script twice in a row on a fresh `down -v` / `up` with the demo profile, and record the result here. One command does a whole round: it asks for the Notion token (hidden), wipes and restarts the stack with the demo profile, runs every script in the order above, and prints a PASS/FAIL summary (full output in `tmp/demo-round-*.log`):

```bash
scripts/demo/run-all.sh --fresh
```

A round makes about 13 LLM jobs. One free-tier key (20 requests/day on `gemini-3-flash-preview`) covers one round, so the second round in a row needs a second key: swap `GEMINI_API_KEY` in `.env` between rounds.

| Date | Run | health | happy | scale | burst | kill-worker | force-429 llm/notion/youtube | frames-fail | too-long | dlq |
|---|---|---|---|---|---|---|---|---|---|---|
| 2026-11-17 | 1 | PASS | PASS (113 s; rerun 111 s) | PASS (rerun) | PASS 6/6, 0 LLM 429s | PASS (rerun: taken over in 6 s, 1 page) | PASS / PASS / PASS | PASS | PASS | PASS (53 s) |
| 2026-11-18 | 2 | PASS | PASS (57 s) | PASS | PASS 6/6, 0 LLM 429s (287 s) | PASS (taken over in 6 s, 1 page) | PASS / PASS / PASS | PASS | PASS | PASS (52 s) |
| 2026-11-19 | 3 (cancel feature, v1.1.0) | PASS | PASS (49 s) | PASS | PASS 6/6, 0 LLM 429s (365 s) | PASS (taken over in 6 s, 1 page) | PASS / PASS / PASS | PASS | PASS | PASS (54 s) |

Round 1 notes: the first pass was 8/12. `happy.sh` (job COMPLETED but no PASS line), `scale.sh` (wrong `.env` path under Git Bash) and `kill-worker.sh` (measured the next frame instead of the takeover; Notion search lag) were script bugs, fixed in the same commit as this note; all four passed on the rerun. Round 2 needs a second Gemini key (one free-tier key covers one round a day).

Round 2 notes: 12/12 in one run on a fresh stack. Two earlier attempts the same night failed for reasons outside the scripts: the daily LLM budget was spent by rate-limit retries (fixed in bfb03fe, `LLM_RPM` lowered to 4), and Cloudflare WARP was on, so YouTube refused captions (HTTP 429) for over 12 hours. With WARP off the round passed, and every job was faster (happy 57 s instead of 111 s).

Round 3 notes: run on `build/v1` after merging the cancel feature (PR #47), 12/12 on a fresh stack. An earlier attempt the same day was void: `run-all.sh --fresh` started compose without `--build`, so it reused cached v1.0.0 images (`/healthz` reported version `dev`) and tested old code; fixed in cb8ba19. In that void run `force-429 llm` also failed once by chance: with a 70 % 429 rate the summary call can get through and only the verify call is refused, which ends as a `FRAME_VERIFY_SKIPPED` warning with no waiting state. A rerun passed.

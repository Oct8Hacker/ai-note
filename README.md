# AI-Note

Turn a YouTube lecture into organized Notion notes, with screenshots captured directly from the video.

Open a video, click **Take Notes**, and AI-Note will:

1. Read its captions.
2. Generate structured notes.
3. Capture relevant frames from the video.
4. Save the notes and images to your Notion workspace.

**Supported videos:** up to **20 minutes**, with accessible captions.

[Setup](#setup) · [Troubleshooting](#troubleshooting) · [Notion OAuth](#optional-connect-notion-with-oauth) · [Default destination](#default-notion-destination) · [Configuration](#configuration-reference) · [API](#api-reference) · [Failure demos](#demonstrating-failure-modes) · [Architecture](#architecture-and-design-choices)

## Before you begin

You will need:

| Requirement | What it is used for |
|---|---|
| Git | Downloading the project |
| Docker Desktop, running | Running the backend and its supporting services |
| Google Chrome | Running the extension |
| A Gemini API key | Generating notes and checking screenshots |
| A Notion account | Saving your notes |

Get your API key from [Google AI Studio](https://aistudio.google.com/apikey).

You do **not** need to install Go, ffmpeg, or yt-dlp on your computer for the Docker setup. They are included in the build or worker containers.

Node.js and npm are only needed if you choose to build the extension yourself.

## Setup

Follow these steps in order. Run terminal commands from the project folder unless a step says otherwise.

### 1. Download the project

Open PowerShell on Windows, or Terminal on macOS/Linux:

```sh
git clone https://github.com/Oct8Hacker/ai-note.git
cd ai-note
git switch build/v1
```

Keep this terminal open for the following steps.

### 2. Create your configuration

Run the command for your operating system.

**Windows PowerShell:**

```powershell
powershell -ExecutionPolicy Bypass -File scripts\setup.ps1
```

**macOS/Linux or Git Bash:**

```bash
bash scripts/setup.sh
```

The script:

- Creates a `.env` file in the project folder.
- Generates the encryption key and admin token.
- Asks you to paste your Gemini API key.

The API key is hidden while you enter it. Paste it and press **Enter**.

If you skipped the prompt, open `.env` in a text editor and fill in:

```dotenv
GEMINI_API_KEY=your_api_key_here
```

Keep `.env` private. Do not upload it to GitHub.

### 3. Choose your AI model

Open `.env` in a text editor. On Windows, you can use:

```powershell
notepad .env
```

Set `LLM_MODEL` to a Gemini model available to your API key. The project has been tested with this configuration:

```dotenv
LLM_PROVIDER=gemini
LLM_MODEL=gemini-3-flash-preview
LLM_RPM=4
LLM_RPD=20
```

**Check the model's availability and limits in Google AI Studio before using these values.** Model access and quotas can differ between accounts and change over time.

When editing `.env`:

- Remove the leading `#` from a setting to enable it.
- Replace existing settings instead of adding duplicate entries.
- Save the file before continuing.

`LLM_RPM` controls requests per minute, and `LLM_RPD` controls requests per day. These limits are shared by all workers. The current limiter can allow one additional request within a minute, so the project recommends `LLM_RPM=4` for a model with a 5-request-per-minute limit.

For your first setup, leave the other settings unchanged. The same model will generate notes and verify frames.

### 4. Start the backend

Make sure Docker Desktop is running, then execute:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env up -d --build
```

The first build may take several minutes.

This starts the gateway, two workers, PostgreSQL, Redis, and RabbitMQ.

Check the containers:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env ps
```

Then open [the health endpoint](http://localhost:8080/healthz) in your browser.

**You are ready to continue when the response includes:**

```json
{
  "status": "ok",
  "workers_alive": 2
}
```

The response includes other fields too. If the workers are still starting, wait briefly and refresh.

### 5. Check the model and video tools

Run:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env exec -T worker /app/worker doctor
```

This checks:

- The text model.
- The vision model.
- yt-dlp.
- ffmpeg.
- YouTube connectivity.
- Notion OAuth configuration, when enabled.

**Look for `"ok": true` in each result.**

A message saying OAuth is disabled is expected when using the token-based Notion setup below.

If a model or YouTube check fails, use the troubleshooting section before submitting a video.

### 6. Install the Chrome extension

1. Open the project's [Releases page](https://github.com/Oct8Hacker/ai-note/releases).
2. Download the `ainote-extension-<version>.zip` asset.
3. Extract the ZIP into a folder you intend to keep.
4. In Chrome, enter `chrome://extensions` in the address bar.
5. Turn on **Developer mode**.
6. Click **Load unpacked**.
7. Select the extracted folder containing `manifest.json`.

The extension should now appear on the extensions page.

Use Chrome's extensions menu to pin **AI-Note** so its popup is easy to open.

**Keep the gateway URL as `http://localhost:8080`** for the standard local setup.

<details>
<summary>Alternative: build the extension from source</summary>

Install Node.js and npm compatible with the extension's dependencies, then run from the project folder:

```sh
npm --prefix extension ci && npm --prefix extension run build
```

In Chrome's **Load unpacked** dialog, select:

```text
ai-note/extension/dist
```

Select `dist`, not the source `extension` folder.

</details>

### 7. Prepare Notion

For local use, the simplest option is an internal Notion connection.

**Create the connection:**

1. Open [Notion's developer connections](https://www.notion.so/profile/integrations).
2. Create a new connection named **AI Note**.
3. Select your workspace.
4. Choose the **internal/token** authentication option.
5. Enable **Read content**, **Update content**, and **Insert content**.
6. Copy the connection's secret token.

Notion may use the word **integration** instead of **connection** on some screens.

**Create the destination page:**

1. In Notion, create a page named exactly **AI Video Notes**.
2. Open that page.
3. Click **•••** at the top-right of the page.
4. Find **Connections** or **Connect to**.
5. Add the **AI Note** connection.

Alternatively, use the connection's **Access → Edit access** settings to grant access to the page.

**Checkpoint:** the page's connections list should include **AI Note**. Creating the connection alone does not give it access to your page.

### 8. Connect the extension to Notion

1. Open the AI-Note extension popup.
2. Paste your Notion secret into **Integration token**.
3. Click **Connect with token**.
4. Select **AI Video Notes** in the destination picker.

If the page is missing, check its connection permissions in Notion and refresh the destination list.

For this setup, leave these `.env` settings empty:

```dotenv
NOTION_CLIENT_ID=
NOTION_CLIENT_SECRET=
```

The Notion token goes into the extension popup, not into either of those fields.

### 9. Generate your first notes

1. Open a public YouTube video with captions and a duration of **20 minutes or less**.
2. Refresh the page if it was already open when you installed the extension.
3. Click **Take Notes** below the video player.
4. Open the extension popup to follow progress.
5. When processing finishes, click **View in Notion**.

Start with a short lecture or tutorial for your first test.

Processing time depends on the video, model availability, and rate limits. A job waiting for quota may take longer than one that can run immediately.

**Setup is complete when the generated page appears in Notion.**

## Using AI-Note again

Keep Docker Desktop running while processing videos.

To start the backend:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env up -d
```

To stop it while keeping its saved data:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env stop
```

You do not need to recreate your Notion connection each time.

## Changing the model

Edit these settings in `.env`:

```dotenv
LLM_PROVIDER=gemini
LLM_MODEL=your_model_name
LLM_RPM=your_configured_requests_per_minute
LLM_RPD=your_configured_requests_per_day
```

Replace the placeholders with actual values for your account.

Apply the changes:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env up -d --force-recreate worker
```

Verify the new configuration:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env exec -T worker /app/worker doctor
```

If you explicitly configured `LLM_VISION_PROVIDER` or `LLM_VISION_MODEL`, check those settings as well. Otherwise, frame verification uses the main provider and model.

All supported settings are documented in [`.env.example`](https://github.com/Oct8Hacker/ai-note/blob/build/v1/.env.example).

## Troubleshooting

### The extension cannot connect

Open [the health endpoint](http://localhost:8080/healthz).

If it does not load:

1. Make sure Docker Desktop is running.
2. Start the backend.
3. Check the container status:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env ps
```

The extension's gateway URL should be `http://localhost:8080` unless you deliberately changed the port.

### The Take Notes button is missing

Check that AI-Note is enabled at `chrome://extensions`, then refresh the YouTube video page.

### The model check fails, or summarizing keeps failing

Run the doctor command from step 5, then inspect recent worker logs:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env logs --tail=100 worker
```

| Error | What to check |
|---|---|
| Key rejected or authentication failure | Confirm `GEMINI_API_KEY` is correct. |
| Model not found | Confirm `LLM_MODEL` is available to your account. |
| `429` or quota exceeded | Check your provider's quota and your configured rate limits. |
| `503` or `504` | The provider may be unavailable or timing out. Allow retries, or switch to another available model. |

After editing model settings, recreate the workers using the command under **Changing the model**.

A successful health check confirms the infrastructure is running; the doctor command checks model access separately.

### The job is waiting for the model

The shared rate limiter may be delaying requests to stay within the configured quota.

Check the popup's waiting information. Repeatedly submitting the same video will not increase the available quota.

If you have reached your provider's daily quota, wait for its reset or configure another available model/provider with appropriate limits.

### The Notion destination is missing

Open **AI Video Notes** in Notion and grant your connection access through the page's **••• → Connections** menu.

Then reopen or refresh the extension's destination picker.

This also applies to `NOTION_NO_DESTINATION` and `NOTION_PARENT_NOT_FOUND` errors.

### YouTube asks you to confirm you are not a bot, or subtitles fail with HTTP 429

**First, turn off any VPN** (Cloudflare WARP / 1.1.1.1, a work VPN, a proxy). They send your traffic, including the workers', through addresses many people share, and YouTube limits those. Check with `curl -s https://www.cloudflare.com/cdn-cgi/trace | grep warp`: it should say `warp=off`. With WARP on, YouTube refused captions for over 12 hours during our testing; with it off, they worked at once.

If it still happens on a plain home connection, the workers may need cookies from your signed-in YouTube session.

1. Export YouTube cookies in Netscape `cookies.txt` format.
2. Create a `.ytdlp` folder in the project folder.
3. Save the file as `.ytdlp/cookies.txt`.
4. Add this setting to `.env`:

```dotenv
YTDLP_COOKIES_FILE=/ytdlp/cookies.txt
```

5. Recreate the workers:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env up -d --force-recreate worker
```

Treat the cookies file as a secret. Do not share or commit it.

### Notion OAuth reports a redirect mismatch

The redirect registered in Notion must exactly match `NOTION_REDIRECT_URI`:

```text
https://lbmicegifohcileflpbjdanghkgigcia.chromiumapp.org/notion
```

The extension ID is pinned to `lbmicegifohcileflpbjdanghkgigcia` by its manifest key. After correcting `.env`, recreate the gateway:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env up -d --force-recreate gateway
```

### A port is already in use

The stack uses host ports `8080`, `5672`, and `15672`.

Stop the conflicting service, or update the relevant host port in `deploy/docker-compose.yml`. If you change the gateway's host port, update the extension's gateway URL too.

## Optional: connect Notion with OAuth

Full OAuth is the one-time OAuth flow required by the project specification. Quick mode uses an internal integration token as a local-setup shortcut; both modes run the same job pipeline.

1. Create a public OAuth connection in [Notion's developer connections](https://www.notion.so/profile/integrations).
2. Enable **Read content**, **Update content**, and **Insert content**.
3. Register this exact redirect URI:

```text
https://lbmicegifohcileflpbjdanghkgigcia.chromiumapp.org/notion
```

4. Add the connection's OAuth credentials to `.env`:

```dotenv
NOTION_CLIENT_ID=your_client_id
NOTION_CLIENT_SECRET=your_client_secret
NOTION_REDIRECT_URI=https://lbmicegifohcileflpbjdanghkgigcia.chromiumapp.org/notion
```

5. Apply the configuration:

```sh
docker compose -f deploy/docker-compose.yml --env-file .env up -d --force-recreate gateway
```

6. Open the extension popup, click **Connect Notion**, and approve access to your destination page.

For the optional template, create a page titled **AI Video Notes**, publish it using **Share → Publish**, and paste its public URL into the connection's **Notion URL for optional template** field. Notion can copy that template into the user's workspace during connection.

## Default Notion destination

<details>
<summary>Expand default notion destination</summary>

When a job doesn't name a destination (`notion_parent_id` omitted), the worker picks one in this order:

1. `NOTION_DEFAULT_PARENT_ID` from `.env`, if set (a page or database ID).
2. Otherwise, the page titled exactly **"AI Video Notes"** that is shared with the integration. If there are several, the oldest wins. Generated notes pages are never titled this way, so the default never drifts onto an earlier result.
3. Otherwise the job fails with `NOTION_NO_DESTINATION` ("Share a page called 'AI Video Notes' with the app").

The extension always sends an explicit destination: the one chosen in the popup's picker, which defaults to "AI Video Notes".

</details>

## Configuration reference

<details>
<summary>Expand configuration reference</summary>

Everything is configured through `.env`. [`.env.example`](.env.example) lists every variable with its default; `scripts/setup.sh` copies it. Values below are application or template defaults, not a guarantee of provider quota. The setup walkthrough overrides the model and quota settings. Entries marked **required** must be supplied for the selected mode; other bold entries highlight settings required by the project brief.

| Variable | Default | Purpose |
|---|---|---|
| **`TOKEN_ENC_KEY`** | — **required** | 32 random bytes, base64. Encrypts Notion tokens inside queued jobs. Generated by setup. |
| **`ADMIN_TOKEN`** | — **required** | Bearer token for `/admin/*` and the demo scripts. Generated by setup. |
| **`LLM_PROVIDER`** | `gemini` | **LLM provider**: `gemini`, `groq`, `openrouter`, `ollama`. |
| **`GEMINI_API_KEY`** / `GROQ_API_KEY` / `OPENROUTER_API_KEY` | — **required for the chosen provider** | **LLM API key**. |
| **`LLM_MODEL`** | `gemini-3.8-flash` for gemini | **LLM model name**. |
| `LLM_VISION_PROVIDER` / `LLM_VISION_MODEL` | same as `LLM_PROVIDER` / `LLM_MODEL` | Model that checks each captured frame against its caption. |
| `LLM_FALLBACK_PROVIDER` / `LLM_FALLBACK_MODEL` | empty | Backup used when the main provider is unavailable (two failed tries in one call). It is not used when the quota runs out. |
| **`LLM_RPM`** | `8` | **Configured LLM rate limit**, requests per minute, shared by all workers. |
| `LLM_TPM` | `200000` | LLM tokens per minute. |
| `LLM_RPD` | `200` | LLM requests per day (`0` = off). |
| `LLM_RPD_RESET_TZ` | `America/Los_Angeles` | When the daily budget resets. |
| `LLM_MAX_INPUT_TOKENS` | `6000` | Transcripts longer than this are summarised in chunks. |
| `LLM_TIMEOUT` | `90s` | Per-call timeout. |
| `LIMITER_MAX_WAIT` | `60s` | Longer waits for a rate-limit token requeue the job instead of blocking a worker. |
| `OLLAMA_URL` | `http://ollama:11434` | Local provider (development only). |
| **`NOTION_CLIENT_ID`** / **`NOTION_CLIENT_SECRET`** | empty | **Required for Full OAuth mode**; leave empty for Quick mode. |
| `NOTION_REDIRECT_URI` | `https://lbmicegifohcileflpbjdanghkgigcia.chromiumapp.org/notion` | OAuth redirect; must match the public integration. |
| `CORS_ALLOWED_ORIGINS` | `chrome-extension://lbmicegifohcileflpbjdanghkgigcia` | Origins allowed to call the gateway. |
| `NOTION_DEFAULT_PARENT_ID` | empty | Default destination ([Default Notion destination](#default-notion-destination)). |
| `NOTION_RPS` | `2.5` | Notion requests per second per token. |
| `NOTION_VERSION` | `2025-09-03` | Pinned Notion API version. |
| `NOTION_MAX_IMAGE_BYTES` | `4500000` | Larger frames are re-encoded before upload. |
| `NOTION_UPLOAD_REUSE_WINDOW` | `50m` | A rerun reuses a frame upload younger than this. |
| `YOUTUBE_RPS` | `4` | YouTube requests per second (yt-dlp calls and ffmpeg stream reads), shared by all workers. |
| `YTDLP_COOKIES_FILE` | empty | Cookies for YouTube bot checks ([Troubleshooting](#troubleshooting)). |
| `MAX_VIDEO_SECONDS` | `1200` | Videos longer than this are rejected before processing. |
| `MAX_FRAMES` | `8` | Most frames per page. |
| `FRAME_CONCURRENCY` / `FRAME_TIMEOUT` | `4` / `30s` | Parallel captures, and the timeout for each ffmpeg call (time spent waiting for rate-limit tokens doesn't count). |
| `FRAME_VERIFY` | `true` | Check each frame against its caption with the vision model. |
| `SCENE_THRESHOLD` | `0.3` | Slide-change sensitivity when snapping to a slide. |
| `MAX_FRAME_DRIFT_SEC` | `3` | Farthest a captured frame may be from the LLM's timestamp. |
| `FRAME_DEDUP_WINDOW_SEC` | `5` | Visual moments closer than this are merged. |
| `NOTES_LANGUAGE` | `English` | Language of the notes. |
| **`AMQP_URL`** | `amqp://guest:guest@rabbitmq:5672/` | **Broker connection string**. |
| `AMQP_HEARTBEAT` | `10s` | Broker heartbeat. |
| `DATABASE_URL` | `postgres://notes:notes@postgres:5432/notes?sslmode=disable` | Postgres. Keep in step with `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`. |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | `notes` / `notes` / `notes` | Postgres container settings; keep consistent with `DATABASE_URL`. |
| `AINOTE_TAG` | `latest` | Compose image tag. A prerelease needs its explicit tag; registry access is required to pull private images. |
| `REDIS_URL` | `redis://redis:6379/0` | Redis. |
| **`MAX_ATTEMPTS`** | `5` | **Retry budget** for failures; then the job is dead-lettered. |
| **`MAX_RATE_LIMIT_RETRIES`** | `8` | **Separate budget for 429 retries**; these don't use up `MAX_ATTEMPTS`. |
| **`RETRY_DELAYS`** | `10s,30s,90s,270s` | **Backoff schedule** between failed attempts. |
| `DEFER_DELAY` / `MAX_DEFERRALS` | `3s` / `30` | Requeue delay and cap when another worker holds the job's lease. |
| `DELIVERY_LIMIT` | `10` | Crash-loop cap (RabbitMQ delivery limit). |
| `LEASE_TTL` | `30s` | Job lease. |
| `WORKER_HEARTBEAT_INTERVAL` / `WORKER_HEARTBEAT_TTL` | `2s` / `6s` | Worker liveness. |
| `WORKER_CONCURRENCY` | `1` | Jobs per worker container. |
| `SHUTDOWN_GRACE` | `25s` | Time to finish in-flight work on stop. |
| `DOCTOR_INTERVAL` | `60s` | How often workers re-run their preflight checks. |
| `GATEWAY_PORT` / `PUBLIC_BASE_URL` | `8080` / `http://localhost:8080` | Gateway listen port and public URL. |
| `OUTBOX_SWEEP_INTERVAL` | `2s` | Outbox relay sweep. |
| `RABBITMQ_MGMT_URL` / `RABBITMQ_USER` / `RABBITMQ_PASS` | `http://rabbitmq:15672` / `guest` / `guest` | RabbitMQ management API (health and DLQ). |
| `FAULT_LLM_429_RATE` / `FAULT_NOTION_429_RATE` / `FAULT_YOUTUBE_429_RATE` | `0` | Inject 429 responses for the named provider; fraction from 0 to 1. |
| `FAULT_FRAME_FAIL_RATE` | `0` | Fraction of frame captures to fail, from 0 to 1. |
| `FAULT_FRAME_FAIL_ALL` | `false` | Fail all frame captures for the text-only demo. |
| `FAULT_STAGE_DELAY_MS` | `0` | Artificial stage delay in milliseconds. |
| `LOG_LEVEL` | `info` | Logging. |

**Free-tier limits differ per model.** Check the limits for your own account in Google AI Studio. Set `LLM_RPM` one below the model's requests-per-minute limit because the limiter permits a one-request reserve. Set `LLM_RPD` and `LLM_TPM` to fit your available daily and token quotas. Summary generation and frame verification both consume requests; chunking and retries can consume more. You can select a separate vision model with `LLM_VISION_PROVIDER` and `LLM_VISION_MODEL`, but the configured limits count both models together.

**Paid key or another provider.** With a paid key, raise `LLM_RPM`, `LLM_TPM` and `LLM_RPD` to about 80 % of your quota. To switch provider, set `LLM_PROVIDER`, its key and `LLM_MODEL`, then restart the workers (`docker compose -f deploy/docker-compose.yml --env-file .env up -d worker`).

</details>

## API reference

<details>
<summary>Expand api reference</summary>

The base URL is `http://localhost:8080`. These examples use Bash syntax; on Windows, run them in Git Bash or WSL. Replace placeholder values before running. For authenticated examples, set `NOTION_TOKEN` to your integration token and `ADMIN_TOKEN` to the value generated in `.env`, in that shell.

All bodies are JSON. Errors look like `{"error": {"code": "…", "message": "…"}}`.

**Submit a job.** Returns `202` with `{"job_id", "status": "QUEUED", "video_id"}`. `notion_parent_id` is optional ([Default Notion destination](#default-notion-destination)). `Idempotency-Key` is optional: resending the same key returns the same job.

```bash
curl -X POST localhost:8080/jobs -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 3f1c2a' \
  -d '{"youtube_url": "https://www.youtube.com/watch?v=VIDEO_ID", "notion_token": "ntn_…", "notion_parent_id": "PAGE_OR_DATABASE_ID"}'
```

**Job status.** Returns `status`, `progress` (0–100), `video_title`, `duration_sec`, `warnings`, `waiting` (set while the job waits on a rate limit: `reason`, `provider`, `next_attempt_at`), `error`, `result` and `attempt`.

```bash
curl localhost:8080/jobs/JOB_ID
```

**Results** of a finished job: `{"status", "result": {"notion_page_url", "frames_requested", "frames_published"}, "warnings"}`. Returns `409 JOB_NOT_FINISHED` until the job ends.

```bash
curl localhost:8080/jobs/JOB_ID/results
```

**Health.** Plain `/healthz` checks infrastructure only: Postgres, RabbitMQ, Redis, `workers_alive` and `provider_429_total`. `?deep=1` adds each worker's preflight report: LLM key and model, yt-dlp, ffmpeg, YouTube reachability and the Notion OAuth setup.

```bash
curl localhost:8080/healthz
curl 'localhost:8080/healthz?deep=1'
```

**Notion OAuth config, as used by the extension:** `{"oauth_enabled": true|false, "client_id"}`.

```bash
curl localhost:8080/auth/notion/config
```

**OAuth code exchange.** Full OAuth mode only; the extension calls it after the user approves. Returns the access token and workspace details.

```bash
curl -X POST localhost:8080/auth/notion/exchange -H 'Content-Type: application/json' \
  -d '{"code": "CODE_FROM_NOTION", "redirect_uri": "https://lbmicegifohcileflpbjdanghkgigcia.chromiumapp.org/notion"}'
```

**Destinations** shared with the integration, for the popup's picker: `{"pages": [{"id", "title", "type", "icon"}]}`.

```bash
curl localhost:8080/notion/pages -H "Authorization: Bearer $NOTION_TOKEN"
```

**Admin: dead-lettered jobs**, each with its event history. Replay one: it resumes from its last checkpoint and needs the Notion token again, because tokens are never stored.

```bash
curl 'localhost:8080/admin/dlq?limit=20' -H "Authorization: Bearer $ADMIN_TOKEN"
curl -X POST localhost:8080/admin/dlq/JOB_ID/replay -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' -d '{"notion_token": "ntn_…"}'
```

**Admin: fault injection**, set live without restarting anything. Keys: `llm_429_rate`, `notion_429_rate`, `youtube_429_rate`, `frame_fail_rate` (0–1), `frame_fail_all` (`true`/`false`), `stage_delay_ms`.

```bash
curl localhost:8080/admin/faults -H "Authorization: Bearer $ADMIN_TOKEN"
curl -X PUT localhost:8080/admin/faults -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' -d '{"llm_429_rate": "0.7"}'
```

</details>

## Demonstrating failure modes

<details>
<summary>Expand demonstrating failure modes</summary>

Run these helpers in Bash (Git Bash or WSL on Windows); they also require `curl` and `jq`. Run from the repository root.

Start the stack with the demo profile (short retries, slow frames), then run the scripts in `scripts/demo/`. Each ends with a `PASS` or `FAIL` line. [`docs/demo-runbook.md`](docs/demo-runbook.md) gives the presentation order and what to show for each.

```bash
docker compose -f deploy/docker-compose.yml --env-file .env --env-file deploy/demo.env up -d
export NOTION_TEST_TOKEN=ntn_…            # scripts that submit jobs read the token from here
```

**This deletes local job and queue data.** To run a whole round on a fresh stack: `scripts/demo/run-all.sh --fresh` (wipes the stack, starts it with the demo profile, runs every script and prints a PASS/FAIL summary).

| Script | Shows | Passes when |
|---|---|---|
| `health.sh` | Sanity check | All checks `ok`, `workers_alive ≥ 2` |
| `happy.sh <url>` | Full flow through the API | Status changes printed; ends `COMPLETED` with a Notion URL and the end-to-end time |
| `burst.sh <url>…` | 6 jobs at once | All complete; `provider_429_total` doesn't change |
| `kill-worker.sh <url>` | SIGKILL the worker holding a job mid-frames | Another worker resumes it within 15 s; exactly one Notion page |
| `force-429.sh llm\|notion\|youtube <url>` | 70 % of calls to one provider return 429 | The job waits instead of failing, then completes |
| `dlq.sh <private-url>` | A video that can never succeed | `DEAD_LETTERED` within 60 s; listed in `/admin/dlq` and the RabbitMQ UI (http://localhost:15672, guest/guest) |
| `frames-fail.sh <url>` | Half the frames fail, then all of them | `COMPLETED_WITH_WARNINGS`, then a text-only page |
| `too-long.sh <url>` | A video over 20 minutes | `FAILED VIDEO_TOO_LONG` before captions are fetched |
| `scale.sh N` | Add or remove workers | `workers_alive = N` |

</details>

## Architecture and design choices

<details>
<summary>Expand architecture and design choices</summary>

```
 ┌──────────────────────── Chrome ─────────────────────────────────┐
 │  Content script ──msg──► Background (service worker) ◄──► Popup │
 │  (Take Notes button)     (OAuth, submit, poll)                  │
 └───────────────────────────────┬─────────────────────────────────┘
                                 │ HTTP JSON
                                 ▼
                        ┌─────────────────┐   OAuth code exchange
                        │   API Gateway   │──────────────────────► Notion OAuth
                        │      (Go)       │
                        └──┬──────────┬───┘
               insert job  │          │ publish (via outbox, confirms)
               + outbox    ▼          ▼
                     ┌──────────┐  ┌──────────────────────────────────┐
                     │ Postgres │  │ RabbitMQ                         │
                     │  (state) │  │  jobs.work ─► jobs.retry.<delay> │
                     └────▲─────┘  │      │          (TTL → back)     │
                          │        │      └─► jobs.dlq                │
            checkpoints,  │        └───────────┬──────────────────────┘
            status, frames│                    │ consume (prefetch 1, manual ack)
                     ┌────┴────────────────────▼────┐      ┌───────┐
                     │  Worker × N (Go)             │◄────►│ Redis │ token buckets,
                     │  Transcribe → Summarize →    │      └───────┘ liveness, faults
                     │  Extract Frames → Publish    │
                     └──┬─────────┬──────────┬──────┘
                        ▼         ▼          ▼
                  YouTube     LLM provider   Notion API
                (yt-dlp,     (Gemini /      (pages, blocks,
                 ffmpeg)      backups)       file uploads)
```

The gateway only accepts and reports jobs; it never runs pipeline work. Each worker runs all four stages and saves a checkpoint after each one, so a retried or redelivered job resumes where it stopped.

| Decision | Choice | Why |
|---|---|---|
| Language | Go | Goroutines and channels for concurrent frames and heartbeats; small static binaries. |
| Broker | RabbitMQ | Native acks and redelivery when a worker dies, dead-letter exchanges, TTL-based delayed retries, delivery limits for poison messages, and a management UI. |
| Job state | Postgres with checkpoints and events | A transaction saves a stage's result and advances the status together; checkpoints make resume possible. |
| Enqueue | Transactional outbox | A job is never lost or duplicated if the broker is down or the gateway crashes right after the commit. |
| Rate limits | Redis token buckets (Lua), shared by every worker, set to ~80 % of the provider's limit | Prevents 429s instead of reacting to them, however many workers run. |
| Retries | One TTL queue per delay plus a dead-letter exchange | Exponential backoff without a scheduler or a sleeping worker. |
| Crash safety | Ack only after the durable commit; leases with Redis heartbeats; idempotent stages | At-least-once delivery is only safe if a redelivery is harmless. |
| Frames | ffmpeg seeks on the stream URL, snaps to slide changes and keeps the sharpest of 3 candidates | Every image is a real frame at the right moment, without downloading the whole video. |
| LLM | Gemini Flash free tier behind a provider interface, with Groq/OpenRouter backups | Free and image-capable; the tight free limits show the rate limiter working. |
| Notion token | Passed per job, AES-GCM encrypted in the broker, never stored | The gateway never keeps the token, and queued messages are safe at rest. |

</details>

## Development and demos

For local development, install Go and Node.js/npm compatible with the project. Integration tests require Docker; the lint script requires Bash.

Run backend checks:

```sh
go vet ./...
go test ./...
go test -tags=integration -p 1 ./...
bash scripts/lint.sh
```

Run extension checks:

```sh
npm --prefix extension ci
npm --prefix extension test
npm --prefix extension run build
```

Further documentation:

- [System requirements and specification](https://github.com/Oct8Hacker/ai-note/blob/build/v1/docs/SRS.md)
- [Demo runbook and failure scenarios](https://github.com/Oct8Hacker/ai-note/blob/build/v1/docs/demo-runbook.md)
- [Task breakdown](https://github.com/Oct8Hacker/ai-note/blob/build/v1/docs/tasks/README.md)
- [Instructions for coding agents](https://github.com/Oct8Hacker/ai-note/blob/build/v1/AGENTS.md)

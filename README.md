# AI-Note

Turn a YouTube lecture into organized Notion notes, with screenshots captured directly from the video.

Open a video, click **Take Notes**, and AI-Note will:

1. Read its captions.
2. Generate structured notes.
3. Capture relevant frames from the video.
4. Save the notes and images to your Notion workspace.

**Supported videos:** up to **20 minutes**, with accessible captions.

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
npm --prefix extension ci
npm --prefix extension run build
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

### YouTube asks you to confirm you are not a bot

The workers may need cookies from your signed-in YouTube session.

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

### A port is already in use

The stack uses host ports `8080`, `5672`, and `15672`.

Stop the conflicting service, or update the relevant host port in `deploy/docker-compose.yml`. If you change the gateway's host port, update the extension's gateway URL too.

## Optional: connect Notion with OAuth

Use this instead of the integration-token flow when you need a public OAuth connection.

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

You can also configure a published **AI Video Notes** page as the connection's optional template.

## How it works

```text
Chrome extension
      ↓
API gateway
      ↓
RabbitMQ job queue
      ↓
Go workers
      ├── Read captions
      ├── Generate notes
      ├── Capture and verify video frames
      └── Publish to Notion
```

PostgreSQL stores job progress and checkpoints. Redis coordinates rate limits and worker liveness.

Workers save progress between stages so interrupted jobs can resume. Temporary failures are retried; jobs that exhaust their retry budget go to a dead-letter queue.

Images come from the source video. They are not AI-generated.

## API overview

The local API runs at `http://localhost:8080`.

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Infrastructure and worker health |
| `GET /healthz?deep=1` | Health plus worker preflight reports |
| `POST /jobs` | Submit a video |
| `GET /jobs/{job_id}` | Read status and progress |
| `GET /jobs/{job_id}/results` | Read the finished result |
| `GET /notion/pages` | List accessible Notion destinations |
| `GET /admin/dlq` | Inspect dead-lettered jobs |
| `POST /admin/dlq/{job_id}/replay` | Replay a dead-lettered job |

Admin endpoints require the `ADMIN_TOKEN` generated during setup.

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

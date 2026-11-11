import { GatewayClient } from "../shared/api";
import { errorMessage, summarizeWarnings } from "../shared/messages";
import { getJobs, getSettings, upsertJob } from "../shared/storage";
import type { TrackedJob } from "../shared/types";
import { disconnect } from "./auth";
import { isTerminal, updateBadge } from "./jobs";

async function refresh(client: GatewayClient, job: TrackedJob) {
  if (job.results) return;
  if (!isTerminal(job.last?.status)) {
    const last = await client.getJob(job.jobId);
    if (last.error?.code === "NOTION_UNAUTHORIZED")
      await disconnect(job.notionConnectionId ?? null);
    job = { ...job, title: last.video_title ?? job.title, last };
    await upsertJob(job);
    if (!isTerminal(last.status)) return;
  }
  const results = await client.getResults(job.jobId);
  if (results === "NOT_FINISHED") return;
  await upsertJob({ ...job, results });
  const success =
    results.status === "COMPLETED" ||
    results.status === "COMPLETED_WITH_WARNINGS";
  const message = success
    ? results.warnings.length
      ? summarizeWarnings(results.warnings, results.result)
      : "Your notes are ready."
    : errorMessage(results.error?.code ?? job.last?.error?.code ?? "");
  await chrome.notifications.create(job.jobId, {
    type: "basic",
    iconUrl: chrome.runtime.getURL("note.png"),
    title: job.title || job.videoId,
    message,
    ...(success && results.result?.notion_page_url
      ? { buttons: [{ title: "View in Notion" }] }
      : {}),
  });
}

let inFlight: Promise<void> | undefined;
export function pollOnce(_now: number): Promise<void> {
  return (inFlight ??= (async () => {
    const client = new GatewayClient((await getSettings()).gatewayUrl);
    // One offline or expired job should not prevent other jobs from updating.
    await Promise.allSettled(
      (await getJobs()).map((job) => refresh(client, job)),
    );
    await updateBadge();
  })().finally(() => {
    inFlight = undefined;
  }));
}

export function startFastPolling(): () => void {
  const tick = () => {
    void pollOnce(Date.now()).catch(() => {});
  };
  tick();
  const timer = setInterval(tick, 2000);
  return () => clearInterval(timer);
}

export async function openJobResult(id: string) {
  const page = (await getJobs()).find((job) => job.jobId === id)?.results
    ?.result?.notion_page_url;
  if (!page) return;
  const url = new URL(page);
  if (url.protocol === "https:") await chrome.tabs.create({ url: url.href });
}

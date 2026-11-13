import { GatewayClient } from "../shared/api";
import { getJobs, getSettings, upsertJob } from "../shared/storage";
import type { Status } from "../shared/types";
import { connectWithOAuth, friendlyError } from "./auth";

export function isTerminal(status?: Status) {
  return (
    status === "COMPLETED" ||
    status === "COMPLETED_WITH_WARNINGS" ||
    status === "FAILED" ||
    status === "DEAD_LETTERED"
  );
}

export async function updateBadge() {
  const active = (await getJobs()).filter(
    (job) => !isTerminal(job.last?.status),
  ).length;
  await chrome.action.setBadgeText({ text: active ? String(active) : "" });
}

export async function takeNotes(
  videoId: string,
  url: string,
): Promise<{ ok: true; jobId: string } | { ok: false; message: string }> {
  try {
    let settings = await getSettings();
    if (!settings.notionToken) {
      await connectWithOAuth();
      settings = await getSettings();
    }
    if (!settings.notionToken)
      return {
        ok: false,
        message: "Connect Notion in the extension popup first",
      };
    const job = await new GatewayClient(settings.gatewayUrl).submitJob(
      url,
      settings.notionToken,
      settings.notionParentId,
      crypto.randomUUID(),
    );
    await upsertJob({
      jobId: job.job_id,
      videoId: job.video_id || videoId,
      title: null,
      last: null,
      results: null,
      submittedAt: Date.now(),
      notionConnectionId: settings.notionConnectionId,
    });
    // A cosmetic badge failure must not turn an accepted submission into a retry.
    await updateBadge().catch(() => {});
    return { ok: true, jobId: job.job_id };
  } catch (err) {
    return { ok: false, message: friendlyError(err) };
  }
}

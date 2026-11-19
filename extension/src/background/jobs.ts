import { ApiError, GatewayClient } from "../shared/api";
import {
  getJobs,
  getSettings,
  updateUnfinishedJob,
  upsertJob,
} from "../shared/storage";
import type { JobView, Status, TrackedJob } from "../shared/types";
import { connectWithOAuth, friendlyError } from "./auth";

export function isTerminal(status?: Status) {
  return (
    status === "COMPLETED" ||
    status === "COMPLETED_WITH_WARNINGS" ||
    status === "FAILED" ||
    status === "CANCELLED" ||
    status === "DEAD_LETTERED"
  );
}

export function saveJobView(
  job: TrackedJob,
  gatewayUrl: string,
  last: JobView,
) {
  return updateUnfinishedJob(job.jobId, {
    gatewayUrl,
    title: last.video_title ?? job.title,
    last,
    ...(last.status === "CANCELLED" && {
      results: { status: last.status, warnings: last.warnings, result: null },
    }),
  });
}

const pendingCancellations = new Map<string, Promise<void>>();

export function cancelJob(id: string): Promise<void> {
  const pending = pendingCancellations.get(id);
  if (pending) return pending;
  const request = cancelTrackedJob(id).finally(() => {
    pendingCancellations.delete(id);
  });
  pendingCancellations.set(id, request);
  return request;
}

async function cancelTrackedJob(id: string) {
  const { gatewayUrl } = await getSettings();
  const job = (await getJobs()).find((item) => item.jobId === id);
  if (!job) throw new ApiError("JOB_NOT_FOUND", 404);
  if (job.last?.status === "CANCELLED") return;
  if (
    job.results ||
    isTerminal(job.last?.status) ||
    job.last?.can_cancel === false
  )
    throw new ApiError("JOB_NOT_CANCELLABLE", 409);
  const origin = job.gatewayUrl ?? gatewayUrl;
  const last = await new GatewayClient(origin).cancelJob(id);
  await saveJobView(job, origin, last);
  await updateBadge().catch(() => {});
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
      gatewayUrl: settings.gatewayUrl,
      notionConnectionId: settings.notionConnectionId,
    });
    // A cosmetic badge failure must not turn an accepted submission into a retry.
    await updateBadge().catch(() => {});
    return { ok: true, jobId: job.job_id };
  } catch (err) {
    return { ok: false, message: friendlyError(err) };
  }
}

import { useState } from "preact/hooks";
import {
  errorMessage,
  STEPS,
  summarizeWarnings,
  waitingMessage,
} from "../shared/messages";
import type { TrackedJob } from "../shared/types";
import { StepTracker } from "./StepTracker";
export function JobCard({ job, now }: { job: TrackedJob; now: number }) {
  const { last, results } = job;
  const status = results?.status ?? last?.status ?? "QUEUED";
  const done = status === "COMPLETED" || status === "COMPLETED_WITH_WARNINGS";
  const failed = status === "FAILED" || status === "DEAD_LETTERED";
  const cancelled = status === "CANCELLED";
  const active = !done && !failed && !cancelled;
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState("");
  async function cancel() {
    if (cancelling) return;
    setCancelError("");
    setCancelling(true);
    try {
      const reply = await chrome.runtime.sendMessage({
        type: "CANCEL_JOB",
        jobId: job.jobId,
      });
      if (!reply?.ok)
        setCancelError(
          reply?.message ?? "Couldn’t cancel this job. Try again.",
        );
    } catch {
      setCancelError("Couldn’t cancel this job. Try again.");
    } finally {
      setCancelling(false);
    }
  }
  const state = failed
    ? "Failed"
    : status === "COMPLETED_WITH_WARNINGS"
      ? "Done with warnings"
      : cancelled
        ? "Cancelled"
        : STEPS.find((s) => s.status === status)?.label;
  const result = results?.result ?? last?.result ?? null;
  const progress = done ? 100 : (last?.progress ?? 0);
  const url = result?.notion_page_url;
  return (
    <article class="job">
      <div class="eyeline">
        <span class={failed ? "state failed" : "state"}>{state}</span>
        <time dateTime={new Date(job.submittedAt).toISOString()}>
          {new Date(job.submittedAt).toLocaleDateString(undefined, {
            month: "short",
            day: "numeric",
          })}
        </time>
      </div>
      <h3>{job.title || last?.video_title || job.videoId}</h3>
      {!failed && !cancelled && (
        <>
          <progress aria-label="Job progress" value={progress} max={100} />
          <details open={!done}>
            <summary>Processing steps</summary>
            <StepTracker status={status} />
          </details>
        </>
      )}
      {last?.waiting && active && <p>{waitingMessage(last, now)}</p>}
      {active && (!last || last.can_cancel) && (
        <button class="text" disabled={cancelling} onClick={cancel}>
          {cancelling ? "Cancelling…" : "Cancel job"}
        </button>
      )}
      {cancelError && !cancelled && <p role="alert">{cancelError}</p>}
      {failed && (
        <p>
          {errorMessage(
            results?.error?.code ??
              last?.error?.code ??
              (status === "DEAD_LETTERED" ? "RETRIES_EXHAUSTED" : ""),
          )}
        </p>
      )}
      {status === "COMPLETED_WITH_WARNINGS" && (
        <p>
          {summarizeWarnings(results?.warnings ?? last?.warnings ?? [], result)}
        </p>
      )}
      {done && url && /^https:\/\//i.test(url) && (
        <a class="action" href={url} target="_blank" rel="noopener noreferrer">
          View in Notion
        </a>
      )}
    </article>
  );
}

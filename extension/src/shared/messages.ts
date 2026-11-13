import type { ApiError } from "./api";
import type { JobView, Result, Status, Warning } from "./types";
import { formatTimestamp } from "./time";

export const STEPS: { status: Status; label: string }[] = [
  { status: "QUEUED", label: "Queued" },
  { status: "TRANSCRIBING", label: "Transcribing" },
  { status: "SUMMARIZING", label: "Summarizing" },
  { status: "EXTRACTING_FRAMES", label: "Extracting Frames" },
  { status: "PUBLISHING", label: "Publishing" },
  { status: "COMPLETED", label: "Done" },
];
const messages: Record<string, string> = {
  VIDEO_TOO_LONG:
    "This video is longer than 20 minutes, which isn't supported.",
  NO_TRANSCRIPT: "This video has no captions, so notes can't be generated.",
  NOTION_UNAUTHORIZED: "Notion access was revoked. Please reconnect Notion.",
  NOTION_PARENT_NOT_FOUND: "The chosen Notion page isn't shared with the app.",
  NOTION_NO_DESTINATION: "Share at least one Notion page with the app.",
  LLM_CONFIG_ERROR: "The AI service isn't configured correctly.",
  RETRIES_EXHAUSTED:
    "We couldn't process this video after several attempts (it may be private or unavailable).",
  RATE_LIMIT_RETRIES_EXHAUSTED:
    "Services stayed busy for too long. Please try again later.",
  DELIVERY_LIMIT_EXCEEDED: "Processing kept crashing on this video.",
  INVALID_JSON: "The gateway couldn't read the request.",
  INVALID_URL: "Open a YouTube video and try again.",
  MISSING_NOTION_TOKEN: "Connect Notion in the extension popup first.",
  INVALID_NOTION_PARENT: "Choose a valid Notion destination.",
  PAYLOAD_TOO_LARGE: "The request was too large.",
  UNSUPPORTED_MEDIA_TYPE: "The gateway couldn't read this request format.",
  JOB_NOT_FOUND: "This job could not be found.",
  JOB_NOT_FINISHED: "Your notes are still being prepared.",
  UNAUTHORIZED: "You don't have access to this action.",
  INVALID_LIMIT: "Choose a positive number of jobs.",
  JOB_NOT_DEAD_LETTERED: "This job is not eligible for replay.",
  INVALID_FAULTS: "The test settings are invalid.",
  INTERNAL: "The gateway encountered an error. Please try again.",
  OAUTH_CODE_INVALID: "Notion sign-in expired. Please reconnect Notion.",
  NOTION_UNAVAILABLE: "Notion is unavailable. Please try again later.",
  FRAME_EXTRACTION_FAILED: "A screenshot couldn't be captured.",
  FRAME_NOT_RELEVANT:
    "A screenshot was omitted because it didn't show a relevant visual.",
  VISUAL_DROPPED_INVALID:
    "A screenshot was omitted because its timestamp or caption was invalid.",
  FRAME_VERIFY_SKIPPED: "Screenshot verification was unavailable.",
};
export function errorMessage(code: string): string {
  return Object.hasOwn(messages, code)
    ? messages[code]
    : "Something went wrong while making your notes. Please try again.";
}
export function apiErrorMessage(e: ApiError): string {
  return errorMessage(e.code);
}

export function warningMessage(w: Warning): string {
  if (w.code === "FRAME_EXTRACTION_FAILED" && w.timestamp_sec !== undefined) {
    return `1 screenshot at ${formatTimestamp(w.timestamp_sec)} couldn't be captured.`;
  }
  return errorMessage(w.code);
}

export function summarizeWarnings(ws: Warning[], r: Result | null): string {
  if (!ws.length) return "Done.";
  const missing = ws.filter((w) => w.code === "FRAME_EXTRACTION_FAILED");
  const other = ws.filter((w) => w.code !== "FRAME_EXTRACTION_FAILED");
  const lines: string[] = [];
  if (missing.length) {
    const times = missing.flatMap((w) =>
      w.timestamp_sec === undefined ? [] : [formatTimestamp(w.timestamp_sec)],
    );
    const count = r
      ? `${missing.length} of ${r.frames_requested}`
      : String(missing.length);
    const noun =
      (r?.frames_requested ?? missing.length) === 1
        ? "screenshot"
        : "screenshots";
    lines.push(
      `Done, but ${count} ${noun} couldn't be captured${times.length ? " (at " + times.join(", ") + ")" : ""}.`,
    );
  } else {
    lines.push("Done.");
  }
  return [...lines, ...new Set(other.map(warningMessage))].join(" ");
}

export function waitingMessage(job: JobView, now: number): string {
  const label =
    STEPS.find((step) => step.status === job.status)?.label ??
    (job.status === "COMPLETED_WITH_WARNINGS" ? "Done" : "Failed");
  if (!job.waiting) return label;
  const { reason, provider, next_attempt_at } = job.waiting;
  const services: Record<string, string> = {
    llm: "AI service busy",
    notion: "Notion busy",
    youtube: "YouTube busy",
  };
  const detail =
    reason === "RATE_LIMITED"
      ? (services[provider ?? ""] ?? "Service busy")
      : reason === "BUSY"
        ? "Workers busy"
        : "Temporary error";
  const seconds = Math.max(
    0,
    Math.ceil((Date.parse(next_attempt_at) - now) / 1000),
  );
  return `${label} — waiting (${detail}), retrying in ${seconds} s`;
}

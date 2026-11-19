// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/preact";
import { JobCard } from "./JobCard";
import type { JobView, TrackedJob } from "../shared/types";
afterEach(cleanup);
function card(last: Partial<JobView>) {
  const tracked: TrackedJob = {
    jobId: "job",
    videoId: "video",
    submittedAt: 0,
    title: "My lecture",
    results: null,
    last: {
      status: "QUEUED",
      can_cancel: true,
      progress: 0,
      video_title: null,
      duration_sec: null,
      job_id: "job",
      video_id: "video",
      result: null,
      error: null,
      warnings: [],
      waiting: null,
      attempt: 1,
      ...last,
    },
  };
  const view = render(<JobCard job={tracked} now={0} />);
  expect(view.container.textContent).not.toMatch(/\{|"code"/);
  return view;
}
it("StepTracker highlights the current step, labels earlier steps and shows countdown", () => {
  card({
    status: "SUMMARIZING",
    progress: 40,
    waiting: {
      reason: "RATE_LIMITED",
      provider: "llm",
      next_attempt_at: new Date(28000).toISOString(),
    },
  });
  expect(
    within(screen.getByRole("list", { name: "Job steps" }))
      .getByText("Summarizing")
      .closest("li")
      ?.getAttribute("aria-current"),
  ).toBe("step");
  expect(screen.getByText("Transcribing").closest("li")?.textContent).toMatch(
    /complete/i,
  );
  expect(screen.getByText(/retrying in 28 s/)).toBeTruthy();
  expect(screen.getByRole("progressbar").getAttribute("value")).toBe("40");
});

it("submits cancellation for a waiting job and reports a conflict", async () => {
  const sendMessage = vi
    .fn()
    .mockResolvedValue({ ok: false, message: "Too late to cancel." });
  vi.stubGlobal("chrome", { runtime: { sendMessage } });
  card({
    status: "SUMMARIZING",
    waiting: { reason: "BUSY", next_attempt_at: new Date(1000).toISOString() },
  });
  fireEvent.click(screen.getByRole("button", { name: "Cancel job" }));
  expect(sendMessage).toHaveBeenCalledWith({
    type: "CANCEL_JOB",
    jobId: "job",
  });
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Too late to cancel.",
  );
});

it("hides cancellation after the gateway closes the cancellation window", () => {
  card({ status: "PUBLISHING", can_cancel: false });
  expect(screen.queryByRole("button", { name: "Cancel job" })).toBeNull();
});

it("labels cancellation as terminal and hides steps and actions", () => {
  card({ status: "CANCELLED", can_cancel: false });
  expect(screen.getByText("Cancelled")).toBeTruthy();
  expect(screen.queryByRole("progressbar")).toBeNull();
  expect(screen.queryByRole("button")).toBeNull();
  expect(screen.queryByRole("link")).toBeNull();
});
it.each(["COMPLETED", "COMPLETED_WITH_WARNINGS"] as const)(
  "links %s to the exact result",
  (status) => {
    const url = "https://www.notion.so/lecture";
    card({
      status,
      result: {
        notion_page_url: url,
        frames_requested: 6,
        frames_published: 5,
      },
      warnings: [{ code: "FRAME_EXTRACTION_FAILED", timestamp_sec: 461 }],
    });
    const link = screen.getByRole("link", { name: "View in Notion" });
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("href")).toBe(url);
    if (status === "COMPLETED_WITH_WARNINGS")
      expect(screen.getByText(/1 of 6 screenshots/)).toBeTruthy();
  },
);
it.each([
  ["FAILED", "VIDEO_TOO_LONG", "longer than 20 minutes"],
  ["DEAD_LETTERED", "RETRIES_EXHAUSTED", "after several attempts"],
] as const)("translates %s", (status, code, text) => {
  card({ status, error: { code, message: "internal error" } });
  expect(screen.getByText(new RegExp(text))).toBeTruthy();
});

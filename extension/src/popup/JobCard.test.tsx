// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/preact";
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

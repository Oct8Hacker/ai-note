import { afterEach, expect, it, vi } from "vitest";
import { mockChromeStorage } from "../../test/chrome-mock";
import { ApiError, GatewayClient } from "./api";
import { errorMessage, summarizeWarnings, waitingMessage } from "./messages";
import { formatTimestamp } from "./time";
import { getJobs, getSettings, saveSettings, upsertJob } from "./storage";
import type { JobView, TrackedJob } from "./types";

afterEach(() => vi.unstubAllGlobals());

it("formatTimestamp matches the worker format", () => {
  expect(formatTimestamp(75)).toBe("01:15");
  expect(formatTimestamp(3725)).toBe("1:02:05");
});

const generic =
  "Something went wrong while making your notes. Please try again.";
it.each(
  "INVALID_JSON INVALID_URL MISSING_NOTION_TOKEN INVALID_NOTION_PARENT PAYLOAD_TOO_LARGE UNSUPPORTED_MEDIA_TYPE JOB_NOT_FOUND JOB_NOT_FINISHED UNAUTHORIZED INVALID_LIMIT JOB_NOT_DEAD_LETTERED INVALID_FAULTS INTERNAL VIDEO_TOO_LONG NO_TRANSCRIPT NOTION_UNAUTHORIZED NOTION_PARENT_NOT_FOUND NOTION_NO_DESTINATION LLM_CONFIG_ERROR RETRIES_EXHAUSTED RATE_LIMIT_RETRIES_EXHAUSTED DELIVERY_LIMIT_EXCEEDED FRAME_EXTRACTION_FAILED FRAME_NOT_RELEVANT VISUAL_DROPPED_INVALID FRAME_VERIFY_SKIPPED".split(
    " ",
  ),
)("errorMessage translates %s", (code) => {
  expect(errorMessage(code)).toMatch(/^[^_]+$/);
  expect(errorMessage(code)).not.toBe(generic);
});
it("errorMessage handles unknown codes", () =>
  expect(errorMessage("NEW_CODE")).toBe(generic));

it("summarizeWarnings includes the missing frame time and total", () => {
  expect(
    summarizeWarnings(
      [{ code: "FRAME_EXTRACTION_FAILED", timestamp_sec: 461 }],
      {
        notion_page_url: "",
        frames_requested: 6,
        frames_published: 5,
      },
    ),
  ).toBe("Done, but 1 of 6 screenshots couldn't be captured (at 07:41).");
});
it.each([
  ["llm", "AI service busy"],
  ["notion", "Notion busy"],
  ["youtube", "YouTube busy"],
])("waitingMessage for %s", (provider, label) => {
  const job = {
    status: "SUMMARIZING",
    waiting: {
      reason: "RATE_LIMITED",
      provider,
      next_attempt_at: new Date(28000).toISOString(),
    },
  } as JobView;
  expect(waitingMessage(job, 0)).toBe(
    `Summarizing — waiting (${label}), retrying in 28 s`,
  );
});

const client = new GatewayClient("http://localhost:8080/");
it("getResults maps 409 to NOT_FINISHED", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(new Response(null, { status: 409 })),
  );
  expect(await client.getResults("123")).toBe("NOT_FINISHED");
});
it("preserves error codes from the gateway", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        Response.json({ error: { code: "JOB_NOT_FOUND" } }, { status: 404 }),
      ),
  );
  const err = await client.getJob("123").catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err).toMatchObject({ code: "JOB_NOT_FOUND", status: 404 });
});
it("lists destinations with bearer authorization", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue(Response.json({ pages: [{ id: "1" }] }));
  vi.stubGlobal("fetch", fetcher);
  expect(await client.notionPages("secret")).toEqual([{ id: "1" }]);
  const [url, opts] = fetcher.mock.calls[0];
  expect(url).toBe("http://localhost:8080/notion/pages");
  expect(new Headers(opts.headers).get("Authorization")).toBe("Bearer secret");
});
it("upsertJob retains twenty newest jobs across overlapping writes", async () => {
  mockChromeStorage();
  const job = (i: number): TrackedJob => ({
    jobId: String(i),
    videoId: "v",
    title: null,
    last: null,
    results: null,
    submittedAt: i,
  });
  await Promise.all(Array.from({ length: 21 }, (_, i) => upsertJob(job(i))));
  await upsertJob({ ...job(1), title: "updated" });
  const jobs = await getJobs();
  expect(jobs.map((j) => j.submittedAt)).toEqual(
    Array.from({ length: 20 }, (_, i) => 20 - i),
  );
  expect(jobs.at(-1)?.title).toBe("updated");
});
it("preserves settings when changing individual fields", async () => {
  mockChromeStorage();
  expect((await getSettings()).gatewayUrl).toBe("http://localhost:8080");
  await saveSettings({ notionToken: "token" });
  await saveSettings({ workspaceName: "Notes" });
  expect(await getSettings()).toMatchObject({
    notionToken: "token",
    workspaceName: "Notes",
  });
});

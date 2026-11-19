import { expect, it, vi } from "vitest";
import { cancelJob } from "./jobs";
import { backgroundMock } from "../../test/background-mock";
import {
  getJobs,
  getSettings,
  saveSettings,
  upsertJob,
} from "../shared/storage";
import { pollOnce } from "./poller";

const job = {
  jobId: "001",
  videoId: "video",
  title: null,
  last: null,
  results: null,
  submittedAt: 0,
};

it("preserves a cancellation when a slow poll finishes and deduplicates cancel requests", async () => {
  const { fetcher, badge, notify } = backgroundMock();
  await upsertJob({ ...job, gatewayUrl: "http://original:8080" });
  let finishPoll!: (response: Response) => void;
  fetcher.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finishPoll = resolve;
      }),
  );
  const poll = pollOnce(0);
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  fetcher.mockResolvedValueOnce(
    Response.json({
      status: "CANCELLED",
      result: null,
      warnings: [],
      can_cancel: false,
    }),
  );
  await Promise.all([cancelJob(job.jobId), cancelJob(job.jobId)]);
  expect(fetcher.mock.calls[1][0]).toBe("http://original:8080/jobs/001/cancel");
  expect(fetcher.mock.calls[1][1]).toMatchObject({ method: "POST" });
  expect(fetcher.mock.calls[1][1]?.body).toBeUndefined();
  finishPoll(Response.json({ status: "TRANSCRIBING" }));
  await poll;
  await pollOnce(2000);
  expect((await getJobs())[0].results?.status).toBe("CANCELLED");
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(badge).toHaveBeenLastCalledWith({ text: "" });
  expect(notify).not.toHaveBeenCalled();
});

it("stops polling a cancellation observed on another client", async () => {
  const { fetcher, notify } = backgroundMock();
  await upsertJob(job);
  fetcher.mockResolvedValueOnce(
    Response.json({ status: "CANCELLED", result: null, warnings: [] }),
  );
  await pollOnce(0);
  await pollOnce(2000);
  expect((await getJobs())[0].results?.status).toBe("CANCELLED");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(notify).not.toHaveBeenCalled();
});

it("keeps a new connection when a job from an older connection fails", async () => {
  const { fetcher } = backgroundMock();
  await upsertJob({ ...job, notionConnectionId: "old" });
  await saveSettings({ notionToken: "reconnected", notionConnectionId: "new" });
  fetcher
    .mockResolvedValueOnce(
      Response.json({
        status: "FAILED",
        error: { code: "NOTION_UNAUTHORIZED" },
      }),
    )
    .mockResolvedValueOnce(
      Response.json({ status: "FAILED", result: null, warnings: [] }),
    );
  await pollOnce(0);
  expect((await getSettings()).notionToken).toBe("reconnected");
});
it("pollOnce saves progress, fetches terminal results once and notifies with the video title", async () => {
  const { fetcher, badge, notify } = backgroundMock();
  await upsertJob(job);
  fetcher
    .mockResolvedValueOnce(
      Response.json({ status: "TRANSCRIBING", video_title: "Lecture" }),
    )
    .mockResolvedValueOnce(
      Response.json({ status: "COMPLETED", video_title: "Lecture" }),
    )
    .mockResolvedValueOnce(
      Response.json({
        status: "COMPLETED",
        result: { notion_page_url: "https://www.notion.so/notes" },
        warnings: [],
      }),
    );
  await pollOnce(0);
  expect((await getJobs())[0].last?.status).toBe("TRANSCRIBING");
  expect(badge).toHaveBeenLastCalledWith({ text: "1" });
  await Promise.all([pollOnce(2000), pollOnce(2000)]);
  await pollOnce(4000);
  expect(fetcher).toHaveBeenCalledTimes(3);
  expect(notify).toHaveBeenCalledTimes(1);
  expect(notify.mock.calls[0][1]).toMatchObject({
    title: expect.stringContaining("Lecture"),
    buttons: [{ title: "View in Notion" }],
  });
  expect(badge).toHaveBeenLastCalledWith({ text: "" });
});
it("pollOnce clears revoked credentials even if fetching final results needs a retry", async () => {
  const { fetcher } = backgroundMock();
  await upsertJob(job);
  await saveSettings({ notionToken: "revoked", notionParentId: "parent" });
  fetcher
    .mockResolvedValueOnce(
      Response.json({
        status: "FAILED",
        error: { code: "NOTION_UNAUTHORIZED" },
      }),
    )
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce(
      Response.json({ status: "FAILED", result: null, warnings: [] }),
    );
  await pollOnce(0);
  expect((await getSettings()).notionToken).toBeNull();
  await saveSettings({ notionToken: "new" });
  await pollOnce(2000);
  expect((await getSettings()).notionToken).toBe("new");
  expect(
    fetcher.mock.calls.map(([url]) => String(url).split("/").pop()),
  ).toEqual(["001", "results", "results"]);
});

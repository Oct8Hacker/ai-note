import { expect, it } from "vitest";
import { backgroundMock } from "../../test/background-mock";
import { getJobs, saveSettings } from "../shared/storage";
import { takeNotes } from "./jobs";

it("takeNotes with OAuth disabled requests a connection and does not submit", async () => {
  const { fetcher } = backgroundMock();
  fetcher.mockResolvedValue(Response.json({ oauth_enabled: false }));
  expect(await takeNotes("video", "url")).toEqual({
    ok: false,
    message: "Connect Notion in the extension popup first",
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0][0]).toMatch(/\/config$/);
});
it("takeNotes uses a fresh idempotency key per click, saves each job and updates the badge", async () => {
  const { fetcher, badge } = backgroundMock();
  await saveSettings({ notionToken: "token", notionParentId: "parent" });
  for (const id of ["1", "2"])
    fetcher.mockResolvedValueOnce(
      Response.json({ job_id: id, video_id: "video", status: "QUEUED" }),
    );
  for (const id of ["1", "2"])
    expect(await takeNotes("video", "url")).toEqual({ ok: true, jobId: id });
  const keys = fetcher.mock.calls.map(([, opts]) =>
    new Headers(opts?.headers).get("Idempotency-Key"),
  );
  expect(keys[0]).toBeTruthy();
  expect(keys[0]).not.toBe(keys[1]);
  expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({
    youtube_url: "url",
    notion_token: "token",
    notion_parent_id: "parent",
  });
  expect(await getJobs()).toHaveLength(2);
  expect(badge).toHaveBeenLastCalledWith({ text: "2" });
});

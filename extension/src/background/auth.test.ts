import { expect, it } from "vitest";
import { backgroundMock } from "../../test/background-mock";
import { getSettings } from "../shared/storage";
import { connectWithOAuth, connectWithToken } from "./auth";

it("connectWithOAuth uses the extension redirect and saves the token and template parent", async () => {
  const { fetcher, redirect, flow } = backgroundMock();
  fetcher
    .mockResolvedValueOnce(
      Response.json({ oauth_enabled: true, client_id: "client" }),
    )
    .mockResolvedValueOnce(
      Response.json({
        access_token: "authorized",
        workspace_name: "Study",
        duplicated_template_id: "parent",
      }),
    );
  await connectWithOAuth();
  expect(chrome.identity.getRedirectURL).toHaveBeenCalledWith("notion");
  const url = new URL(flow.mock.calls[0][0].url);
  expect(url.searchParams.get("redirect_uri")).toBe(redirect);
  expect(url.searchParams.get("state")).toBeTruthy();
  expect(JSON.parse(String(fetcher.mock.calls[1][1]?.body))).toEqual({
    code: "code",
    redirect_uri: redirect,
  });
  expect(await getSettings()).toMatchObject({
    notionToken: "authorized",
    workspaceName: "Study",
    notionParentId: "parent",
  });
});
it("rejects an OAuth response with an unrelated state", async () => {
  const { fetcher, flow, redirect } = backgroundMock();
  fetcher.mockResolvedValue(
    Response.json({ oauth_enabled: true, client_id: "client" }),
  );
  flow.mockResolvedValue(redirect + "?code=code&state=other");
  await expect(connectWithOAuth()).rejects.toThrow();
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it("connectWithToken rejects an invalid token with a friendly message", async () => {
  const { fetcher } = backgroundMock();
  fetcher.mockResolvedValue(
    Response.json({ error: { code: "NOTION_UNAUTHORIZED" } }, { status: 401 }),
  );
  await expect(connectWithToken("invalid")).rejects.toThrow(
    /reconnect Notion/i,
  );
  expect((await getSettings()).notionToken).toBeNull();
});

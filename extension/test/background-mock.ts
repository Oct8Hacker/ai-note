import { vi } from "vitest";
import { mockChromeStorage } from "./chrome-mock";

export function backgroundMock() {
  mockChromeStorage();
  const redirect = "https://example.chromiumapp.org/notion";
  const flow = vi.fn(async ({ url }: { url: string }) => {
    const state = new URL(url).searchParams.get("state");
    return redirect + "?code=code&state=" + state;
  });
  const badge = vi.fn().mockResolvedValue(undefined);
  const notify = vi.fn().mockResolvedValue("id");
  Object.assign(chrome, {
    identity: {
      getRedirectURL: vi.fn(() => redirect),
      launchWebAuthFlow: flow,
    },
    action: { setBadgeText: badge },
    notifications: { create: notify },
    runtime: { getURL: (p: string) => "chrome-extension://test/" + p },
  });
  const fetcher = vi.fn<typeof fetch>();
  vi.stubGlobal("fetch", fetcher);
  return { fetcher, flow, badge, notify, redirect };
}

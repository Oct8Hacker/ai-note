// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  ensureButton,
  init,
  precheckDuration,
  videoIdFromLocation,
} from "./button";

const toolbar =
  '<div id="movie_player"><video></video><div class="ytp-right-controls"></div></div>';
const first = "dQw4w9WgXcQ";
const second = "jNQXAC9IVRw";
const send = vi.fn();
let stop: (() => void) | undefined;

function navigate(path: string) {
  history.pushState({}, "", path);
  document.dispatchEvent(new Event("yt-navigate-finish", { bubbles: true }));
}
function button() {
  return document.querySelector<HTMLButtonElement>("[data-ainote]")!;
}
beforeEach(() => {
  document.body.innerHTML = toolbar;
  navigate("/watch?v=" + first);
  send.mockReset().mockResolvedValue({ ok: true, jobId: "1" });
  vi.stubGlobal("chrome", { runtime: { sendMessage: send } });
});
afterEach(() => {
  stop?.();
  stop = undefined;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

it("ensureButton is idempotent and requires a toolbar", () => {
  const click = vi.fn();
  expect(ensureButton(document, click)).toBe(ensureButton(document, click));
  expect(document.querySelectorAll("[data-ainote]")).toHaveLength(1);
  button().click();
  expect(click).toHaveBeenCalledTimes(1);
  document.body.replaceChildren();
  expect(ensureButton(document, click)).toBeNull();
});
it.each([
  ["/watch?v=" + first + "&t=10", first],
  ["/shorts/x", null],
  ["/", null],
  ["/watch?v=invalid", null],
])("videoIdFromLocation %s", (path, id) => {
  navigate(path!);
  expect(videoIdFromLocation(location)).toBe(id);
});
it("reads the current ID at click time and removes the button off watch pages", () => {
  stop = init(window);
  navigate("/watch?v=" + second);
  // Change the URL again before YouTube finishes dispatching navigation events.
  history.replaceState({}, "", "/watch?v=" + first);
  button().click();
  expect(send).toHaveBeenCalledWith({
    type: "TAKE_NOTES",
    videoId: first,
    url: location.href,
  });
  navigate("/");
  expect(button()).toBeNull();
});
it("reattaches once when a toolbar or the entire player is replaced", async () => {
  stop = init(window);
  document.body.innerHTML = toolbar;
  await vi.waitFor(() => expect(button()).toBeTruthy());
  document.querySelector(".ytp-right-controls")!.replaceChildren();
  await vi.waitFor(() =>
    expect(document.querySelectorAll("[data-ainote]")).toHaveLength(1),
  );
});
it("precheckDuration rejects long videos inline and allows unknown duration", () => {
  expect(precheckDuration(document, 1200)).toBe("unknown");
  const video = document.querySelector("video")!;
  Object.defineProperty(video, "duration", { value: 1200, configurable: true });
  expect(precheckDuration(document, 1200)).toBe("ok");
  Object.defineProperty(video, "duration", { value: 1300 });
  expect(precheckDuration(document, 1200)).toBe("too_long");
  stop = init(window);
  button().click();
  expect(document.body.textContent).toContain(
    "Videos longer than 20 minutes aren't supported",
  );
  expect(send).not.toHaveBeenCalled();
});
it("debounces for two seconds and confirms only an acknowledged request", async () => {
  vi.useFakeTimers();
  stop = init(window);
  button().click();
  button().click();
  expect(send).toHaveBeenCalledTimes(1);
  expect(button().textContent).not.toBe("Queued ✓");
  await vi.advanceTimersByTimeAsync(0);
  expect(button().textContent).toBe("Queued ✓");
  await vi.advanceTimersByTimeAsync(2000);
  expect(button().textContent).toBe("Take Notes");
  button().click();
  expect(send).toHaveBeenCalledTimes(2);
});
it("ignores a late acknowledgment after navigating to another video", async () => {
  let ack!: (value: unknown) => void;
  send.mockReturnValueOnce(
    new Promise((resolve) => {
      ack = resolve;
    }),
  );
  stop = init(window);
  button().click();
  navigate("/watch?v=" + second);
  ack({ ok: true });
  await Promise.resolve();
  expect(button().textContent).toBe("Take Notes");
  button().click();
  expect(send).toHaveBeenLastCalledWith({
    type: "TAKE_NOTES",
    videoId: second,
    url: location.href,
  });
});

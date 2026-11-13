import {
  connectWithOAuth,
  connectWithToken,
  disconnect,
  friendlyError,
} from "./auth";
import { takeNotes } from "./jobs";
import { openJobResult, pollOnce, startFastPolling } from "./poller";

async function ensureAlarm() {
  if (!(await chrome.alarms.get("poll")))
    await chrome.alarms.create("poll", { periodInMinutes: 0.5 });
}
chrome.runtime.onInstalled.addListener(() => {
  void chrome.storage.local.setAccessLevel({ accessLevel: "TRUSTED_CONTEXTS" });
  void ensureAlarm();
});
chrome.runtime.onStartup.addListener(ensureAlarm);
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "poll") void pollOnce(Date.now());
});

chrome.runtime.onMessage.addListener((message, sender, reply) => {
  let action: Promise<unknown>;
  if (
    message?.type === "TAKE_NOTES" &&
    typeof message.videoId === "string" &&
    typeof message.url === "string"
  ) {
    action = takeNotes(message.videoId, message.url);
  } else {
    if (!sender.url?.startsWith(chrome.runtime.getURL(""))) return;
    switch (message?.type) {
      case "CONNECT_OAUTH":
        action = connectWithOAuth();
        break;
      case "CONNECT_TOKEN":
        if (typeof message.token !== "string") return;
        action = connectWithToken(message.token);
        break;
      case "DISCONNECT":
        action = disconnect();
        break;
      default:
        return;
    }
  }
  action.then(
    (result) => reply(result ?? { ok: true }),
    (err) => reply({ ok: false, message: friendlyError(err) }),
  );
  return true;
});

let popupCount = 0;
let stopPolling: (() => void) | undefined;
chrome.runtime.onConnect.addListener((port) => {
  if (
    port.name !== "popup" ||
    !port.sender?.url?.startsWith(chrome.runtime.getURL(""))
  )
    return;
  popupCount++;
  stopPolling ??= startFastPolling();
  port.onDisconnect.addListener(() => {
    if (--popupCount === 0) {
      stopPolling?.();
      stopPolling = undefined;
    }
  });
});
chrome.notifications.onButtonClicked.addListener((id, index) => {
  if (index === 0) void openJobResult(id);
});

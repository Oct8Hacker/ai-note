const selector = "button[data-ainote]";
const label = "Take Notes";
const debounceMs = 2000;

export function videoIdFromLocation(loc: Location): string | null {
  if (loc.pathname !== "/watch") return null;
  const id = new URLSearchParams(loc.search).get("v");
  return id && /^[A-Za-z0-9_-]{11}$/.test(id) ? id : null;
}

export function ensureButton(
  doc: Document,
  onClick: () => void,
): HTMLButtonElement | null {
  const toolbar =
    doc.querySelector("#movie_player .ytp-right-controls") ??
    doc.querySelector(".ytp-right-controls");
  if (!toolbar) return null;
  const existing = toolbar.querySelector<HTMLButtonElement>(selector);
  if (existing) return existing;
  const button = doc.createElement("button");
  button.type = "button";
  button.dataset.ainote = "";
  button.className = "ytp-button";
  button.textContent = label;
  button.setAttribute("aria-live", "polite");
  button.addEventListener("click", (event) => {
    event.stopPropagation();
    onClick();
  });
  toolbar.prepend(button);
  return button;
}

export function removeButton(doc: Document): void {
  doc.querySelectorAll(selector).forEach((button) => button.remove());
}

export function precheckDuration(
  doc: Document,
  maxSec: number,
): "ok" | "too_long" | "unknown" {
  const video =
    doc.querySelector<HTMLVideoElement>("#movie_player video") ??
    doc.querySelector("video");
  if (!video || !Number.isFinite(video.duration)) return "unknown";
  return video.duration > maxSec ? "too_long" : "ok";
}

// The player and its controls can both be replaced without a page reload.
export function init(win: Window): () => void {
  const doc = win.document;
  let player: Element | null = null;
  let currentId: string | null = null;
  let revision = 0;
  let lastClick = -Infinity;
  let resetTimer: number | undefined;
  let feedback = label;

  function show(text: string) {
    feedback = text;
    const button = doc.querySelector<HTMLButtonElement>(selector);
    if (button && button.textContent !== text) button.textContent = text;
  }

  async function click() {
    sync();
    const id = videoIdFromLocation(win.location);
    if (!id || Date.now() - lastClick < debounceMs) return;
    if (precheckDuration(doc, 1200) === "too_long") {
      show("Videos longer than 20 minutes aren't supported");
      return;
    }
    lastClick = Date.now();
    const request = ++revision;
    win.clearTimeout(resetTimer);
    let message: string;
    try {
      const reply = await chrome.runtime.sendMessage({
        type: "TAKE_NOTES",
        videoId: id,
        url: win.location.href,
      });
      message = reply?.ok
        ? "Queued ✓"
        : reply?.message || "Couldn't queue notes. Please try again.";
    } catch {
      message = "Couldn't queue notes. Please try again.";
    }
    if (request !== revision || videoIdFromLocation(win.location) !== id)
      return;
    show(message);
    resetTimer = win.setTimeout(() => show(label), debounceMs);
  }

  const controlsObserver = new MutationObserver(sync);
  const pageObserver = new MutationObserver(() => {
    if (!player || !player.isConnected) sync();
  });

  function sync() {
    const id = videoIdFromLocation(win.location);
    if (currentId !== id) {
      currentId = id;
      revision++;
      lastClick = -Infinity;
      win.clearTimeout(resetTimer);
      feedback = label;
    }
    const nextPlayer = doc.getElementById("movie_player");
    if (player !== nextPlayer) {
      controlsObserver.disconnect();
      player = nextPlayer;
      if (player)
        controlsObserver.observe(player, { childList: true, subtree: true });
    }
    if (!id) {
      removeButton(doc);
      return;
    }
    const button = ensureButton(doc, click);
    if (button && button.textContent !== feedback)
      button.textContent = feedback;
  }

  win.addEventListener("yt-navigate-finish", sync, true);
  pageObserver.observe(doc.documentElement, { childList: true, subtree: true });
  sync();
  return () => {
    revision++;
    win.clearTimeout(resetTimer);
    controlsObserver.disconnect();
    pageObserver.disconnect();
    win.removeEventListener("yt-navigate-finish", sync, true);
    removeButton(doc);
  };
}

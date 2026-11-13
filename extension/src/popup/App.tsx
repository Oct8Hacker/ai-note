import { useEffect, useState } from "preact/hooks";
import { GatewayClient } from "../shared/api";
import {
  getJobs,
  getSettings,
  type Settings as StoredSettings,
} from "../shared/storage";
import type { TrackedJob } from "../shared/types";
import { JobCard } from "./JobCard";
import { ConnectPanel } from "./ConnectPanel";
import { DestinationPicker } from "./DestinationPicker";
import { Settings } from "./Settings";

export function App() {
  const [tab, setTab] = useState<"Notes" | "Settings">("Notes");
  const [settings, setSettings] = useState<StoredSettings | null>(null);
  const [jobs, setJobs] = useState<TrackedJob[]>([]);
  const [now, setNow] = useState(Date.now());
  const [oauth, setOauth] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let revision = 0;
    async function refresh() {
      const version = ++revision;
      try {
        const [s, j] = await Promise.all([getSettings(), getJobs()]);
        if (version === revision) {
          setSettings(s);
          setJobs(j);
          setError("");
        }
      } catch {
        if (version === revision)
          setError(
            "Couldn’t read extension storage. Reopen the popup and try again.",
          );
      }
    }
    const changed = (_: unknown, area: string) => {
      if (area === "local") void refresh();
    };
    chrome.storage.onChanged.addListener(changed);
    void refresh();
    const port = chrome.runtime.connect({ name: "popup" });
    return () => {
      revision++;
      chrome.storage.onChanged.removeListener(changed);
      port.disconnect();
    };
  }, []);
  const gateway = settings?.gatewayUrl;
  useEffect(() => {
    let active = true;
    setOauth(false);
    if (gateway)
      new GatewayClient(gateway).notionConfig().then(
        (c) => {
          if (active) setOauth(c.oauth_enabled);
        },
        () => {},
      );
    return () => {
      active = false;
    };
  }, [gateway]);
  const waiting = jobs.some((j) => j.last?.waiting && !j.results);
  useEffect(() => {
    if (!waiting) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [waiting]);
  async function clearFinished() {
    try {
      await navigator.locks.request("ai-note:jobs", async () => {
        await chrome.storage.local.set({
          jobs: (await getJobs()).filter((j) => !j.results),
        });
      });
    } catch {
      setError("Couldn’t clear finished jobs.");
    }
  }
  return (
    <>
      <header>
        <span class="brandmark" aria-hidden="true">
          ▤
        </span>
        <h1>AI Note</h1>
      </header>
      <nav aria-label="Popup views">
        {(["Notes", "Settings"] as const).map((name) => (
          <button
            key={name}
            aria-current={tab === name ? "page" : undefined}
            onClick={() => setTab(name)}
          >
            {name}
          </button>
        ))}
      </nav>
      <main>
        {error && <p role="alert">{error}</p>}
        {!settings ? (
          <p>Loading…</p>
        ) : tab === "Notes" ? (
          <>
            <div class="heading">
              <h2>Recent jobs</h2>
              {jobs.some((j) => j.results) && (
                <button class="text" onClick={clearFinished}>
                  Clear finished jobs
                </button>
              )}
            </div>
            {jobs.length ? (
              jobs.map((job) => <JobCard key={job.jobId} job={job} now={now} />)
            ) : (
              <section class="empty">
                <h3>No jobs yet</h3>
                <p>
                  {settings.notionToken
                    ? "Open a YouTube video and select Take Notes in the player."
                    : "Connect Notion, then select Take Notes in the YouTube player."}
                </p>
                {!settings.notionToken && (
                  <button onClick={() => setTab("Settings")}>
                    Set up Notion
                  </button>
                )}
              </section>
            )}
          </>
        ) : (
          <>
            <ConnectPanel
              oauth={oauth}
              connected={!!settings.notionToken}
              workspace={settings.workspaceName}
            />
            {settings.notionToken && (
              <DestinationPicker
                gateway={settings.gatewayUrl}
                token={settings.notionToken}
                parent={settings.notionParentId}
              />
            )}
            <Settings gateway={settings.gatewayUrl} />
          </>
        )}
      </main>
    </>
  );
}

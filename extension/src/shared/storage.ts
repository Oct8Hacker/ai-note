import type { TrackedJob } from "./types";

export interface Settings {
  gatewayUrl: string;
  notionToken: string | null;
  notionConnectionId?: string | null;
  notionParentId: string | null;
  workspaceName: string | null;
}
const defaults: Settings = {
  gatewayUrl: "http://localhost:8080",
  notionToken: null,
  notionConnectionId: null,
  notionParentId: null,
  workspaceName: null,
};

// Store each setting separately so independent updates from the popup and worker cannot overwrite one another.
export async function getSettings(): Promise<Settings> {
  const stored = await chrome.storage.local.get(Object.keys(defaults));
  return { ...defaults, ...stored };
}
export async function saveSettings(p: Partial<Settings>): Promise<void> {
  await chrome.storage.local.set(p);
}

// Preserve the original gateway for jobs saved before per-job URLs were stored.
export async function changeGateway(next: string): Promise<void> {
  await navigator.locks.request("ai-note:jobs", async () => {
    const previous = (await getSettings()).gatewayUrl;
    if (next === previous) return;
    const jobs = (await getJobs()).map((job) => ({
      ...job,
      gatewayUrl: job.gatewayUrl ?? previous,
    }));
    const differentHost = new URL(previous).origin !== new URL(next).origin;
    await chrome.storage.local.set({
      gatewayUrl: next,
      jobs,
      ...(differentHost
        ? {
            notionToken: null,
            notionConnectionId: null,
            notionParentId: null,
            workspaceName: null,
          }
        : {}),
    });
  });
}
export async function getJobs(): Promise<TrackedJob[]> {
  const { jobs } = await chrome.storage.local.get("jobs");
  return jobs ?? [];
}
export async function upsertJob(job: TrackedJob): Promise<void> {
  await navigator.locks.request("ai-note:jobs", async () => {
    const jobs = (await getJobs()).filter((entry) => entry.jobId !== job.jobId);
    jobs.unshift(job);
    jobs.sort((a, b) => b.submittedAt - a.submittedAt);
    await chrome.storage.local.set({ jobs: jobs.slice(0, 20) });
  });
}

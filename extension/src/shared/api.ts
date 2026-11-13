import type {
  JobView,
  ResultsView,
  OAuthToken,
  NotionPage,
  Status,
} from "./types";

export class ApiError extends Error {
  constructor(
    readonly code: string,
    readonly status: number,
  ) {
    super(code);
    this.name = "ApiError";
  }
}

export class GatewayClient {
  private readonly base: string;
  constructor(baseUrl: string) {
    this.base = baseUrl.replace(/\/+$/, "");
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const response = await fetch(this.base + path, {
      ...init,
      redirect: "error",
      credentials: "omit",
      cache: "no-store",
    });
    if (!response.ok) {
      const body = await response.json().catch(() => null);
      throw new ApiError(
        typeof body?.error?.code === "string" ? body.error.code : "UNKNOWN",
        response.status,
      );
    }
    return response.json();
  }

  submitJob(
    youtubeUrl: string,
    notionToken: string,
    parentId: string | null,
    idempotencyKey: string,
  ) {
    return this.request<{ job_id: string; status: Status; video_id: string }>(
      "/jobs",
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": idempotencyKey,
        },
        body: JSON.stringify({
          youtube_url: youtubeUrl,
          notion_token: notionToken,
          notion_parent_id: parentId ?? undefined,
        }),
      },
    );
  }
  getJob(id: string) {
    return this.request<JobView>("/jobs/" + encodeURIComponent(id));
  }
  async getResults(id: string): Promise<ResultsView | "NOT_FINISHED"> {
    try {
      return await this.request<ResultsView>(
        "/jobs/" + encodeURIComponent(id) + "/results",
      );
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) return "NOT_FINISHED";
      throw err;
    }
  }
  notionConfig() {
    return this.request<{ oauth_enabled: boolean; client_id?: string }>(
      "/auth/notion/config",
    );
  }
  exchangeCode(code: string, redirectUri: string) {
    return this.request<OAuthToken>("/auth/notion/exchange", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code, redirect_uri: redirectUri }),
    });
  }
  async notionPages(token: string) {
    const { pages } = await this.request<{ pages: NotionPage[] }>(
      "/notion/pages",
      {
        headers: { Authorization: "Bearer " + token },
      },
    );
    return pages;
  }
}

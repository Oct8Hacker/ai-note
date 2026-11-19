export type Status =
  | "QUEUED"
  | "TRANSCRIBING"
  | "SUMMARIZING"
  | "EXTRACTING_FRAMES"
  | "PUBLISHING"
  | "COMPLETED"
  | "COMPLETED_WITH_WARNINGS"
  | "FAILED"
  | "CANCELLED"
  | "DEAD_LETTERED";
export interface Warning {
  code: string;
  timestamp_sec?: number;
}
export interface JobView {
  can_cancel: boolean;
  job_id: string;
  status: Status;
  progress: number;
  video_id: string;
  video_title: string | null;
  duration_sec: number | null;
  warnings: Warning[];
  error: { code: string; message: string } | null;
  result: Result | null;
  waiting: Waiting | null;
  attempt: number;
}
export interface Result {
  notion_page_url: string;
  frames_requested: number;
  frames_published: number;
}
export interface Waiting {
  reason: "RATE_LIMITED" | "RETRYING" | "BUSY";
  provider?: string;
  next_attempt_at: string;
}
export interface ResultsView {
  status: Status;
  result: Result | null;
  warnings: Warning[];
  error?: { code: string; message: string } | null;
}
export interface OAuthToken {
  access_token: string;
  workspace_name: string;
  workspace_icon?: string;
  bot_id: string;
  duplicated_template_id: string | null;
}
export interface NotionPage {
  id: string;
  title: string;
  type: "page" | "database";
  icon?: string;
}
export interface TrackedJob {
  gatewayUrl?: string;
  notionConnectionId?: string | null;
  jobId: string;
  videoId: string;
  title: string | null;
  last: JobView | null;
  results: ResultsView | null;
  submittedAt: number;
}

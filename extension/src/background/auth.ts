import { ApiError, GatewayClient } from "../shared/api";
import { apiErrorMessage } from "../shared/messages";
import { getSettings, saveSettings } from "../shared/storage";

export class ConnectionError extends Error {}
export function friendlyError(err: unknown): string {
  if (err instanceof ConnectionError) return err.message;
  if (err instanceof ApiError) return apiErrorMessage(err);
  return "Couldn't reach the gateway. Check the gateway URL and try again.";
}

async function authorize() {
  const { gatewayUrl } = await getSettings();
  const client = new GatewayClient(gatewayUrl);
  const config = await client.notionConfig();
  if (!config.oauth_enabled || !config.client_id)
    throw new ConnectionError("Connect Notion in the extension popup first");

  const redirect = chrome.identity.getRedirectURL("notion");
  const state = crypto.randomUUID();
  const authorizeUrl = new URL("https://api.notion.com/v1/oauth/authorize");
  authorizeUrl.search = new URLSearchParams({
    client_id: config.client_id,
    response_type: "code",
    owner: "user",
    redirect_uri: redirect,
    state,
  }).toString();
  let callback: string | undefined;
  try {
    callback = await chrome.identity.launchWebAuthFlow({
      url: authorizeUrl.href,
      interactive: true,
    });
  } catch {
    /* Closing the dialog also rejects the Chrome API promise. */
  }
  if (!callback)
    throw new ConnectionError(
      "Notion sign-in was cancelled. Try connecting again.",
    );
  const returned = new URL(callback);
  const code = returned.searchParams.get("code");
  if (
    returned.origin + returned.pathname !== redirect ||
    returned.searchParams.get("state") !== state ||
    !code
  )
    throw new ConnectionError(
      "Notion sign-in could not be verified. Try connecting again.",
    );
  const token = await client.exchangeCode(code, redirect);
  if (!token.access_token || (await getSettings()).gatewayUrl !== gatewayUrl)
    throw new ConnectionError("Reconnect Notion using the current gateway.");
  await navigator.locks.request("ai-note:connection", () =>
    saveSettings({
      notionConnectionId: crypto.randomUUID(),
      notionToken: token.access_token,
      workspaceName: token.workspace_name,
      notionParentId: token.duplicated_template_id,
    }),
  );
}

let connecting: Promise<void> | undefined;
export function connectWithOAuth(): Promise<void> {
  return (connecting ??= authorize().finally(() => {
    connecting = undefined;
  }));
}

export async function connectWithToken(input: string): Promise<void> {
  const token = input.trim();
  if (!token) throw new ConnectionError("Enter a Notion integration token.");
  const { gatewayUrl } = await getSettings();
  try {
    await new GatewayClient(gatewayUrl).notionPages(token);
  } catch (err) {
    throw new ConnectionError(friendlyError(err));
  }
  if ((await getSettings()).gatewayUrl !== gatewayUrl)
    throw new ConnectionError("Reconnect Notion using the current gateway.");
  await navigator.locks.request("ai-note:connection", () =>
    saveSettings({
      notionConnectionId: crypto.randomUUID(),
      notionToken: token,
      workspaceName: null,
      notionParentId: null,
    }),
  );
}

// A late failure for an older job must not revoke a newly connected workspace.
export function disconnect(expectedConnection?: string | null) {
  return navigator.locks.request("ai-note:connection", async () => {
    if (
      expectedConnection !== undefined &&
      (await getSettings()).notionConnectionId !== expectedConnection
    )
      return;
    await saveSettings({
      notionConnectionId: null,
      notionToken: null,
      workspaceName: null,
      notionParentId: null,
    });
  });
}

import { useState } from "preact/hooks";
export function ConnectPanel({
  oauth,
  connected,
  workspace,
}: {
  oauth: boolean;
  connected: boolean;
  workspace: string | null;
}) {
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  async function send(type: string) {
    setBusy(true);
    setError("");
    try {
      const reply = await chrome.runtime.sendMessage({
        type,
        ...(type === "CONNECT_TOKEN" ? { token: token.trim() } : {}),
      });
      if (!reply?.ok) {
        setError(reply?.message || "Couldn’t connect to Notion.");
        return;
      }
      setToken("");
      setEditing(false);
    } catch {
      setError("Couldn’t reach the extension. Reopen the popup and try again.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section class="panel">
      <h2>Notion connection</h2>
      {connected && (
        <>
          <p class="statusline">
            Connected{workspace ? " to " + workspace : ""}
          </p>
          <div class="actions">
            <button disabled={busy} onClick={() => setEditing(!editing)}>
              Reconnect
            </button>
            <button disabled={busy} onClick={() => send("DISCONNECT")}>
              Disconnect
            </button>
          </div>
        </>
      )}
      {(!connected || editing) && (
        <>
          {oauth && (
            <button
              class="primary"
              disabled={busy}
              onClick={() => send("CONNECT_OAUTH")}
            >
              Connect Notion
            </button>
          )}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void send("CONNECT_TOKEN");
            }}
          >
            <label>
              Integration token
              <input
                type="password"
                autoComplete="off"
                value={token}
                onInput={(e) => setToken(e.currentTarget.value)}
              />
            </label>
            <button disabled={busy || !token.trim()}>Connect with token</button>
          </form>
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

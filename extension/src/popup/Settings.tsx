import { useState } from "preact/hooks";
import { changeGateway } from "../shared/storage";
export function Settings({ gateway }: { gateway: string }) {
  const [draft, setDraft] = useState(gateway);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function save() {
    setError("");
    setMessage("");
    let url: URL;
    try {
      url = new URL(draft.trim());
      if (
        !["http:", "https:"].includes(url.protocol) ||
        url.username ||
        url.password ||
        url.search ||
        url.hash
      )
        throw Error();
    } catch {
      setError("Enter an HTTP or HTTPS gateway URL.");
      return;
    }
    const next = url.href.replace(/\/+$/, "");
    setBusy(true);
    try {
      if (
        url.origin !== "http://localhost:8080" &&
        !(await chrome.permissions.request({
          origins: [url.protocol + "//" + url.hostname + "/*"],
        }))
      ) {
        setError("Allow access to this gateway to save it.");
        return;
      }
      await changeGateway(next);
      setMessage("Gateway saved.");
    } catch {
      setError("Couldn’t save the gateway. Check the address and try again.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section class="panel">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <label>
          Gateway URL
          <input
            type="url"
            required
            value={draft}
            onInput={(e) => setDraft(e.currentTarget.value)}
          />
        </label>
        <button disabled={busy}>Save gateway</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
    </section>
  );
}

import { useEffect, useState } from "preact/hooks";
import { ApiError, GatewayClient } from "../shared/api";
import { apiErrorMessage } from "../shared/messages";
import { saveSettings } from "../shared/storage";
import type { NotionPage } from "../shared/types";
export function DestinationPicker({
  gateway,
  token,
  parent,
}: {
  gateway: string;
  token: string;
  parent: string | null;
}) {
  const [pages, setPages] = useState<NotionPage[] | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setPages(null);
    setError("");
    new GatewayClient(gateway).notionPages(token).then(
      (list) => {
        if (active) setPages(list);
      },
      (err) => {
        if (active)
          setError(
            err instanceof ApiError
              ? apiErrorMessage(err)
              : "Couldn’t load Notion destinations.",
          );
      },
    );
    return () => {
      active = false;
    };
  }, [gateway, token, retry]);
  const template = pages?.find(
    (p) => p.type === "page" && p.title === "AI Video Notes",
  )?.id;
  useEffect(() => {
    if (!parent && template)
      void saveSettings({ notionParentId: template }).catch(() =>
        setError("Couldn’t save the destination."),
      );
  }, [parent, template]);
  return (
    <section class="panel">
      <label>
        Notion destination
        <select
          value={parent ?? template ?? ""}
          disabled={!pages?.length}
          onChange={(e) => {
            void saveSettings({ notionParentId: e.currentTarget.value }).catch(
              () => setError("Couldn’t save the destination."),
            );
          }}
        >
          <option disabled value="">
            {pages
              ? "Choose a page or database"
              : error
                ? "Unavailable"
                : "Loading destinations…"}
          </option>
          {parent && pages && !pages.some((p) => p.id === parent) && (
            <option value={parent}>Saved destination</option>
          )}
          {pages?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.title || "Untitled"} · {p.type}
            </option>
          ))}
        </select>
      </label>
      {pages?.length === 0 && (
        <p>Share a Notion page or database with the integration.</p>
      )}
      {error && <p role="alert">{error}</p>}
      {(error || pages?.length === 0) && (
        <button onClick={() => setRetry(retry + 1)}>Reload destinations</button>
      )}
    </section>
  );
}

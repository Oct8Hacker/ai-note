// @vitest-environment jsdom
import { afterEach, it, expect, vi } from "vitest";
import {
  cleanup,
  render,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/preact";
import { ConnectPanel } from "./ConnectPanel";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it.each([true, false])(
  "offers OAuth when enabled=%s and always supports a token",
  async (enabled) => {
    const sendMessage = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal("chrome", { runtime: { sendMessage } });
    render(<ConnectPanel oauth={enabled} connected={false} workspace={null} />);
    expect(
      Boolean(screen.queryByRole("button", { name: "Connect Notion" })),
    ).toBe(enabled);
    fireEvent.input(screen.getByLabelText("Integration token"), {
      target: { value: "secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Connect with token" }));
    await waitFor(() =>
      expect(sendMessage).toHaveBeenCalledWith({
        type: "CONNECT_TOKEN",
        token: "secret",
      }),
    );
  },
);

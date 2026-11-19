// @vitest-environment jsdom
import { afterEach, it, expect, vi } from "vitest";
import {
  cleanup,
  render,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/preact";
import { DestinationPicker } from "./DestinationPicker";
import { Settings } from "./Settings";
import { mockChromeStorage } from "../../test/chrome-mock";
import { getSettings } from "../shared/storage";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("lists pages, defaults to the saved parent and persists a change", async () => {
  mockChromeStorage();
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      Response.json({
        pages: [
          { id: "1", title: "Notes", type: "page" },
          { id: "2", title: "Labs", type: "database" },
        ],
      }),
    ),
  );
  render(
    <DestinationPicker
      gateway="http://localhost:8080"
      token="secret"
      parent="2"
    />,
  );
  await screen.findByRole("option", { name: /Labs/ });
  const picker = screen.getByRole("combobox") as HTMLSelectElement;
  expect(picker.value).toBe("2");
  fireEvent.change(picker, { target: { value: "1" } });
  await waitFor(async () =>
    expect((await getSettings()).notionParentId).toBe("1"),
  );
});
it("requests host permission and saves the custom gateway only when granted", async () => {
  mockChromeStorage();
  const request = vi
    .fn()
    .mockResolvedValueOnce(false)
    .mockResolvedValueOnce(true);
  Object.assign(chrome, { permissions: { request } });
  render(<Settings gateway="http://localhost:8080" />);
  fireEvent.input(screen.getByLabelText("Gateway URL"), {
    target: { value: "https://notes.example" },
  });
  const save = screen.getByRole("button", { name: "Save gateway" });
  fireEvent.click(save);
  await screen.findByRole("alert");
  expect((await getSettings()).gatewayUrl).toBe("http://localhost:8080");
  fireEvent.click(save);
  await waitFor(async () =>
    expect((await getSettings()).gatewayUrl).toBe("https://notes.example"),
  );
  expect(request).toHaveBeenCalledWith({
    origins: ["https://notes.example/*"],
  });
});

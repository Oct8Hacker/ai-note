import { vi } from "vitest";

export function mockChromeStorage() {
  const values: Record<string, unknown> = {};
  let pending = Promise.resolve();
  vi.stubGlobal("navigator", {
    locks: {
      request(_name: string, fn: () => Promise<void>) {
        const done = pending.then(fn);
        pending = done.catch(() => {});
        return done;
      },
    },
  });
  vi.stubGlobal("chrome", {
    storage: {
      local: {
        async get(key: string | string[]) {
          const keys = Array.isArray(key) ? key : [key];
          return Object.fromEntries(
            keys
              .filter((k) => k in values)
              .map((k) => [k, structuredClone(values[k])]),
          );
        },
        async set(update: Record<string, unknown>) {
          Object.assign(values, structuredClone(update));
        },
      },
    },
  });
}

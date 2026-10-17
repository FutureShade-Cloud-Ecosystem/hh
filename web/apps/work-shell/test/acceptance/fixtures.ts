// One server per worker: the composed dist (or the assembled one WS_DIST
// names) behind the demo mint, on a port the system picks.
import { test as base, expect } from "@playwright/test";
import { rmSync } from "node:fs";
import type { Page } from "@playwright/test";
// @ts-expect-error plain module, no declaration file
import { composeDist } from "../support/compose.mjs";
// @ts-expect-error plain module, no declaration file
import { startServer } from "../support/server.mjs";

export const test = base.extend<{ shellUrl: string }, { served: { url: string } }>({
  served: [
    async ({}, use) => {
      const assembled = process.env["WS_DIST"];
      const dist: string = assembled !== undefined && assembled !== "" ? assembled : composeDist();
      const server = await startServer(dist, { holdApi: assembled !== undefined && assembled !== "" });
      await use({ url: server.url });
      await server.close();
      if (assembled === undefined || assembled === "") rmSync(dist, { recursive: true, force: true });
    },
    { scope: "worker", timeout: 300_000 },
  ],
  shellUrl: async ({ served }, use) => {
    await use(served.url);
  },
});

/**
 * Opt in to the stub FB Console for one page: the server marks a Console
 * session live, and the page's TOP window (never a frame: the desk apps must not
 * see a Tauri shell) gets an invoke that forwards the shell's auth commands to
 * the stub. The host then resumes that session at boot, as it does in the
 * Tauri shell. Returns the stub's recorded state.
 */
export async function useConsole(page: Page, shellUrl: string): Promise<() => Promise<{ live: boolean; signOuts: number; opened: number; calls: string[] }>> {
  const state = async (): Promise<{ live: boolean; signOuts: number; opened: number; calls: string[] }> => {
    const response = await fetch(`${shellUrl}/__console/state`);
    return (await response.json()) as { live: boolean; signOuts: number; opened: number; calls: string[] };
  };
  await fetch(`${shellUrl}/__console/open`, { method: "POST" });
  await page.addInitScript(() => {
    if (window.top !== window) return;
    (window as unknown as { __TAURI_INTERNALS__: unknown }).__TAURI_INTERNALS__ = {
      invoke: async (command: string) => {
        const response = await fetch(`/__console/${command}`, { method: "POST" });
        const body: unknown = await response.json();
        if (response.status >= 400) throw body;
        return body;
      },
    };
  });
  return state;
}

export { expect };

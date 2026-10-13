// One server per worker: the composed dist (or the assembled one WS_DIST
// names) behind the demo mint, on a port the system picks.
import { test as base, expect } from "@playwright/test";
import { rmSync } from "node:fs";
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

export { expect };

// tauri-csp.test.ts: the Tauri profiles' Content Security Policy must let the
// webview reach the core over HTTPS beside the shell's own ipc origins. A
// connect-src that allows only 'self' ipc: http://ipc.localhost strands every
// core call in a native shell, because the webview's own origin is the asset
// server, not the core. Run from anywhere:
//   bun test scripts/tauri-csp.test.ts
import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const REPO_ROOT = resolve(import.meta.dir, "..");

/** The profile's csp directive, split into its sources: "connect-src 'self' ipc:" becomes ["connect-src", "'self'", "ipc:"]. */
function directive(csp: string, name: string): string[] {
  const match = csp.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${name} `));
  return match === undefined ? [] : match.split(/\s+/);
}

function readCsp(shell: "hh-field" | "hh-work"): string {
  const conf = JSON.parse(readFileSync(resolve(REPO_ROOT, `tauri/${shell}/tauri.conf.json`), "utf8")) as {
    app?: { security?: { csp?: string } };
  };
  const csp = conf.app?.security?.csp;
  if (typeof csp !== "string" || csp === "") throw new Error(`tauri/${shell}/tauri.conf.json carries no csp`);
  return csp;
}

describe("tauri csp connect-src", () => {
  for (const shell of ["hh-field", "hh-work"] as const) {
    test(`${shell} keeps the ipc origins and permits the HTTPS core origins`, () => {
      const sources = directive(readCsp(shell), "connect-src");
      expect(sources[0]).toBe("connect-src");
      for (const source of ["'self'", "ipc:", "http://ipc.localhost"]) {
        expect(sources).toContain(source);
      }
      expect(sources).toContain("https://*.futurebuild.ai");
    });
  }
});

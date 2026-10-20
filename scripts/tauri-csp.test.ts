// tauri-csp.test.ts: the Tauri profiles' Content Security Policy holds no
// network origin for the webview. Sign in and sign out revocation run on the
// Rust side (the recorded design), so the only connect-src sources are the
// shell's own ipc origins; the Rust pin (tauri/hh-work/src/lib.rs,
// the_csp_connect_src_is_unchanged_by_sign_in) asserts the same and this test
// agrees with it. Run from anywhere:
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
    test(`${shell} keeps the ipc origins and admits no network origin`, () => {
      const csp = readCsp(shell);
      const sources = directive(csp, "connect-src");
      expect(sources[0]).toBe("connect-src");
      for (const source of ["'self'", "ipc:", "http://ipc.localhost"]) {
        expect(sources).toContain(source);
      }
      expect(sources.length).toBe(4);
      for (const source of sources.slice(1)) {
        expect(source.startsWith("https:")).toBe(false);
        expect(source).not.toContain("*");
      }
      expect(csp).not.toContain("*");
    });
  }
});

// Shared test data for the Work shell host's node suite: the real Work profile
// (tauri/core/profiles/work.json, generated from manifest.yaml) and the launch
// map the assembly script ships (tauri/hh-work/assemble-dist.sh LAUNCH_MAP),
// read from disk so a test cannot pass on a copy that drifted from either.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

const REPO = fileURLToPath(new URL("../../../../", import.meta.url));

export function readWorkProfile() {
  return JSON.parse(readFileSync(resolve(REPO, "tauri/core/profiles/work.json"), "utf8"));
}

/** { appId: servePath } for every LAUNCH_MAP line of the assembly script. */
export function readLaunchMap() {
  const script = readFileSync(resolve(REPO, "tauri/hh-work/assemble-dist.sh"), "utf8");
  const block = script.match(/LAUNCH_MAP=\(\n([\s\S]*?)\n\)/);
  if (!block) throw new Error("LAUNCH_MAP not found in assemble-dist.sh");
  const launch = {};
  for (const m of block[1].matchAll(/"([a-z-]+) ([a-z0-9-]+) (\/[a-z0-9-]+\/)"/g)) launch[m[1]] = m[3];
  return launch;
}

/** A storage double with the map's own semantics, probed like the real thing. */
export function fakeStorage() {
  const mem = new Map();
  return {
    getItem: (key) => (mem.has(key) ? mem.get(key) : null),
    setItem: (key, value) => {
      mem.set(key, value);
    },
    removeItem: (key) => {
      mem.delete(key);
    },
    keys: () => [...mem.keys()],
  };
}

export const NOW = 1_700_000_000_000;

/** A JWT shaped token whose exp claim is an hour past NOW. */
export function fakeToken(sub = "demo-account-sales") {
  const exp = Math.floor(NOW / 1000) + 3600;
  return [
    Buffer.from('{"alg":"HS256","typ":"JWT"}').toString("base64url"),
    Buffer.from(JSON.stringify({ sub, exp })).toString("base64url"),
    "stub-signature",
  ].join(".");
}

/** The front door's stored record, field for field (web/apps/front-door/src/session.ts FrontDoorSession). */
export function doorRecord(overrides = {}) {
  return {
    token: fakeToken(),
    tokenExpiresAtMs: NOW + 3_600_000,
    flow: "sales",
    roleLabel: "Sales",
    user: { userId: "demo-account-sales", email: "sales@demo.invalid", name: "Sales (demo)" },
    roleCode: "inside_sales",
    roleName: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
    ...overrides,
  };
}

// field-profiles.test.ts: the checked in profile files under tauri/core/profiles
// must not be able to disagree with manifest.yaml (the same rule the manifest
// itself is under, scripts/manifest-catalog.test.ts). Run from anywhere:
//   bun test scripts/field-profiles.test.ts
// The generated files are written by scripts/generate-field-profiles.ts; this
// test is the drift gate CI collects with the repo's other bun tests.

import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import {
  buildDoc,
  loadManifest,
  PROFILE_FILES,
  serialize,
  type GeneratedDoc,
} from "./generate-field-profiles";

const manifest = await loadManifest();

function readProfile(shell: keyof typeof PROFILE_FILES): GeneratedDoc {
  return JSON.parse(readFileSync(PROFILE_FILES[shell], "utf8")) as GeneratedDoc;
}

describe("generated field profile", () => {
  test("the checked in file equals a fresh build of manifest.yaml", () => {
    const fresh = serialize(buildDoc(manifest, "field"));
    const current = readFileSync(PROFILE_FILES.field, "utf8");
    expect(current).toBe(fresh);
  });

  test("field holds exactly the manifest's hh-field apps, manifest order", () => {
    const doc = readProfile("field");
    expect(doc.shell).toBe("hh-field");
    expect(doc.apps.map((a) => a.id)).toEqual(["yard-pick", "drive-run", "house-messages"]);
  });

  test("every field app carries a known offline mode and closed policy set", () => {
    const doc = readProfile("field");
    const modes = ["full", "read_cache_plus_outbox", "read_cache_only", "none"];
    const policies = ["server_wins", "last_write_wins_by_field", "manual"];
    for (const app of doc.apps) {
      const offline = app.offline as { mode: string; scopes: string[]; conflict_policy: Record<string, string> };
      expect(modes).toContain(offline.mode);
      expect(offline.scopes.length).toBeGreaterThan(0);
      for (const policy of Object.values(offline.conflict_policy)) {
        expect(policies).toContain(policy);
      }
    }
  });

  test("pinned offline shapes from the plan (plan B.3)", () => {
    const doc = readProfile("field");
    const byId = new Map(doc.apps.map((a) => [a.id, a]));
    const yard = byId.get("yard-pick")!.offline as { scopes: string[]; conflict_policy: Record<string, string> };
    expect(yard.scopes).toContain("pick_tasks");
    expect(yard.conflict_policy["pick_tasks"]).toBe("server_wins");
    expect(yard.conflict_policy["shortages"]).toBe("manual");
    const drive = byId.get("drive-run")!.offline as { scopes: string[]; conflict_policy: Record<string, string> };
    expect(drive.conflict_policy["stops"]).toBe("server_wins");
    expect(drive.conflict_policy["pod"]).toBe("manual");
  });

  test("one brand across the shell's apps, read from the manifest", () => {
    const doc = readProfile("field");
    expect(doc.brand).toBe("fb-tokens");
  });
});

describe("declared skeleton profiles", () => {
  test("work and pro equal a fresh build and carry id and name only", () => {
    for (const shell of ["work", "pro"] as const) {
      const fresh = serialize(buildDoc(manifest, shell));
      const current = readFileSync(PROFILE_FILES[shell], "utf8");
      expect(current).toBe(fresh);
      const doc = JSON.parse(current) as GeneratedDoc;
      expect(doc.apps.length).toBeGreaterThan(0);
      for (const app of doc.apps) {
        expect(Object.keys(app).sort()).toEqual(["id", "name"]);
      }
    }
  });

  test("the skeletons hold the manifest's own app sets", () => {
    const work = readProfile("work");
    expect(work.shell).toBe("hh-work");
    expect(work.apps.map((a) => a.id)).toContain("counter-till");
    expect(work.apps.map((a) => a.id)).toContain("money-desk");
    const pro = readProfile("pro");
    expect(pro.shell).toBe("hh-pro");
    expect(pro.apps.map((a) => a.id)).toEqual(["pro-procurement", "pro-pay", "pro-field"]);
  });
});

// field-profiles.test.ts: the checked in profile files under tauri/core/profiles
// must not be able to disagree with manifest.yaml (the same rule the manifest
// itself is under, scripts/manifest-catalog.test.ts). Run from anywhere:
//   bun test scripts/field-profiles.test.ts
// The generated files are written by scripts/generate-field-profiles.ts; this
// test is the drift gate CI collects with the repo's other bun tests.

import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  buildDoc,
  loadManifest,
  PROFILE_FILES,
  REPO_ROOT,
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
  test("work and pro equal a fresh build and carry id and name only", () => {    for (const shell of ["work", "pro"] as const) {
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

describe("field launcher paths", () => {
  // The assembly script (tauri/hh-field/assemble-dist.sh) ships each Field
  // app's web bundle inside the profile at the serve path the demo box uses
  // (deploy/demo/Caddyfile.template) and the front door's role tiles open.
  // This section pins the three together: a launch map entry must name a
  // manifest app of the field shell, an existing web app, and a path the
  // front door's own tile table carries, so a rename on any side turns this
  // test red instead of shipping a dead tile inside the shell.
  function launchEntries(): { app: string; dir: string; path: string }[] {
    const script = readFileSync(resolve(REPO_ROOT, "tauri/hh-field/assemble-dist.sh"), "utf8");
    const block = script.match(/LAUNCH_MAP=\(\n([\s\S]*?)\n\)/);
    if (!block) return [];
    return [...block[1].matchAll(/"([a-z-]+) ([a-z0-9-]+) (\/[a-z0-9-]+\/)"/g)].map((m) => ({
      app: m[1],
      dir: m[2],
      path: m[3],
    }));
  }

  test("the launch map names every field app that has a web bundle", () => {
    const fieldApps = readProfile("field").apps.map((a) => a.id);
    const withBundle = fieldApps.filter((id) => existsSync(resolve(REPO_ROOT, "web/apps", id)));
    expect(launchEntries().map((e) => e.app)).toEqual(withBundle);
  });

  test("each launch path is the front door's own tile href for that app", () => {
    const roles = readFileSync(resolve(REPO_ROOT, "web/apps/front-door/src/roles.ts"), "utf8");
    for (const entry of launchEntries()) {
      expect(roles).toContain(`href: "${entry.path}"`);
    }
  });
});

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

describe("generated work profile", () => {
  test("the checked in file equals a fresh build of manifest.yaml", () => {
    const fresh = serialize(buildDoc(manifest, "work"));
    const current = readFileSync(PROFILE_FILES.work, "utf8");
    expect(current).toBe(fresh);
  });

  test("work holds exactly the manifest's hh-work apps, manifest order", () => {
    const doc = readProfile("work");
    expect(doc.shell).toBe("hh-work");
    expect(doc.apps.map((a) => a.id)).toEqual([
      "counter-till",
      "quote-desk",
      "dispatch-board",
      "buying-and-stock",
      "money-desk",
      "manager-home",
      "house-messages",
      "dealer-admin",
    ]);
  });

  test("every work app carries a known offline mode and closed policy set", () => {
    const doc = readProfile("work");
    const modes = ["full", "read_cache_plus_outbox", "read_cache_only", "none"];
    const policies = ["server_wins", "last_write_wins_by_field", "manual"];
    for (const app of doc.apps) {
      const offline = app.offline as { mode: string; scopes: string[]; conflict_policy: Record<string, string> };
      expect(modes).toContain(offline.mode);
      for (const policy of Object.values(offline.conflict_policy)) {
        expect(policies).toContain(policy);
      }
    }
  });

  test("pinned offline shapes from the plan (plan B.3 and B.4)", () => {
    const doc = readProfile("work");
    const byId = new Map(doc.apps.map((a) => [a.id, a]));
    type Offline = { mode: string; scopes: string[]; conflict_policy: Record<string, string> };
    const offline = (id: string) => byId.get(id)!.offline as Offline;
    // The till has no outbox (D5); the money desk and dealer admin are online only.
    expect(offline("counter-till").mode).toBe("read_cache_only");
    expect(offline("money-desk").mode).toBe("none");
    expect(offline("money-desk").scopes).toEqual([]);
    expect(offline("dealer-admin").mode).toBe("none");
    // Quote drafts carry an outbox, last write wins by field.
    expect(offline("quote-desk").mode).toBe("read_cache_plus_outbox");
    expect(offline("quote-desk").conflict_policy["quotes"]).toBe("last_write_wins_by_field");
    expect(offline("buying-and-stock").conflict_policy["cycle_counts"]).toBe("manual");
    expect(offline("manager-home").conflict_policy["approvals"]).toBe("server_wins");
  });

  test("no money scope queues: no work app with an outbox lists a money scope", () => {
    const doc = readProfile("work");
    const money = ["tenders", "payments", "invoices", "ledger", "receivables", "payables"];
    for (const app of doc.apps) {
      const offline = app.offline as { mode: string; scopes: string[] };
      if (offline.mode === "read_cache_plus_outbox" || offline.mode === "full") {
        for (const scope of offline.scopes) expect(money).not.toContain(scope);
      }
    }
  });

  test("one brand across the shell's apps, read from the manifest", () => {
    expect(readProfile("work").brand).toBe("fb-tokens");
  });
});

describe("declared skeleton profiles", () => {
  test("pro equals a fresh build and carries id and name only", () => {
    const fresh = serialize(buildDoc(manifest, "pro"));
    const current = readFileSync(PROFILE_FILES.pro, "utf8");
    expect(current).toBe(fresh);
    const doc = JSON.parse(current) as GeneratedDoc;
    expect(doc.apps.length).toBeGreaterThan(0);
    for (const app of doc.apps) {
      expect(Object.keys(app).sort()).toEqual(["id", "name"]);
    }
  });

  test("the pro skeleton holds the manifest's own app set", () => {
    const pro = readProfile("pro");
    expect(pro.shell).toBe("hh-pro");
    expect(pro.apps.map((a) => a.id)).toEqual(["pro-procurement", "pro-pay", "pro-field"]);
  });
});

describe.each(["field", "work"] as const)("%s launcher paths", (shell) => {
  // The assembly script (tauri/hh-<shell>/assemble-dist.sh) ships each app's
  // web bundle inside the profile at the serve path the demo box uses
  // (deploy/demo/Caddyfile.template) and the front door's role tiles open,
  // built with the flags of the demo image (deploy/demo/Dockerfile). This
  // section pins the three together: a launch map entry must name a manifest
  // app of the shell, an existing web app, a path the front door's own tile
  // table carries, and the Dockerfile's own build line for that app, so a
  // rename on any side turns this test red instead of shipping a dead tile
  // inside the shell.
  const scriptPath = resolve(REPO_ROOT, `tauri/hh-${shell}/assemble-dist.sh`);

  function launchEntries(): { app: string; dir: string; path: string }[] {
    const script = readFileSync(scriptPath, "utf8");
    const block = script.match(/LAUNCH_MAP=\(\n([\s\S]*?)\n\)/);
    if (!block) return [];
    return [...block[1].matchAll(/"([a-z-]+) ([a-z0-9-]+) (\/[a-z0-9-]+\/)"/g)].map((m) => ({
      app: m[1],
      dir: m[2],
      path: m[3],
    }));
  }

  test("the launch map names every app of the shell that has a web bundle", () => {
    const apps = readProfile(shell).apps.map((a) => a.id);
    const withBundle = apps.filter((id) => existsSync(resolve(REPO_ROOT, "web/apps", id)));
    expect(launchEntries().map((e) => e.app)).toEqual(withBundle);
  });

  test("each launch path is the front door's own tile href for that app", () => {
    const roles = readFileSync(resolve(REPO_ROOT, "web/apps/front-door/src/roles.ts"), "utf8");
    for (const entry of launchEntries()) {
      expect(roles).toContain(`href: "${entry.path}"`);
    }
  });

  test("each launch path and web dir is the demo image's own build line", () => {
    const docker = readFileSync(resolve(REPO_ROOT, "deploy/demo/Dockerfile"), "utf8");
    for (const entry of launchEntries()) {
      const dir = entry.dir.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      // Most apps have their own WORKDIR line; the Quote Desk builds inside
      // the image's conditional (it is absent from some build contexts).
      const line = new RegExp(
        `(WORKDIR /src/web/apps/${dir}\\nRUN npx vite build|cd apps/${dir} && npx vite build) --mode demo --base=${entry.path} `,
      );
      expect(docker).toMatch(line);
    }
  });

  test("the script builds each app the way the image does and ships the profile data", () => {
    const script = readFileSync(scriptPath, "utf8");
    // Staff apps build in demo mode, with their own base, into the dist.
    expect(script).toContain("--mode demo --base=");
    // Field: the front door is the entry at the root. Work: the host
    // (web/apps/work-shell) is the entry and the front door a page under it,
    // built like every other app (see "work shell host assembly" below).
    expect(script).toContain(shell === "field" ? "web/apps/front-door" : "build_app front-door /door/");
    expect(script).toContain(`tauri/core/profiles/${shell}.json`);
    expect(script).toContain('"$DIST/profile.json"');
    expect(script).toContain(`tauri/hh-${shell}/dist`);
    // Never builds a bundle of another shell's apps into this profile.
    const other = shell === "field" ? "work" : "field";
    expect(script).not.toContain(`hh-${other}`);
  });
});

describe("work launcher entries", () => {
  test("the desk apps the Work profile ships are exactly the built ones", () => {
    const script = readFileSync(resolve(REPO_ROOT, "tauri/hh-work/assemble-dist.sh"), "utf8");
    const ids = [...script.matchAll(/^  "([a-z-]+) [a-z0-9-]+ \/[a-z0-9-]+\/"$/gm)].map((m) => m[1]);
    expect(ids).toEqual([
      "counter-till",
      "quote-desk",
      "dispatch-board",
      "buying-and-stock",
      "money-desk",
      "manager-home",
      "dealer-admin",
    ]);
  });
});

describe("work shell host assembly", () => {
  // The Work profile opens a host page (web/apps/work-shell) at the dist root;
  // the front door is a page under it at /door/ (the host mounts it as its
  // sign in and role picker). Field keeps the front door at its root.
  const work = () => readFileSync(resolve(REPO_ROOT, "tauri/hh-work/assemble-dist.sh"), "utf8");

  test("the host is built into the dist root and is the launcher entry", () => {
    const script = work();
    expect(script).toContain('npm --prefix "$ROOT/web/apps/work-shell" run build');
    expect(script).toContain('cp -r "$ROOT/web/apps/work-shell/dist/." "$DIST/"');
  });

  test("the front door is built at /door/ and is not copied to the dist root", () => {
    const script = work();
    expect(script).toContain('build_app front-door /door/ "$DIST/door/"');
    expect(script).not.toContain('cp -r "$ROOT/web/apps/front-door/dist/." "$DIST/"');
  });

  test("the front door page keeps its own launch paths: its tiles are the same hrefs the launch map ships", () => {
    const roles = readFileSync(resolve(REPO_ROOT, "web/apps/front-door/src/roles.ts"), "utf8");
    for (const href of ["/till/", "/quote/", "/dispatch/", "/buying-and-stock/", "/home/", "/dealer-admin/", "/money-desk/"]) {
      expect(roles).toContain(`href: "${href}"`);
    }
  });

  test("a launch.json is written from the LAUNCH_MAP, beside profile.json", () => {
    const script = work();
    expect(script).toContain('"$DIST/launch.json"');
    expect(script).toContain('"$DIST/profile.json"');
  });

  test("the host's web app is a workspace app named in the Work scope", () => {
    const pkg = JSON.parse(readFileSync(resolve(REPO_ROOT, "web/apps/work-shell/package.json"), "utf8"));
    expect(pkg.name).toBe("@hardscapehouse/work-shell");
  });

  test("Field keeps the front door at its dist root", () => {
    const script = readFileSync(resolve(REPO_ROOT, "tauri/hh-field/assemble-dist.sh"), "utf8");
    expect(script).toContain('cp -r "$ROOT/web/apps/front-door/dist/." "$DIST/"');
    expect(script).not.toContain("work-shell");
  });

  test("the Work README names the host as the entry and the front door as a page under it", () => {
    const readme = readFileSync(resolve(REPO_ROOT, "tauri/hh-work/README.md"), "utf8");
    expect(readme).toContain("web/apps/work-shell");
    expect(readme).toContain("/door/");
    expect(readme).toContain("shell-proof");
  });
});

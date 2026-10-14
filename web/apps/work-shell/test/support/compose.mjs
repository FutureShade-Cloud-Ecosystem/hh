// Composes the directory the acceptance legs serve, the way the Work profile's
// assemble-dist.sh lays it out, without building the eight desk apps: the host
// at the root, the front door built at /door/, profile.json and launch.json,
// and one fixture page per desk app at the app's serve path. The fixture pages
// do what the real apps do at the handoff (read the front door's key once and
// DROP it) and what a real app does for Switch role (assign the origin root),
// so the legs prove the host's side of both. WS_DIST names an already
// assembled dist instead (the real bundles); nothing is composed then.
import { execFileSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL("../..", import.meta.url));
const repo = resolve(appDir, "../../..");

/** { appId: servePath } from the assembly script's LAUNCH_MAP, the one source. */
export function readLaunchMap() {
  const script = readFileSync(resolve(repo, "tauri/hh-work/assemble-dist.sh"), "utf8");
  const block = script.match(/LAUNCH_MAP=\(\n([\s\S]*?)\n\)/);
  if (!block) throw new Error("LAUNCH_MAP not found in assemble-dist.sh");
  const launch = {};
  for (const m of block[1].matchAll(/"([a-z-]+) ([a-z0-9-]+) (\/[a-z0-9-]+\/)"/g)) launch[m[1]] = m[3];
  return launch;
}

function vite(cwd, args) {
  execFileSync("npx", ["--no-install", "vite", "build", ...args], { cwd, stdio: "pipe" });
}

const FIXTURE_APP = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Fixture desk app</title></head>
<body>
<p data-testid="fixture"></p>
<button type="button" data-testid="fixture-switch-role">Switch role</button>
<script>
  var KEY = "hh-demo.front-door.session.v1";
  var raw = sessionStorage.getItem(KEY);
  var role = "none";
  if (raw) {
    try { role = JSON.parse(raw).roleCode; } catch (e) { role = "corrupt"; }
    sessionStorage.removeItem(KEY);
  }
  document.querySelector("[data-testid=fixture]").textContent = "fixture:" + location.pathname + ":" + role;
  document.querySelector("button").addEventListener("click", function () { location.assign("/"); });
</script>
</body></html>
`;

/** Build and lay out the served directory; returns its path. A failed build removes what it made. */
export function composeDist() {
  const out = mkdtempSync(join(tmpdir(), "ws-dist-"));
  try {
    vite(appDir, ["--outDir", out, "--emptyOutDir"]);
    vite(resolve(repo, "web/apps/front-door"), ["--mode", "demo", "--base=/door/", "--outDir", join(out, "door"), "--emptyOutDir"]);
    cpSync(resolve(repo, "tauri/core/profiles/work.json"), join(out, "profile.json"));
    const launch = readLaunchMap();
    writeFileSync(join(out, "launch.json"), JSON.stringify(launch));
    for (const path of Object.values(launch)) {
      mkdirSync(join(out, path), { recursive: true });
      writeFileSync(join(out, path, "index.html"), FIXTURE_APP);
    }
    return out;
  } catch (error) {
    rmSync(out, { recursive: true, force: true });
    throw error;
  }
}

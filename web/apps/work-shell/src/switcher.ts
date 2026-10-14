// The app switcher by role. The Work profile (profile.json, generated from
// manifest.yaml) names the apps and the roles that hold them; launch.json
// (written by assemble-dist.sh from its LAUNCH_MAP) names the apps that have
// a bundle in the profile and the path each one is served at. The switcher
// offers an app only when it has a bundle and the signed in role holds it in
// the profile; the server's gates stay the boundary, this only decides what
// to offer. The filter itself is the shell chrome's own `appsForRoles`.
import { appsForRoles, type ManifestFrontend } from "@hardscapehouse/shell-chrome/src/shell-state.js";

/** A Work app the host can open: the manifest slice plus where its bundle is served. */
export interface WorkApp extends ManifestFrontend {
  readonly path: string;
}

/** The profile apps that have a bundle, in manifest order, each with its serve path. */
export function launchableApps(
  profileApps: readonly ManifestFrontend[],
  launch: Readonly<Record<string, string>>,
): WorkApp[] {
  const apps: WorkApp[] = [];
  for (const app of profileApps) {
    if (Object.hasOwn(launch, app.id)) apps.push({ ...app, path: launch[app.id] as string });
  }
  return apps;
}

/** What the signed in role is offered, in the profile's order. */
export function switcherFor(apps: readonly WorkApp[], roleCode: string): WorkApp[] {
  if (roleCode === "") return [];
  return appsForRoles(apps, [roleCode]) as WorkApp[];
}

// A launch path is a directory served from the shell's own origin: absolute,
// one or more plain segments, trailing slash. Anything else (a scheme, a
// protocol relative host, a dot segment) refuses the whole file: the host
// never opens a remote origin.
const SERVE_PATH = /^\/(?:[a-z0-9][a-z0-9-]*\/)+$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/**
 * The two shipped files, read field by field and never cast. Null when either
 * is unusable; the host then says the profile could not be read.
 */
export function parseProfile(profile: unknown, launch: unknown): { apps: WorkApp[] } | null {
  if (!isRecord(profile) || !Array.isArray(profile["apps"]) || !isRecord(launch)) return null;
  const frontends: ManifestFrontend[] = [];
  for (const entry of profile["apps"] as unknown[]) {
    if (!isRecord(entry)) return null;
    const { id, name, roles } = entry;
    if (typeof id !== "string" || id === "" || typeof name !== "string" || name === "") return null;
    if (!Array.isArray(roles) || !roles.every((r) => typeof r === "string")) return null;
    frontends.push({ id, name, roles: roles as string[] });
  }
  const paths: Record<string, string> = {};
  for (const [id, path] of Object.entries(launch)) {
    if (typeof path !== "string" || !SERVE_PATH.test(path)) return null;
    paths[id] = path;
  }
  return { apps: launchableApps(frontends, paths) };
}

#!/usr/bin/env bun
// generate-manifest.ts: builds manifest.yaml from the role catalog so the two
// cannot drift. The catalog is the source of truth for the frontends block;
// this script copies audience, roles, devices, kit, shell, brand and the
// offline block (mode, scopes, per-entity conflict policy) verbatim and never
// invents a scope.
//
// Usage:
//   bun scripts/generate-manifest.ts                 write manifest.yaml
//   bun scripts/generate-manifest.ts --check         exit 1 if manifest.yaml differs from a fresh build
//   bun scripts/generate-manifest.ts --stdout        print instead of writing
//   bun scripts/generate-manifest.ts [flags] <path>  read the catalog from <path>
//
// Catalog path resolution: positional argument, then the HH_ROLE_CATALOG
// environment variable, then docs/analysis/role-catalog.yaml (the copy HH-2.5
// moves into this repository).

import { YAML } from "bun";
import { resolve } from "node:path";

export const REPO_ROOT = resolve(import.meta.dir, "..");
export const MANIFEST_PATH = resolve(REPO_ROOT, "manifest.yaml");
export const DEFAULT_CATALOG_PATH = resolve(REPO_ROOT, "docs/analysis/role-catalog.yaml");

export interface OfflineBlock {
  mode: string;
  scopes: string[];
  conflict_policy: Record<string, string>;
  note?: string;
}

export interface MicroApp {
  id: string;
  name: string;
  audience: string;
  roles: string[];
  devices: string[];
  kit: string;
  brand: string;
  shell: string | string[];
  offline: OfflineBlock;
}

export interface CatalogDoc {
  catalog: number;
  micro_apps: MicroApp[];
  launcher_sections?: { id: string }[];
}

export function catalogPath(argv: string[], env: NodeJS.ProcessEnv): string {
  const positional = argv.find((a) => !a.startsWith("--"));
  if (positional) return resolve(positional);
  if (env.HH_ROLE_CATALOG) return resolve(env.HH_ROLE_CATALOG);
  return DEFAULT_CATALOG_PATH;
}

export async function loadCatalog(path: string): Promise<CatalogDoc> {
  const file = Bun.file(path);
  if (!(await file.exists())) {
    throw new Error(
      `role catalog not found at ${path}; pass a path or set HH_ROLE_CATALOG`,
    );
  }
  const doc = YAML.parse(await file.text()) as CatalogDoc;
  if (!Array.isArray(doc.micro_apps)) {
    throw new Error(`${path}: no micro_apps list found`);
  }
  return doc;
}

function needsQuote(s: string): boolean {
  if (s === "" || s.trim() !== s) return true;
  if (/^(true|false|null|~|yes|no|on|off)$/i.test(s)) return true;
  if (/^-?(\d|\.\d)/.test(s)) return true;
  return /[:#[\]{},&*!|>'"%@`?]/.test(s);
}

function scalar(v: string): string {
  return needsQuote(v) ? JSON.stringify(v) : v;
}

function flowList(items: string[]): string {
  return items.length === 0 ? "[]" : `[${items.map(scalar).join(", ")}]`;
}

function shellValue(shell: string | string[]): string {
  return Array.isArray(shell) ? flowList(shell) : scalar(shell);
}

export function buildManifest(doc: CatalogDoc): string {
  const seen = new Set<string>();
  for (const app of doc.micro_apps) {
    if (seen.has(app.id)) throw new Error(`duplicate micro-app id ${app.id}`);
    seen.add(app.id);
    if (!app.offline || !app.offline.mode) {
      throw new Error(`${app.id}: missing offline block or offline mode`);
    }
    if (!Array.isArray(app.roles)) {
      throw new Error(
        `${app.id}: the roles key is required; an empty list means pre-auth token gating, a missing key is an error`,
      );
    }
  }
  for (const section of doc.launcher_sections ?? []) {
    if (seen.has(section.id)) {
      throw new Error(
        `launcher section ${section.id} also listed as a micro-app; launcher sections are not frontends (analysis/DECISIONS.md D4)`,
      );
    }
  }

  const out: string[] = [];
  out.push(
    "# manifest.yaml, the App Shape manifest for the hh monorepo.",
    "# GENERATED FILE. Produced by scripts/generate-manifest.ts from the role",
    "# catalog; do not edit by hand. Change the catalog or the generator, then:",
    "#   bun scripts/generate-manifest.ts",
    "# Drift check (also run by the manifest-catalog test):",
    "#   bun scripts/generate-manifest.ts --check",
    "#",
    "# Catalog source: docs/analysis/role-catalog.yaml by default; pass a path or",
    "# set HH_ROLE_CATALOG to override.",
    "#",
    "# runtimes, tenancy, database seed, capabilities, agent seats and",
    "# observability come from context/FUTURESHADE-CONTEXT.md section 4.3.",
    "#",
    "# frontends: one entry per micro-app in the catalog; audience, roles,",
    "# devices, kit, shell, brand and the offline block (mode, scopes, per-entity",
    "# conflict policy) are copied verbatim. Launcher sections are not frontends",
    "# (analysis/DECISIONS.md D4) and never appear here. Conflict policy values",
    "# follow context section 4.5: server_wins, last_write_wins_by_field, or",
    "# manual (which surfaces a review card).",
    "#",
    "# gate: role  means the bundle is served after sign-in and the core gates",
    "#             every endpoint by role from the JWT; the shell is never the",
    "#             security boundary (context section 4.5).",
    "# gate: token means roles is empty in the catalog: the bundle is served",
    "#             pre-auth and the one-time or printed token is the credential,",
    "#             so no role check runs.",
    "",
    "shape: 1",
    "app: hh",
    "name: Hardscape House",
    "runtimes: [core, web, tauri, agents]",
    "tenancy: multi-org",
    "",
    "# extensions: pg_trgm per context section 4.3; citext per the HH-2.4",
    "# extensions ADR draft, which adds citext (a standard contrib extension the",
    "# baseline migrations depend on) to the App Shape extension allowlist.",
    "database:",
    "  extensions: [pg_trgm, citext]",
    "  seed: db/seed/demo.sql",
    "",
    "capabilities:",
    "  features: [threads, member_sandboxes, intake_rooms]",
    "",
    "agents:",
    "  seats: [product_lead]",
    "  intake_rooms: [ideas, product_feedback]",
    "",
    "observability:",
    "  service: hh",
    "",
    "frontends:",
  );

  for (const app of doc.micro_apps) {
    const roles = app.roles;
    const gate = roles.length === 0 ? "token" : "role";
    out.push(`  - id: ${scalar(app.id)}`);
    out.push(`    name: ${scalar(app.name)}`);
    out.push(`    audience: ${scalar(app.audience)}`);
    out.push(`    roles: ${flowList(roles)}`);
    if (gate === "token") {
      out.push(
        "    # pre-auth: served without sign-in; the token is the credential and no role check runs",
      );
    }
    out.push(`    gate: ${gate}`);
    out.push(`    devices: ${flowList(app.devices ?? [])}`);
    out.push(`    kit: ${scalar(app.kit)}`);
    out.push(`    shell: ${shellValue(app.shell)}`);
    out.push(`    brand: ${scalar(app.brand)}`);
    if (app.offline.note) {
      out.push(`    # offline note from the catalog: ${app.offline.note}`);
    }
    out.push("    offline:");
    out.push(`      mode: ${scalar(app.offline.mode)}`);
    out.push(`      scopes: ${flowList(app.offline.scopes ?? [])}`);
    const entries = Object.entries(app.offline.conflict_policy ?? {});
    if (entries.length === 0) {
      out.push("      conflict_policy: {}");
    } else {
      out.push("      conflict_policy:");
      for (const [scope, policy] of entries) {
        out.push(`        ${scalar(scope)}: ${scalar(String(policy))}`);
      }
    }
    out.push("");
  }

  return `${out.join("\n").replace(/\n+$/, "\n")}`;
}

async function main(): Promise<void> {
  const argv = process.argv.slice(2);
  const doc = await loadCatalog(catalogPath(argv, process.env));
  const manifest = buildManifest(doc);

  if (argv.includes("--stdout")) {
    process.stdout.write(manifest);
    return;
  }

  if (argv.includes("--check")) {
    const current = await Bun.file(MANIFEST_PATH).text().catch(() => "");
    if (current !== manifest) {
      console.error(
        `manifest.yaml differs from a fresh build of the catalog; run: bun scripts/generate-manifest.ts`,
      );
      process.exit(1);
    }
    console.log("manifest.yaml matches the catalog");
    return;
  }

  await Bun.write(MANIFEST_PATH, manifest);
  console.log(`wrote ${MANIFEST_PATH} (${doc.micro_apps.length} frontends)`);
}

if (import.meta.main) {
  await main();
}

// manifest-catalog.test.ts: the manifest and the role catalog must not be able
// to disagree (lane HH-2.2 acceptance). Run from anywhere:
//   bun test scripts/manifest-catalog.test.ts
// The catalog path resolves as in generate-manifest.ts: HH_ROLE_CATALOG, else
// docs/analysis/role-catalog.yaml.

import { describe, expect, test } from "bun:test";
import { YAML } from "bun";
import {
  buildManifest,
  catalogPath,
  loadCatalog,
  MANIFEST_PATH,
  validateCatalog,
  type CatalogDoc,
  type MicroApp,
} from "./generate-manifest";

interface FrontendEntry {
  id: string;
  name: string;
  audience: string;
  roles: string[];
  gate: string;
  devices: string[];
  kit: string;
  shell: string | string[];
  brand: string;
  offline: {
    mode: string;
    scopes: string[];
    conflict_policy: Record<string, string>;
  };
}

const catalog: CatalogDoc = await loadCatalog(
  catalogPath(process.argv.slice(2), process.env),
);
const manifestText = await Bun.file(MANIFEST_PATH).text();
const manifest = YAML.parse(manifestText) as {
  shape: number;
  app: string;
  database: { extensions: string[] };
  runtimes: string[];
  frontends: FrontendEntry[];
};

const catalogById = new Map<string, MicroApp>(
  catalog.micro_apps.map((app) => [app.id, app]),
);
const frontendById = new Map<string, FrontendEntry>(
  manifest.frontends.map((frontend) => [frontend.id, frontend]),
);

describe("manifest metadata", () => {
  test("shape and app identity", () => {
    expect(manifest.shape).toBe(1);
    expect(manifest.app).toBe("hh");
  });

  test("runtimes from context section 4.3", () => {
    expect(manifest.runtimes).toEqual(["core", "web", "tauri", "agents"]);
  });

  test("database extensions declare pg_trgm and citext", () => {
    expect(manifest.database.extensions).toEqual(["pg_trgm", "citext"]);
  });
});

describe("frontends and catalog cannot disagree", () => {
  test("every catalog micro-app appears as a manifest frontend", () => {
    for (const app of catalog.micro_apps) {
      expect(
        frontendById.has(app.id),
        `catalog micro-app ${app.id} has no manifest frontend`,
      ).toBe(true);
    }
  });

  test("every manifest frontend has a catalog source", () => {
    for (const frontend of manifest.frontends) {
      expect(
        catalogById.has(frontend.id),
        `manifest frontend ${frontend.id} has no catalog micro-app`,
      ).toBe(true);
    }
    expect(manifest.frontends.length).toBe(catalog.micro_apps.length);
  });

  test("launcher sections never appear as frontends (analysis/DECISIONS.md D4)", () => {
    for (const section of catalog.launcher_sections ?? []) {
      expect(frontendById.has(section.id)).toBe(false);
    }
  });

  test("audience, roles, devices, kit, shell and brand are copied verbatim", () => {
    for (const app of catalog.micro_apps) {
      const frontend = frontendById.get(app.id);
      expect(frontend, app.id).toBeDefined();
      expect(frontend!.name).toBe(app.name);
      expect(frontend!.audience).toBe(app.audience);
      expect(frontend!.roles).toEqual(app.roles);
      expect(frontend!.devices).toEqual(app.devices);
      expect(frontend!.kit).toBe(app.kit);
      expect(frontend!.shell).toEqual(app.shell);
      expect(frontend!.brand).toBe(app.brand);
    }
  });

  test("offline mode, scopes and conflict policy are copied verbatim", () => {
    for (const app of catalog.micro_apps) {
      const frontend = frontendById.get(app.id);
      expect(frontend, app.id).toBeDefined();
      expect(frontend!.offline.mode).toBe(app.offline.mode);
      expect(frontend!.offline.scopes).toEqual(app.offline.scopes);
      expect(frontend!.offline.conflict_policy).toEqual(
        app.offline.conflict_policy ?? {},
      );
    }
  });
});

describe("pre-auth gating", () => {
  test("a frontend declaring no roles is served pre-auth and gated by its token", () => {
    for (const frontend of manifest.frontends) {
      if (frontend.roles.length === 0) {
        expect(
          frontend.gate,
          `${frontend.id} declares no roles: gate must be token, not the role check`,
        ).toBe("token");
      } else {
        expect(
          frontend.gate,
          `${frontend.id} declares roles: gate must be role`,
        ).toBe("role");
      }
    }
  });

  test("the pre-auth set matches the catalog's empty-roles set exactly", () => {
    const catalogPreAuth = catalog.micro_apps
      .filter((app) => app.roles.length === 0)
      .map((app) => app.id)
      .sort();
    const manifestPreAuth = manifest.frontends
      .filter((frontend) => frontend.gate === "token")
      .map((frontend) => frontend.id)
      .sort();
    expect(manifestPreAuth).toEqual(catalogPreAuth);
  });

  test("the pre-auth set is demo-entry, hh-site and public-desk (analysis/DECISIONS.md D23)", () => {
    const manifestPreAuth = manifest.frontends
      .filter((frontend) => frontend.gate === "token")
      .map((frontend) => frontend.id)
      .sort();
    expect(manifestPreAuth).toEqual(["demo-entry", "hh-site", "public-desk"]);
  });

  test("a missing roles key fails validation and names the entry and the key", () => {
    const broken = {
      catalog: 1,
      micro_apps: [
        {
          id: "roles-probe",
          name: "Roles Probe",
          audience: "internal",
          devices: ["desktop"],
          kit: "list-detail",
          shell: "hh-work",
          brand: "fb-tokens",
          offline: { mode: "none", scopes: [], conflict_policy: {} },
        },
      ],
    } as unknown as CatalogDoc;
    expect(() => validateCatalog(broken)).toThrow(/roles-probe/);
    expect(() => validateCatalog(broken)).toThrow(/roles key is required/);
  });

  test("a missing offline.scopes or devices key fails validation, naming the entry and the key", () => {
    const base = {
      id: "shape-probe",
      name: "Shape Probe",
      audience: "internal",
      roles: ["inside_sales"],
      devices: ["desktop"],
      kit: "list-detail",
      shell: "hh-work",
      brand: "fb-tokens",
      offline: { mode: "read_cache_only", scopes: ["customers"], conflict_policy: {} },
    };
    const noScopes = {
      catalog: 1,
      micro_apps: [{ ...base, offline: { mode: "read_cache_only", conflict_policy: {} } }],
    } as unknown as CatalogDoc;
    expect(() => validateCatalog(noScopes)).toThrow(/shape-probe/);
    expect(() => validateCatalog(noScopes)).toThrow(/offline\.scopes key is required/);
    const noDevices = {
      catalog: 1,
      micro_apps: [{ ...base, devices: undefined }],
    } as unknown as CatalogDoc;
    expect(() => validateCatalog(noDevices)).toThrow(/shape-probe/);
    expect(() => validateCatalog(noDevices)).toThrow(/devices key is required/);
  });
});

describe("generated file is current", () => {
  test("manifest.yaml is byte-identical to a fresh build from the catalog", () => {
    expect(manifestText).toBe(buildManifest(catalog));
  });
});

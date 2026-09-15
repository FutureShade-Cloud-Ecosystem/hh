// sync-scope-catalog.test.ts: the SyncScope enum in core/api/skeleton.yaml
// must contain every offline scope the role catalog declares. The enum's own
// description claims the manifest and the catalog may not disagree with it;
// this test makes that claim enforceable. Run from anywhere:
//   bun test scripts/sync-scope-catalog.test.ts
// The catalog path resolves as in generate-manifest.ts (HH_ROLE_CATALOG, else
// docs/analysis/role-catalog.yaml); the skeleton path defaults to
// core/api/skeleton.yaml and can be overridden with HH_API_SKELETON.

import { describe, expect, test } from "bun:test";
import { YAML } from "bun";
import { resolve } from "node:path";
import {
  catalogPath,
  loadCatalog,
  REPO_ROOT,
  type CatalogDoc,
} from "./generate-manifest";

const SKELETON_PATH = resolve(
  process.env.HH_API_SKELETON ?? resolve(REPO_ROOT, "core/api/skeleton.yaml"),
);

const catalog: CatalogDoc = await loadCatalog(
  catalogPath(process.argv.slice(2), process.env),
);
const skeleton = YAML.parse(await Bun.file(SKELETON_PATH).text()) as {
  components: { schemas: { SyncScope: { enum: string[] } } };
};

const catalogScopes = new Set<string>();
for (const app of catalog.micro_apps) {
  for (const scope of app.offline.scopes) {
    catalogScopes.add(scope);
  }
}

describe("SyncScope enum and the catalog cannot disagree", () => {
  test("every offline scope in the catalog appears in the SyncScope enum", () => {
    const enumValues = skeleton.components.schemas.SyncScope.enum;
    for (const scope of catalogScopes) {
      expect(
        enumValues,
        `offline scope ${scope} is declared in the catalog and the manifest but missing from the SyncScope enum in ${SKELETON_PATH}`,
      ).toContain(scope);
    }
  });
});

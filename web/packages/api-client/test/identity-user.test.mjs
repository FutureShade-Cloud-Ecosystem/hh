// The exchange's tenant leg answers scope TENANT (tier 0, a portal role code);
// the generated IdentityUser must admit it, so a real exchange adapter never
// receives a value its own type forbids.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SCHEMA_DESCRIPTORS } from "../src/testing.ts";
import { validateValue } from "./_validate.mjs";

const USER = { user_id: "u1", email: "buyer@example.test", display_name: "Buyer", role_code: "buyer", role_name: "Buyer", tier: 0 };

test("IdentityUser admits the staff scopes and the tenant scope, and nothing else", () => {
  for (const scope of ["GLOBAL", "BRANCH", "TENANT"]) {
    assert.deepEqual(validateValue({ ...USER, scope }, SCHEMA_DESCRIPTORS.IdentityUser), [], scope);
  }
  assert.notEqual(validateValue({ ...USER, scope: "OTHER" }, SCHEMA_DESCRIPTORS.IdentityUser).length, 0);
});

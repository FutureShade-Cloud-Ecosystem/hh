// Scope declarations: which scopes a shell profile holds, which of them admit
// a client write, and the fence that keeps money out of the outbox. The
// constants are pinned to manifest.yaml and the Go registry so a drift in
// either fails here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { MANIFEST_OFFLINE, MONEY_SCOPES, SCOPE_POLICIES, ScopeBook, scopeBookFor } from "../src/scopes.ts";

const repo = (rel) => fileURLToPath(new URL(`../../../../${rel}`, import.meta.url));

function parseManifestOffline(text) {
  const frontends = text.split(/\n  - id: /).slice(1);
  return frontends.map((block) => {
    const id = block.split("\n")[0].trim();
    const mode = /\n      mode: (\w+)/.exec(block)[1];
    const scopes = /\n      scopes: \[([^\]]*)\]/.exec(block)[1].split(",").map((s) => s.trim()).filter(Boolean);
    const policy = {};
    const cp = /\n      conflict_policy: ?(\{\}|\n((?:        \S.*\n?)+))/.exec(block);
    if (cp[2]) for (const line of cp[2].trim().split("\n")) {
      const [k, v] = line.split(":").map((s) => s.trim());
      policy[k] = v;
    }
    return { id, mode, scopes, policy };
  });
}

test("the offline blocks are the manifest's, entry by entry", () => {
  const parsed = parseManifestOffline(readFileSync(repo("manifest.yaml"), "utf8"));
  assert.ok(parsed.length >= 12);
  assert.deepEqual(
    MANIFEST_OFFLINE.map((a) => ({ id: a.id, mode: a.mode, scopes: [...a.scopes], policy: { ...a.conflictPolicy } })),
    parsed,
  );
});

test("the scope policies are the Go registry's, scope by scope", () => {
  const go = readFileSync(repo("core/internal/sync/registry.go"), "utf8");
  const found = {};
  for (const m of go.matchAll(/"([a-z_]+)":\s*\{Name: "[a-z_]+", Policy: Policy(\w+),/g)) {
    found[m[1]] = { ServerWins: "server_wins", LWWByField: "last_write_wins_by_field", Manual: "manual", PullOnly: "pull_only" }[m[2]];
  }
  assert.equal(Object.keys(found).length, 43);
  assert.deepEqual({ ...SCOPE_POLICIES }, found);
});

test("every manifest conflict policy agrees with the registry", () => {
  for (const app of MANIFEST_OFFLINE) {
    for (const [scope, policy] of Object.entries(app.conflictPolicy)) {
      assert.equal(SCOPE_POLICIES[scope], policy, `${app.id} ${scope}`);
    }
  }
});

test("the Field profile declares exactly Yard Pick's and HH Drive's scopes", () => {
  const book = scopeBookFor(["yard-pick", "drive-run"]);
  assert.deepEqual(book.declared(), ["attachments", "inventory_lookup", "pick_tasks", "pod", "receiving", "runs", "shortages", "stops", "weights"]);
});

test("writable scopes are those with a declared policy in a full or outbox app", () => {
  const book = scopeBookFor(["yard-pick", "drive-run"]);
  for (const s of ["pick_tasks", "shortages", "receiving", "weights", "attachments", "stops", "pod"]) {
    assert.equal(book.writeRefusal(s), null, s);
  }
  assert.equal(book.writeRefusal("inventory_lookup").reason, "scope_not_writable");
  assert.equal(book.writeRefusal("runs").reason, "scope_not_writable");
  assert.equal(book.writeRefusal("products").reason, "scope_not_declared");
  assert.equal(book.writeRefusal("not_a_scope").reason, "scope_not_declared");
});

test("money scopes refuse a write whatever any manifest says", () => {
  assert.deepEqual([...MONEY_SCOPES].sort(), ["aging", "approvals", "invoices", "payment_methods", "payments", "price_matrix"]);
  const everything = scopeBookFor(MANIFEST_OFFLINE.map((a) => a.id));
  for (const s of MONEY_SCOPES) {
    assert.equal(everything.writeRefusal(s)?.reason, "money_scope", s);
  }
  // price_matrix has a declared policy in the Quote Desk, and is refused all the same.
  assert.equal(scopeBookFor(["quote-desk"]).writeRefusal("price_matrix").reason, "money_scope");
  assert.equal(scopeBookFor(["quote-desk"]).writeRefusal("quotes"), null);
});

test("a read cache only app never admits a write, even for a scope with a policy", () => {
  const book = scopeBookFor(["manager-home"]);
  assert.equal(book.writeRefusal("approvals").reason, "money_scope");
  assert.equal(book.writeRefusal("widget_measures").reason, "scope_not_writable");
});

test("an unknown app id is a defect, not an empty profile", () => {
  assert.throws(() => scopeBookFor(["no-such-app"]), /no-such-app/);
});

test("a hand built book cannot admit a money scope by spelling it differently, nor a name that is not a registry scope", () => {
  const spelled = ["Payments", " payments", "payments\n", "PAYMENTS", "Price_Matrix", "approvals ", "pay-ments", "", "__proto__", "constructor", "not_a_registry_scope", "ent_pick_tasks; DROP TABLE outbox"];
  const book = new ScopeBook([
    {
      id: "hand-built",
      mode: "full",
      scopes: spelled,
      conflictPolicy: Object.fromEntries(spelled.map((n) => [n, "server_wins"])),
    },
  ]);
  for (const name of spelled) assert.notEqual(book.writeRefusal(name), null, JSON.stringify(name));
  assert.equal(book.writeRefusal("Payments").reason, "money_scope", "a money name in any spelling is called what it is");
  assert.equal(book.writeRefusal("ent_pick_tasks; DROP TABLE outbox").reason, "scope_not_declared");
});

// The front door's node suite: the mint validation, the session custody's two
// keys and refusals of every unusable input, and the catalog's shape against
// the rule the D2 brief fixes (two live roles with the contract's labels and
// the till's flow ids; eleven coming roles, each carrying the modules the
// matrix gives it at work, manage or admin level). A test that read the
// catalog from the app would pass on an app that forgot a role, so the
// counts and the labels are written here the way the till's entry test
// writes its coming list.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SESSION_MIRROR_KEY } from "@hardscapehouse/auth";
import {
  FRONT_DOOR_SESSION_KEY,
  FrontDoorSessionStore,
  validateMint,
} from "../src/session.js";
import { COMING_ROLES, LIVE_ROLES } from "../src/roles.js";

/** A storage double with the map's own semantics, probed like the real thing. */
function fakeStorage() {
  const mem = new Map();
  return {
    getItem: (key) => (mem.has(key) ? mem.get(key) : null),
    setItem: (key, value) => {
      mem.set(key, value);
    },
    removeItem: (key) => {
      mem.delete(key);
    },
  };
}

const NOW = 1_700_000_000_000;

function mintAnswer(overrides = {}) {
  const exp = Math.floor(NOW / 1000) + 3600;
  const token = [
    Buffer.from('{"alg":"HS256","typ":"JWT"}').toString("base64url"),
    Buffer.from(JSON.stringify({ sub: "demo-account-sales", exp, ...overrides.tokenPayload })).toString(
      "base64url",
    ),
    Buffer.from('{"stub":true}').toString("base64url"),
  ].join(".");
  return {
    token,
    user: {
      user_id: "demo-account-sales",
      email: "sales@demo.invalid",
      display_name: "Sales (demo)",
      role_code: "inside_sales",
      role_name: "Inside Sales",
      tier: 3,
      scope: "BRANCH",
      ...overrides.user,
    },
  };
}

function validated(overrides = {}) {
  return validateMint(mintAnswer(overrides), "sales", "Sales", NOW);
}

test("the mint validates whole, and the expiry is the token's own claim", () => {
  const session = validated();
  assert.notEqual(session, null);
  assert.equal(session.flow, "sales");
  assert.equal(session.roleLabel, "Sales");
  assert.equal(session.tokenExpiresAtMs, (Math.floor(NOW / 1000) + 3600) * 1000);
  assert.equal(session.user.userId, "demo-account-sales");
  assert.equal(session.roleCode, "inside_sales");
  assert.equal(session.tier, 3);
});

test("a mint without a token, a usable user block, or a readable expiry refuses", () => {
  assert.equal(validateMint({ user: mintAnswer().user }, "sales", "Sales", NOW), null, "no token");
  const noExpiry = mintAnswer({ tokenPayload: { exp: undefined } });
  assert.equal(validateMint(noExpiry, "sales", "Sales", NOW), null, "no exp claim");
  const expired = validateMint(mintAnswer(), "sales", "Sales", NOW + 3600 * 1000 + 1);
  assert.equal(expired, null, "an expired token is not a session");
  const notJwt = { token: "not-a-jwt", user: mintAnswer().user };
  assert.equal(validateMint(notJwt, "sales", "Sales", NOW), null, "a non JWT shape refuses");
  for (const field of ["user_id", "display_name", "role_code", "role_name", "tier", "scope"]) {
    const user = { ...mintAnswer().user };
    delete user[field];
    assert.equal(validateMint({ token: mintAnswer().token, user }, "sales", "Sales", NOW), null,
      `a mint whose user block lacks ${field} refuses`);
  }
});

test("adopt writes the session key and the till custody's mirror key, clear wipes both", () => {
  const storage = fakeStorage();
  const store = new FrontDoorSessionStore({ storage, now: () => NOW });
  const session = validated();
  assert.notEqual(session, null);
  store.adopt(session);
  assert.ok(storage.getItem(FRONT_DOOR_SESSION_KEY)?.includes('"token"'), "the token is stored");
  const mirror = JSON.parse(storage.getItem(SESSION_MIRROR_KEY));
  assert.deepEqual(mirror, { v: 2, userId: session.user.userId, email: session.user.email, name: session.user.name },
    "the mirror key carries the display profile alone, in the mirror's own shape");
  store.clear();
  assert.equal(storage.getItem(FRONT_DOOR_SESSION_KEY), null, "the session key is gone");
  assert.equal(storage.getItem(SESSION_MIRROR_KEY), null, "the mirror key is gone");
});

test("current reads a stored session back and refuses a corrupt or expired one", () => {
  const storage = fakeStorage();
  const store = new FrontDoorSessionStore({ storage, now: () => NOW });
  store.adopt(validated());
  // A fresh custody over the same storage resumes the session: this is the reload path.
  const resumed = new FrontDoorSessionStore({ storage, now: () => NOW });
  assert.equal(resumed.current()?.flow, "sales");
  // A hand-edited entry reads as absent, never as a session with fields it does not carry.
  storage.setItem(FRONT_DOOR_SESSION_KEY, '{"token":"x"}');
  assert.equal(resumed.current(), null);
  // An expired record reads as absent too.
  storage.setItem(
    FRONT_DOOR_SESSION_KEY,
    JSON.stringify({ ...validated(), tokenExpiresAtMs: NOW - 1 }),
  );
  assert.equal(resumed.current(), null, "a stale mint is not a session");
});

test("a hostile storage accessor degrades to memory and nothing throws", () => {
  const hostile = {
    get getItem() {
      throw new Error("denied site data");
    },
  };
  const store = new FrontDoorSessionStore({ storage: hostile, now: () => NOW });
  assert.equal(store.degraded, true);
  const session = validated();
  assert.notEqual(session, null);
  store.adopt(session);
  assert.equal(store.current()?.flow, "sales", "the session lives in memory");
  store.clear();
  assert.equal(store.current(), null);
});

test("the catalog holds the two live roles with the contract's labels and the till's flows", () => {
  assert.equal(LIVE_ROLES.length, 2);
  const [sales, manager] = LIVE_ROLES;
  assert.equal(sales.label, "Sales");
  assert.equal(sales.flow, "sales");
  assert.deepEqual(sales.tiles.map((tile) => tile.name), ["Counter Till", "Quote Desk"]);
  assert.equal(manager.label, "Sales manager");
  assert.equal(manager.flow, "sales-manager");
  assert.deepEqual(
    manager.tiles.map((tile) => tile.name),
    ["Counter Till", "Quote Desk", "Manager Home", "Dealer Admin", "House Messages"],
  );
  // The box's two live modules navigate; the manager's three not-yet modules do not.
  assert.deepEqual(
    sales.tiles.map((tile) => tile.href),
    ["/till/", "/quote/"],
  );
  assert.deepEqual(
    manager.tiles.map((tile) => tile.href),
    ["/till/", "/quote/", null, null, null],
  );
});

test("the catalog holds the eleven coming roles, each with its matrix modules", () => {
  const labels = [
    "Outside Sales",
    "Yard Team",
    "Yard Manager",
    "Logistics Manager",
    "Driver",
    "Branch Manager",
    "Procurement Manager",
    "Payables and Receivables",
    "Financial Controller",
    "General Manager",
    "HR",
  ];
  assert.deepEqual(COMING_ROLES.map((role) => role.label), labels);
  // Every coming role shows at least the modules the matrix gives it at work
  // level or above; HR's two and Driver's two are the smallest rows, the yard
  // manager's seven the widest.
  for (const role of COMING_ROLES) {
    assert.ok(role.modules.length >= 2, `${role.label} shows its modules`);
    assert.ok(role.modules.length <= 7, `${role.label} carries no invented module`);
  }
  // Spot checks against ROLE-ACCESS-r3's cells: Outside Sales has no till;
  // the general manager's admin cells include Dealer Admin; HR's are House
  // Messages and Dealer Admin.
  const outside = COMING_ROLES.find((role) => role.id === "outside_sales");
  assert.equal(outside.modules.includes("Counter Till"), false);
  const general = COMING_ROLES.find((role) => role.id === "general_manager");
  assert.deepEqual(general.modules, ["Money Desk", "Manager Home", "House Messages", "Dealer Admin"]);
  const hr = COMING_ROLES.find((role) => role.id === "hr");
  assert.deepEqual(hr.modules, ["House Messages", "Dealer Admin"]);
});

// The approvals note's count: the core's bare array, the contract's page
// object, the margin kind only, pending only, every cursor followed.
import { countMarginApprovals, readApprovalsPage } from "../src/approvals-count.js";

const row = (policy_type, status) => ({ id: "a", requested_by: "u", branch_id: "b", policy_type, detail: "d", status });
const pageOf = (pages) => async (cursor) => ({ ok: true, value: pages[cursor ?? ""] });

test("the count reads the core's bare array", async () => {
  const n = await countMarginApprovals(pageOf({ "": [row("MARGIN_BELOW_MINIMUM", "PENDING"), row("MARGIN_BELOW_MINIMUM", "PENDING")] }));
  assert.equal(n, 2);
});

test("the count reads the contract's page object and follows every cursor", async () => {
  const n = await countMarginApprovals(
    pageOf({
      "": { items: [row("MARGIN_BELOW_MINIMUM", "PENDING")], next_cursor: "c2" },
      c2: { items: [row("MARGIN_BELOW_MINIMUM", "PENDING")], next_cursor: "c3" },
      c3: { items: [row("MARGIN_BELOW_MINIMUM", "PENDING")], next_cursor: null },
    }),
  );
  assert.equal(n, 3);
});

test("the count is the margin kind and pending only", async () => {
  const n = await countMarginApprovals(
    pageOf({
      "": [
        row("MARGIN_BELOW_MINIMUM", "PENDING"),
        row("MARGIN_BELOW_MINIMUM", "APPROVED"),
        row("CREDIT_LIMIT_EXCEEDED", "PENDING"),
        row("FREIGHT_ABOVE_QUOTE", "PENDING"),
      ],
    }),
  );
  assert.equal(n, 1);
});

test("an unreadable answer, a bad row, a failed page or a repeated cursor yields no count", async () => {
  assert.equal(await countMarginApprovals(pageOf({ "": "nope" })), null);
  assert.equal(await countMarginApprovals(pageOf({ "": [{ policy_type: 1, status: "PENDING" }] })), null);
  assert.equal(await countMarginApprovals(async () => ({ ok: false })), null);
  assert.equal(
    await countMarginApprovals(pageOf({ "": { items: [], next_cursor: "x" }, x: { items: [], next_cursor: "x" } })),
    null,
  );
  assert.equal(readApprovalsPage({ items: [], next_cursor: 5 }), null);
});

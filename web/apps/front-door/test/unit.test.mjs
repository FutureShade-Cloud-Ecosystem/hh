// The front door's node suite: the mint validation, the session custody's two
// keys and refusals of every unusable input, and the catalog's shape against
// the rule the D2 brief fixes (the live roles with the contract's
// labels and the flows they mint; the coming roles, each carrying the modules
// the matrix gives it at work, manage or admin level). A test that read the
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
import { COMING_ROLES, LIVE_ROLES, comingHeading } from "../src/roles.js";

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

test("the catalog holds the thirteen live roles with the contract's labels and the flows they mint", () => {
  assert.equal(LIVE_ROLES.length, 13);
  const [sales, manager, yardManager, yardTeam, payRec, buyer, driver, controller] = LIVE_ROLES;
  assert.equal(sales.label, "Sales");
  assert.equal(sales.flow, "sales");
  assert.deepEqual(sales.tiles.map((tile) => tile.name), ["Counter Till", "Quote Desk"]);
  assert.equal(manager.label, "Sales manager");
  assert.equal(manager.flow, "sales-manager");
  assert.deepEqual(
    manager.tiles.map((tile) => tile.name),
    ["Counter Till", "Quote Desk", "Manager Home", "Dealer Admin", "House Messages"],
  );
  // The yard manager: Manager Home is its live landing, the Yard Pick app its
  // live pick app and Buying and Stock the live buying app; the two modules
  // the matrix gives it that the box does not serve yet open the coming-soon
  // page.
  assert.equal(yardManager.label, "Yard manager");
  assert.equal(yardManager.flow, "yard-manager");
  assert.deepEqual(
    yardManager.tiles.map((tile) => tile.name),
    ["Manager Home", "Yard Pick", "Dispatch and Load Building", "Buying and Stock", "House Messages"],
  );
  assert.deepEqual(
    yardManager.tiles.map((tile) => tile.href),
    ["/home/", "/yard/", null, "/buying-and-stock/", null],
  );
  // The Yard Team's label is the flows row's own ("Yard Team"), and its one
  // live module is the Yard Pick app under /yard/; House Messages is not on
  // the box yet.
  assert.equal(yardTeam.label, "Yard Team");
  assert.equal(yardTeam.flow, "yard-team");
  assert.deepEqual(yardTeam.tiles.map((tile) => tile.name), ["Yard Pick", "House Messages"]);
  assert.deepEqual(yardTeam.tiles.map((tile) => tile.href), ["/yard/", null]);
  assert.equal(buyer.label, "Procurement manager");
  assert.equal(buyer.flow, "procurement-manager");
  assert.deepEqual(
    buyer.tiles.map((tile) => tile.name),
    ["Buying and Stock", "Manager Home", "House Messages"],
  );
  // The box's live modules navigate; the not-yet modules do not.
  assert.deepEqual(
    sales.tiles.map((tile) => tile.href),
    ["/till/", "/quote/"],
  );
  assert.deepEqual(
    manager.tiles.map((tile) => tile.href),
    ["/till/", "/quote/", "/home/", null, null],
  );
  assert.deepEqual(
    buyer.tiles.map((tile) => tile.href),
    ["/buying-and-stock/", "/home/", null],
  );
  // Payables and Receivables' one live module is the Money Desk; its Manager
  // Home tile opens at /home/ and its House Messages tile is not on the box yet.
  assert.equal(payRec.label, "Payables and Receivables");
  assert.equal(payRec.flow, "payables-receivables");
  assert.deepEqual(payRec.tiles.map((tile) => tile.name), ["Money Desk", "Manager Home", "House Messages"]);
  assert.deepEqual(payRec.tiles.map((tile) => tile.href), ["/money-desk/", "/home/", null]);
  // The driver: HH Drive is its live landing; House Messages is the closed
  // module behind the chipped nav.
  assert.equal(driver.label, "Driver");
  assert.equal(driver.flow, "driver");
  assert.deepEqual(driver.tiles.map((tile) => tile.name), ["HH Drive", "House Messages"]);
  assert.deepEqual(driver.tiles.map((tile) => tile.href), ["/drive/", null]);
  // The Financial Controller's one live module is the same Money Desk tile, on the same path.
  assert.equal(controller.id, "financial_controller");
  assert.equal(controller.label, "Financial Controller");
  assert.equal(controller.flow, "financial-controller");
  assert.deepEqual(controller.tiles.map((tile) => tile.name), ["Money Desk", "Manager Home", "House Messages"]);
  assert.deepEqual(controller.tiles.map((tile) => tile.href), ["/money-desk/", "/home/", null]);
  assert.equal(controller.tiles[0], payRec.tiles[0], "both roles open the one Money Desk tile");
  // The General Manager (global, tier one): the Money Desk and Manager Home
  // are on the box, so their tiles navigate; House Messages and Dealer Admin
  // are not built yet, so they stay href null and read "coming".
  const general = LIVE_ROLES.find((role) => role.id === "general_manager");
  assert.equal(general.label, "General Manager");
  assert.equal(general.flow, "general-manager");
  assert.deepEqual(
    general.tiles.map((tile) => tile.name),
    ["Money Desk", "Manager Home", "House Messages", "Dealer Admin"],
  );
  assert.deepEqual(general.tiles.map((tile) => tile.href), ["/money-desk/", "/home/", null, null]);
  // HR holds only House Messages and Dealer Admin, neither built yet: both
  // tiles stay href null and read "coming", never a link to nothing.
  const hr = LIVE_ROLES.find((role) => role.id === "hr");
  assert.equal(hr.label, "HR");
  assert.equal(hr.flow, "hr");
  assert.deepEqual(hr.tiles.map((tile) => tile.name), ["House Messages", "Dealer Admin"]);
  assert.deepEqual(hr.tiles.map((tile) => tile.href), [null, null]);
  // The Branch Manager (branch scoped, tier two): the Counter Till, the Quote
  // Desk and Manager Home are on the box, so they navigate; Dispatch and Load
  // Building and House Messages are not built yet and stay href null.
  const branch = LIVE_ROLES.find((role) => role.id === "branch_manager");
  assert.equal(branch.label, "Branch Manager");
  assert.equal(branch.flow, "branch-manager");
  assert.deepEqual(
    branch.tiles.map((tile) => tile.name),
    ["Counter Till", "Quote Desk", "Dispatch and Load Building", "Manager Home", "House Messages"],
  );
  assert.deepEqual(branch.tiles.map((tile) => tile.href), ["/till/", "/quote/", null, "/home/", null]);
  assert.equal(branch.tiles[0], sales.tiles[0], "both roles open the one Counter Till tile");
  // The Logistics Manager (branch scoped, tier two): Manager Home is its one
  // tile on the box; Dispatch and Load Building and House Messages are not
  // built yet and stay href null.
  const logistics = LIVE_ROLES.find((role) => role.id === "logistics_manager");
  assert.equal(logistics.label, "Logistics Manager");
  assert.equal(logistics.flow, "logistics-manager");
  assert.deepEqual(
    logistics.tiles.map((tile) => tile.name),
    ["Dispatch and Load Building", "Manager Home", "House Messages"],
  );
  assert.deepEqual(logistics.tiles.map((tile) => tile.href), [null, "/home/", null]);
});

test("the catalog holds no coming role: every role the matrix names is live", () => {
  // The thirteen roles of the decided matrix, by id: each is on a live card and
  // none is left on a coming row. A role the matrix adds later goes on
  // COMING_ROLES first.
  const ids = [
    "inside_sales",
    "sales_manager",
    "yard_manager",
    "yard_team",
    "payables_receivables",
    "procurement_manager",
    "driver",
    "financial_controller",
    "general_manager",
    "hr",
    "branch_manager",
    "logistics_manager",
    "outside_sales",
  ];
  assert.deepEqual(LIVE_ROLES.map((role) => role.id), ids);
  assert.deepEqual(COMING_ROLES, []);
  // Outside Sales holds no till (rows 363 and 364): the Quote Desk is its live
  // tile and House Messages, not built yet, stays href null.
  const outside = LIVE_ROLES.find((role) => role.id === "outside_sales");
  assert.equal(outside.label, "Outside Sales");
  assert.equal(outside.flow, "outside-sales");
  assert.deepEqual(outside.tiles.map((tile) => tile.name), ["Quote Desk", "House Messages"]);
  assert.deepEqual(outside.tiles.map((tile) => tile.href), ["/quote/", null]);
  assert.equal(outside.tiles.some((tile) => tile.name === "Counter Till"), false);
  assert.equal(outside.tiles[0], LIVE_ROLES[0].tiles[1], "both roles open the one Quote Desk tile");
});

test("the coming disclosure's heading counts the roles still coming, in words", () => {
  assert.equal(comingHeading(COMING_ROLES.length), "No more roles are coming");
  assert.equal(comingHeading(5), "Five more roles are coming");
  assert.equal(comingHeading(1), "One more role is coming");
});

// The approvals note's count: the core's bare array, the contract's page
// object, the margin kind only, pending only, every cursor followed.
import { countMarginApprovals, readApprovalsPage } from "../src/approvals-count.js";

const row = (policy_type, status) => ({ id: "a", requested_by: "u", branch_id: "b", policy_type, detail: "d", status });
const pageOf = (pages) => async (cursor) => ({ ok: true, value: pages[cursor ?? ""] });

test("the count reads the contract's page object, and a bare array is not a page", async () => {
  const n = await countMarginApprovals(pageOf({ "": { items: [row("MARGIN_BELOW_MINIMUM", "PENDING"), row("MARGIN_BELOW_MINIMUM", "PENDING")], next_cursor: null } }));
  assert.equal(n, 2);
  assert.equal(await countMarginApprovals(pageOf({ "": [row("MARGIN_BELOW_MINIMUM", "PENDING")] })), null);
});

test("the count follows every cursor", async () => {
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
      "": {
        items: [
          row("MARGIN_BELOW_MINIMUM", "PENDING"),
          row("MARGIN_BELOW_MINIMUM", "APPROVED"),
          row("CREDIT_LIMIT_EXCEEDED", "PENDING"),
          row("FREIGHT_ABOVE_QUOTE", "PENDING"),
        ],
        next_cursor: null,
      },
    }),
  );
  assert.equal(n, 1);
});

test("an unreadable answer, a bad row, a failed page or a repeated cursor yields no count", async () => {
  assert.equal(await countMarginApprovals(pageOf({ "": "nope" })), null);
  assert.equal(await countMarginApprovals(pageOf({ "": { items: [{ policy_type: 1, status: "PENDING" }], next_cursor: null } })), null);
  assert.equal(await countMarginApprovals(async () => ({ ok: false })), null);
  assert.equal(
    await countMarginApprovals(pageOf({ "": { items: [], next_cursor: "x" }, x: { items: [], next_cursor: "x" } })),
    null,
  );
  assert.equal(readApprovalsPage({ items: [], next_cursor: 5 }), null);
});

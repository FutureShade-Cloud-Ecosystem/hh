// The Quote Desk's pure logic, unit tested on the Node runner before any of
// it is written against a screen (the `_*.ts` house convention the ERP's own
// quote helpers set). Every piece of logic this app owns is here or in the
// surface registry's own tests below: the stepper, the totals card's display
// composition, the margin gate's byte-absence rule, the workspace geometry,
// the send channels, the spoken refusal, the status words, the edit offer and
// the typed floor refusal's parse and words. The screens render these answers
// and own no rule of their own.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  collapseNext,
  creditLine,
  customerTypeTag,
  depositCents,
  freshQuoteState,
  initialsOf,
  lineCostCents,
  lineMarginLabel,
  marginPctLabel,
  marginPresent,
  qtyLabel,
  quoteFlowSteps,
  quotePanelTracks,
  sendCaption,
  sendChannels,
  sentConfirmation,
  spokenRefusal,
  validityDaysOf,
  validUntilFromDays,
  VALIDITY_CHOICES,
} from "../src/_quote-workarea.js";
import {
  canConvert,
  canEdit,
  EDIT_LINE_ADD,
  EDIT_LINE_QTY,
  EDIT_LINE_REMOVE,
  EDIT_VALID_UNTIL,
  expiryLabel,
  hasAnyEdit,
  quoteErrorMessage,
  quoteStatusPhrase,
  quoteStatusWord,
  validateQty,
} from "../src/_quote-edit.js";
import {
  effectiveItems,
  panelVisible,
  visibleItems,
  QB_ACTIONS,
  QB_COLUMNS,
  QB_PANELS,
  QUOTES_BUILDER_SURFACE_ID,
} from "../src/quote-surface.js";
import { floorRefusalMessage, parseFloorRefusal } from "../src/floor-refusal.js";

// ── the stepper ────────────────────────────────────────────────────────────────

test("the stepper tracks the quote: nothing picked leaves Customer open", () => {
  const steps = quoteFlowSteps({ hasCustomer: false, lineCount: 0, sentOrBeyond: false });
  assert.deepEqual(
    steps.map((s) => [s.label, s.complete, s.current]),
    [
      ["Customer", false, true],
      ["Products", false, false],
      ["Review & Send", false, false],
    ],
  );
});

test("a picked customer with no lines completes Customer and opens Products", () => {
  const steps = quoteFlowSteps({ hasCustomer: true, lineCount: 0, sentOrBeyond: false });
  assert.deepEqual(
    steps.map((s) => [s.label, s.complete, s.current]),
    [
      ["Customer", true, false],
      ["Products", false, true],
      ["Review & Send", false, false],
    ],
  );
});

test("lines on the quote complete Products and open Review and Send; a sent quote completes all three", () => {
  const steps = quoteFlowSteps({ hasCustomer: true, lineCount: 2, sentOrBeyond: false });
  assert.deepEqual(
    steps.map((s) => [s.label, s.complete, s.current]),
    [
      ["Customer", true, false],
      ["Products", true, false],
      ["Review & Send", false, true],
    ],
  );
  const sent = quoteFlowSteps({ hasCustomer: true, lineCount: 2, sentOrBeyond: true });
  assert.deepEqual(sent.map((s) => s.complete), [true, true, true]);
  assert.deepEqual(sent.map((s) => s.current), [false, false, true]);
});

// ── the totals card ────────────────────────────────────────────────────────────

test("the deposit line is absent when off, and the stored percent of the stored total when on", () => {
  assert.equal(depositCents(false, 10, 36160), null);
  assert.equal(depositCents(true, 0, 36160), null);
  assert.equal(depositCents(true, 10, 36160), 3616);
});

// ── margin: byte absence is the gate ───────────────────────────────────────────

test("marginPresent answers on the server's own byte: an empty glance means no margin surface at all", () => {
  assert.equal(marginPresent(null), false);
  assert.equal(marginPresent({}), false);
  assert.equal(marginPresent({ lines: [{ line_no: 1, margin_bps: 400 }] }), false);
  assert.equal(marginPresent({ margin_bps: 3860 }), true);
});

test("the margin labels speak percent and stay silent where the fields are absent", () => {
  assert.equal(marginPctLabel(3860), "38.6%");
  assert.equal(marginPctLabel(undefined), "");
  assert.equal(lineMarginLabel({ lines: [{ line_no: 1, margin_bps: 1500 }] }, 1), "15.0%");
  assert.equal(lineMarginLabel({ lines: [] }, 2), "");
  assert.equal(lineMarginLabel({ margin_bps: 3000 }, 1), "");
  assert.equal(lineCostCents({ lines: [{ line_no: 1, cost_cents: 27600 }] }, 1), 27600);
  assert.equal(lineCostCents({ lines: [] }, 1), null);
});

// ── the identity card ──────────────────────────────────────────────────────────

test("the identity card's words: initials, the credit line, the type tag", () => {
  assert.equal(initialsOf("Maple Ridge Contracting"), "MR");
  assert.equal(initialsOf("  "), "?");
  assert.equal(creditLine({ credit_limit_cents: 500000, open_balance_cents: 125000 }, (c) => `$${c}`), "Outstanding $125000 · Credit available $375000");
  assert.equal(creditLine({ credit_limit_cents: 500000, open_balance_cents: 0 }, (c) => `$${c}`), "Account current · Credit available $500000");
  assert.equal(customerTypeTag("CONTRACTOR"), "Contractor");
  assert.equal(customerTypeTag(""), "");
});

// ── validity ───────────────────────────────────────────────────────────────────

test("validity days are whole days from an injected today, and the date writer lands on the same arithmetic", () => {
  const now = new Date("2026-03-10T12:00:00Z");
  assert.equal(validUntilFromDays(30, now), "2026-04-09");
  assert.equal(validityDaysOf("2026-04-09", now), 30);
  assert.equal(validityDaysOf("2026-03-10", now), 0);
  assert.equal(validityDaysOf(undefined, now), null);
  assert.deepEqual([...VALIDITY_CHOICES], [7, 14, 30, 60, 90]);
});

test("validity runs on the house day, not the UTC day: nine in the evening in Toronto is still that day", () => {
  // 01:00 UTC on 10 March is 21:00 on the 9th in Toronto (daylight time began on the 8th).
  const evening = new Date("2026-03-10T01:00:00Z");
  assert.equal(validUntilFromDays(30, evening), "2026-04-08");
  assert.equal(validityDaysOf("2026-04-08", evening), 30);
  assert.equal(validityDaysOf("2026-03-09", evening), 0);
});

// ── the workspace geometry ─────────────────────────────────────────────────────

test("the panel tracks follow the arrangement, the split and the collapse state", () => {
  assert.equal(quotePanelTracks(58, "PRIMARY_LEFT", false, false), "minmax(320px, 58fr) 10px minmax(300px, 42fr)");
  assert.equal(quotePanelTracks(58, "PRIMARY_RIGHT", false, false), "minmax(300px, 42fr) 10px minmax(320px, 58fr)");
  assert.equal(quotePanelTracks(58, "PRIMARY_LEFT", true, false), "44px 10px minmax(300px, 42fr)");
  assert.equal(quotePanelTracks(58, "PRIMARY_LEFT", false, true), "minmax(320px, 58fr) 10px 44px");
});

test("collapsing the last open panel restores the other in the same act", () => {
  assert.deepEqual(collapseNext("primary", true, false), { primaryCollapsed: false, secondaryCollapsed: false });
  assert.deepEqual(collapseNext("primary", false, true), { primaryCollapsed: true, secondaryCollapsed: false });
  assert.deepEqual(collapseNext("secondary", false, true), { primaryCollapsed: false, secondaryCollapsed: false });
  assert.deepEqual(collapseNext("secondary", true, false), { primaryCollapsed: false, secondaryCollapsed: true });
});

// ── send channels ──────────────────────────────────────────────────────────────

test("the send channels follow the policy, email leads, and a policy that disables both still offers email", () => {
  assert.deepEqual(sendChannels(null), ["EMAIL"]);
  assert.deepEqual(sendChannels({ send_email_enabled: true, send_sms_enabled: true }), ["EMAIL", "SMS"]);
  assert.deepEqual(sendChannels({ send_email_enabled: false, send_sms_enabled: true }), ["SMS"]);
  assert.deepEqual(sendChannels({ send_email_enabled: true, send_sms_enabled: false }), ["EMAIL"]);
  assert.deepEqual(sendChannels({ send_email_enabled: false, send_sms_enabled: false }), ["EMAIL"]);
  assert.equal(sendCaption(["EMAIL", "SMS"]), "Email + Text");
  assert.equal(sendCaption(["EMAIL"]), "Email");
});

test("the send outcome names only what is true: a link, the Sent state, the cadence, never an email", () => {
  const base = "Quote sent: a link was created, the quote is now Sent and its follow-ups have started.";
  assert.equal(sentConfirmation(false, false, ""), `${base} Nothing was emailed: copy the link to the customer.`);
  assert.equal(sentConfirmation(true, true, ""), `${base} The link was texted to the customer. Nothing was emailed.`);
  assert.equal(
    sentConfirmation(true, false, "quote: the customer has not opted in to text messages"),
    `${base} Nothing was emailed, and the text could not be sent: The customer has not opted in to text messages.`,
  );
  for (const text of [sentConfirmation(false, false, ""), sentConfirmation(true, false, "x")]) assert.doesNotMatch(text, /texted/);
});

// ── the spoken refusal ─────────────────────────────────────────────────────────

test("spokenRefusal peels every module prefix, strips internal enums and never emits a dash", () => {
  assert.equal(spokenRefusal("quote: pricing: this line is below the floor (PRICE_BLOCKED)"), "This line is below the floor");
  assert.equal(spokenRefusal("quote: the quote is already converted (ErrInvalidTransition)"), "The quote is already converted");
  assert.equal(spokenRefusal("quote: pricing: refused with STATE_NAME"), "Refused with");
  assert.equal(/[\u2013\u2014]/.test(spokenRefusal("quote: pricing: refused (PRICE_BLOCKED)")), false);
});

// ── display formatting ─────────────────────────────────────────────────────────

test("quantities read without trailing zeros", () => {
  assert.equal(qtyLabel("2.5000"), "2.5");
  assert.equal(qtyLabel("3"), "3");
  assert.equal(qtyLabel("n/a"), "n/a");
});

// ── starting over ──────────────────────────────────────────────────────────────

test("a new quote resets the quote's own state and nothing of the operator's", () => {
  const fresh = freshQuoteState();
  assert.equal(fresh.customer, null);
  assert.equal(fresh.term, "");
  assert.deepEqual(fresh.qtyDrafts, {});
  assert.deepEqual(fresh.discDrafts, {});
  assert.equal(fresh.error, "");
});

// ── the edit offer and the status words ────────────────────────────────────────

test("canEdit answers only what the server offered, and absent means nothing", () => {
  const view = { editable_fields: [EDIT_LINE_QTY, EDIT_LINE_ADD] };
  assert.equal(canEdit(view, EDIT_LINE_QTY), true);
  assert.equal(canEdit(view, EDIT_LINE_REMOVE), false);
  assert.equal(canEdit({ editable_fields: [] }, EDIT_LINE_QTY), false);
  assert.equal(canEdit(null, EDIT_LINE_QTY), false);
  assert.equal(hasAnyEdit(view), true);
  assert.equal(hasAnyEdit({ editable_fields: [] }), false);
  assert.equal(EDIT_VALID_UNTIL, "valid_until");
});

test("the status words degrade honestly and the phrases say them mid sentence", () => {
  assert.equal(quoteStatusWord("DRAFT"), "Draft");
  assert.equal(quoteStatusWord("CONVERTED"), "Converted");
  assert.equal(quoteStatusWord("SOMETHING_NEW"), "something new");
  assert.equal(quoteStatusPhrase("DRAFT"), "a draft");
  assert.equal(quoteStatusPhrase("SENT"), "sent");
});

test("the expiry line says No expiry when the quote never expires", () => {
  assert.equal(expiryLabel({ valid_until: "2026-04-09", expired: false }), "Valid until Apr 9, 2026");
  assert.equal(expiryLabel({ valid_until: "2026-01-01", expired: true }), "Expired Jan 1, 2026");
  assert.equal(expiryLabel({ valid_until: "", expired: false }), "No expiry");
  assert.equal(expiryLabel(null), "not set");
});

test("the error mapper prefers the server's own sentence and never names a manager rung", () => {
  assert.equal(quoteErrorMessage(403, "this quote belongs to another branch", "edit this line"), "this quote belongs to another branch");
  assert.equal(quoteErrorMessage(404, "", "edit this line"), "That quote no longer exists.");
  assert.equal(quoteErrorMessage(409, "the frozen refusal", "save"), "the frozen refusal");
  assert.equal(quoteErrorMessage(500, "", "save the terms"), "Could not save the terms.");
});

test("validateQty mirrors the server's own bound", () => {
  assert.equal(validateQty("2.5"), null);
  assert.equal(validateQty(""), "Enter a quantity.");
  assert.equal(validateQty("0"), "Quantity must be more than zero. Remove the line instead.");
  assert.notEqual(validateQty("1000000"), null);
});

test("canConvert reads the server's own flag only", () => {
  assert.equal(canConvert({ can_convert: true }), true);
  assert.equal(canConvert({ can_convert: false }), false);
  assert.equal(canConvert(null), false);
});

// ── the surface registry ───────────────────────────────────────────────────────

test("the registry names the surface, the minimum panels and the cost class columns", () => {
  assert.equal(QUOTES_BUILDER_SURFACE_ID, "quotes.builder");
  assert.deepEqual(QB_PANELS.filter((p) => p.min).map((p) => p.id), ["lines", "summary"]);
  assert.deepEqual(QB_COLUMNS.filter((c) => c.cls === "cost").map((c) => c.id), ["line.unit_cost", "line.margin_pct"]);
  assert.ok(QB_ACTIONS.length >= 6);
});

test("effectiveItems: user rows beat role rows beat the catalog, and the catalog hides cost columns by default", () => {
  const byDefault = effectiveItems("column", QB_COLUMNS, undefined, undefined);
  assert.deepEqual(
    byDefault.filter((c) => c.visible).map((c) => c.id),
    ["line.thumb", "line.item", "line.qty", "line.uom", "line.unit_price", "line.disc_pct", "line.total"],
  );
  const roleRows = [{ kind: "column", id: "line.margin_pct", position: 0, visible: true }];
  const byRole = effectiveItems("column", QB_COLUMNS, roleRows, undefined);
  assert.equal(byRole[0].id, "line.margin_pct");
  assert.equal(visibleItems(byRole).length, 1);
  const userRows = [{ kind: "column", id: "line.qty", position: 0, visible: false }];
  assert.deepEqual(visibleItems(effectiveItems("column", QB_COLUMNS, roleRows, userRows)).map((c) => c.id), []);
});

test("panelVisible keeps the minimum set on whatever curation says, and an uncurated optional panel shows", () => {
  const resolved = [
    { id: "lines", kind: "panel", label: "Products", position: 0, visible: false },
    { id: "addons", kind: "panel", label: "Suggested add-ons", position: 2, visible: false },
  ];
  assert.equal(panelVisible(resolved, "lines"), true);
  assert.equal(panelVisible(resolved, "addons"), false);
  const noRow = [{ id: "lines", kind: "panel", label: "Products", position: 0, visible: true }];
  assert.equal(panelVisible(noRow, "addons"), true);
});

// ── the typed floor refusal ────────────────────────────────────────────────────

test("parseFloorRefusal reads the typed body out of the flattened 409 text and refuses anything else", () => {
  const body = JSON.stringify({
    policy: "category_margin_floor",
    approver_roles: ["General Manager", "Financial Controller", "Sales Manager"],
    manager_may_set: true,
    existing_request: { id: "fr-1", status: "PENDING", line_no: 2, disc_pct: 15 },
    margin_bps: 800,
    floor_bps: 1000,
  });
  const refusal = parseFloorRefusal(body);
  assert.notEqual(refusal, null);
  assert.equal(refusal.policy, "category_margin_floor");
  assert.deepEqual(refusal.approverRoles, ["General Manager", "Financial Controller", "Sales Manager"]);
  assert.equal(refusal.managerMaySet, true);
  assert.equal(refusal.requestStatus, "PENDING");
  assert.equal(refusal.marginBps, 800);
  assert.equal(refusal.floorBps, 1000);
  // The envelope the transport carries when the body DOES name an error: that
  // is a sentence, not a typed refusal, and the parse must say so.
  assert.equal(parseFloorRefusal("the quote is already converted"), null);
  assert.equal(parseFloorRefusal(JSON.stringify({ error: "frozen" })), null);
  assert.equal(parseFloorRefusal("{}"), null);
});

test("floorRefusalMessage speaks the typed refusal: the approvers, the branch manager way forward, the figures when sent", () => {
  const withFigures = parseFloorRefusal(
    JSON.stringify({
      policy: "category_margin_floor",
      approver_roles: ["General Manager", "Financial Controller", "Sales Manager"],
      manager_may_set: true,
      margin_bps: 800,
      floor_bps: 1000,
    }),
  );
  const message = floorRefusalMessage(withFigures);
  assert.match(message, /below the margin floor/);
  assert.match(message, /the General Manager, the Financial Controller or the Sales Manager to approve it/);
  assert.match(message, /have the branch's Sales manager set the price itself/);
  assert.match(message, /8\.0%/);
  assert.match(message, /10\.0%/);
  const withoutFigures = parseFloorRefusal(
    JSON.stringify({
      policy: "category_margin_floor",
      approver_roles: ["General Manager", "Financial Controller"],
      manager_may_set: false,
    }),
  );
  const bare = floorRefusalMessage(withoutFigures);
  assert.match(bare, /the General Manager or the Financial Controller/);
  assert.equal(/margin at/.test(bare), false);
  assert.equal(/[\u2013\u2014]/.test(bare), false);
});

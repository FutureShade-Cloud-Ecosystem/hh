// The hold expiry panel's pure tables, ported from the ERP's
// _quote-hold-expiry test and adapted for HH. The banner exists only when the
// server says a hold lapsed and the seam is wired; the sentence counts what
// WAS held; an offer to re-hold is the banner's own (the server decides, and
// refuses in words), never a rule the app holds.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  REHOLD_LABEL,
  expiredLineLabel,
  expiryBanner,
  expiryHeadline,
  formatQty,
  reholdRefusal,
  reholdResultNote,
} from "../src/_quote-hold-expiry.js";

const hold = (over = {}) => ({
  reservation_id: "r1", product_id: "p1", product_name: "Natural Step", sku: "NS-1", branch_id: "b1",
  qty_base: 12, status: "EXPIRED", expires_at: "2026-08-04T13:00:00Z", ended_at: "2026-08-04T13:05:00Z", ...over,
});
const st = (over = {}) => ({ quote_id: "q", active_count: 2, expired: [hold()], any_expired: true, ...over });

test("a banner exists only when a hold lapsed", () => {
  assert.equal(expiryBanner(null), null);
  assert.equal(expiryBanner(st({ any_expired: false, expired: [] })), null);
  assert.notEqual(expiryBanner(st()), null);
});

test("an unwired seam says nothing at all", () => {
  assert.equal(expiryBanner(st(), { seamWired: false }), null);
  assert.notEqual(expiryBanner(st(), { seamWired: true }), null);
});

test("any_expired with an empty list paints no headline", () => {
  assert.equal(expiryBanner(st({ expired: [] })), null);
});

test("all holds consumed (active only) is silent: the key is any_expired and nothing else", () => {
  assert.equal(expiryBanner(st({ any_expired: false, expired: [], active_count: 3 })), null);
});

test("the headline counts what WAS held, with the right verb", () => {
  assert.equal(expiryHeadline(st()), "A stock hold on this quote has expired: 1 of 3 lines is no longer held.");
  assert.equal(
    expiryHeadline(st({ expired: [hold(), hold({ reservation_id: "r2" })], active_count: 1 })),
    "Stock holds on this quote have expired: 2 of 3 lines are no longer held.",
  );
});

test("the lapsed line is named as a rep would say it, with when it lapsed", () => {
  assert.equal(expiredLineLabel(hold()), "12 · Natural Step");
  assert.equal(expiredLineLabel(hold({ qty_base: 12.5 })), "12.5 · Natural Step");
  assert.equal(expiredLineLabel(hold({ product_name: " ", sku: "NS-1" })), "12 · NS-1");
  assert.equal(expiredLineLabel(hold({ product_name: "", sku: "" })), "12 · an unnamed product");
  assert.equal(expiredLineLabel(hold({ status: "RELEASED" })), "12 · Natural Step · released early");
  assert.equal(expiredLineLabel(hold(), (iso) => `@${iso}`), "12 · Natural Step · lapsed @2026-08-04T13:00:00Z");
  assert.equal(expiredLineLabel(hold({ expires_at: undefined }), (iso) => iso), "12 · Natural Step");
});

test("formatQty trims trailing zeros and survives garbage", () => {
  assert.equal(formatQty(4), "4");
  assert.equal(formatQty(4.5), "4.5");
  assert.equal(formatQty(Number.NaN), "0");
});

test("the offer is the banner's own: the app hard codes no rule that hides it", () => {
  const banner = expiryBanner(st());
  assert.equal(banner.offerRehold, true);
  assert.equal(REHOLD_LABEL, "Re-hold these lines");
});

test("a refused re-hold shows the server's words, a fallback only when it sent none", () => {
  assert.equal(reholdRefusal(409, "this quote has expired; extend its expiry date before holding stock again"), "this quote has expired; extend its expiry date before holding stock again");
  assert.match(reholdRefusal(409, ""), /could not be re-held/);
  assert.match(reholdRefusal(undefined, ""), /could not be re-held/);
});

test("reholdResultNote reports what the re-hold did", () => {
  assert.match(reholdResultNote(0), /Nothing could be re-held/);
  assert.equal(reholdResultNote(1), "Re-held 1 line.");
  assert.equal(reholdResultNote(3), "Re-held 3 lines.");
});

test("no sentence carries an em or en dash", () => {
  const all = [expiryHeadline(st()), reholdResultNote(0), reholdRefusal(500, ""), expiryBanner(st()).headline];
  for (const line of all) assert.doesNotMatch(line, /[–—]/);
});

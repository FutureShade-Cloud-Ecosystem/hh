// PR 503 round 3 (P3-4): the staff register's avatar reads the name's own letters,
// never a parenthesis from the demo suffix ("D(" for "Dealer admin (demo)").
import { test } from "node:test";
import assert from "node:assert/strict";

import { staffInitials } from "../src/views-staff.js";

test("the register avatar uses the shared initials, with no parenthesis from a demo suffix", () => {
  assert.equal(staffInitials("Dealer admin (demo)"), "DA");
  assert.equal(staffInitials("Harper Lane"), "HL");
  assert.equal(staffInitials("Inventory"), "I");
});

test("a name with no letters reads two question marks, the register's old fallback", () => {
  assert.equal(staffInitials(""), "??");
  assert.equal(staffInitials("   "), "??");
});

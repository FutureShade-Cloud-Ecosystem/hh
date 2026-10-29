// The office desk's Holds read: the till's credit holds endpoint answers {"items": null} on an empty list
// (the core's shape census names tillListCreditHolds), and opening Holds must read that as no holds, never throw.
import { test } from "node:test";
import assert from "node:assert/strict";
import { OfficeDesk } from "../src/ar/office-desk.ts";

function deskWith(holdsAnswer) {
  const api = {
    tillHolds: async () => holdsAnswer,
  };
  const host = {
    repaint: () => {},
    reveal: () => {},
    field: () => "",
    setField: () => {},
    notice: () => {},
    error: () => {},
    accounts: () => [],
    policy: () => null,
    reloadBook: async () => {},
    reloadAccount: async () => {},
    mountDocument: () => {},
  };
  return new OfficeDesk(api, host, { userId: "fin-console-user-a", roleCode: "financial_controller", userName: "Jordan Blake" });
}

test("opening Holds reads the till's null items as no holds, and does not throw", async () => {
  const desk = deskWith({ ok: true, value: { items: null } });
  await assert.doesNotReject(desk.openHolds());
  assert.equal(desk.model().tillHolds.size, 0);
});

test("a hold the read names lands with the payment that raised it", async () => {
  const desk = deskWith({
    ok: true,
    value: { items: [{ id: "hold-1", source_payment_id: "pay-9" }, { id: "hold-2" }] },
  });
  await desk.openHolds();
  const holds = desk.model().tillHolds;
  assert.equal(holds.get("hold-1"), "pay-9");
  assert.equal(holds.has("hold-2"), false);
});

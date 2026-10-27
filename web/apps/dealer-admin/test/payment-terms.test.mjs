// FW1-14: the Pricing surface's Payment terms and deposits tab. AR-09 named
// it a static board of invented figures with no route behind it and buttons
// that do nothing. This leg pins the honest state: the tab names what belongs
// here, says it is not connected, and carries no figure and no button.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderPaymentTermsAndDeposits } from "../src/views-pricing.ts";

function flattenTemplate(value) {
  if (value === null || value === undefined || typeof value === "symbol") return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.map(flattenTemplate).join("");
  if (typeof value === "object" && Array.isArray(value.strings) && Array.isArray(value.values)) {
    let out = "";
    for (let i = 0; i < value.strings.length; i += 1) {
      out += value.strings[i];
      if (i < value.values.length) out += flattenTemplate(value.values[i]);
    }
    return out;
  }
  return "";
}

test("the payment terms and deposits tab says it is not connected and shows no figure", () => {
  const markup = flattenTemplate(renderPaymentTermsAndDeposits());
  assert.match(markup, /Payment terms and deposits/);
  assert.match(markup, /Not connected yet/);

  // No invented money, no invented percentages, no invented account names.
  for (const invented of [
    "$2,100.00", "$22,300.00", "$14,820.00", "91 days", "25%", "50%",
    "Fieldstone", "Granite Edge", "Bluffside", "ALWAYS", "NOT BY DEFAULT",
  ]) {
    assert.ok(!markup.includes(invented), `the tab still shows the invented figure "${invented}"`);
  }

  // No button: nothing on this tab can be set or saved, and a control that
  // silently did nothing was half of the defect.
  assert.ok(!markup.includes("<button"), "the tab still carries a button");
});

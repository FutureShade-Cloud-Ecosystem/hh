// W3-HST2 contract fixture for GET /api/v1/tax/worksheet (TaxWorksheet).
//
// Every field below is declared on TaxWorksheet / TaxWorksheetLine in
// core/api/fragments/identity.yaml and generated into @hardscapehouse/api-client.
// The spec leg walks the GST/HST return view on this fixture and fails if the
// view reads any key that TaxWorksheet or TaxWorksheetLine does not carry.

import type { TaxWorksheet, TaxWorksheetLine } from "@hardscapehouse/api-client";

export const TAX_WORKSHEET_KEYS = [
  "year",
  "filing_frequency",
  "quarter",
  "month",
  "from_date",
  "to_date",
  "branch_id",
  "line_101_sales_cents",
  "line_103_collected_cents",
  "line_104_adjustments_cents",
  "line_105_total_hst_cents",
  "line_106_itcs_cents",
  "line_107_itc_adjustments_cents",
  "line_108_total_itcs_cents",
  "line_109_net_tax_cents",
  "line_110_instalments_cents",
  "line_111_rebates_cents",
  "line_112_total_other_credits_cents",
  "line_113_balance_cents",
  "line_114_refund_claimed_cents",
  "line_115_amount_owing_cents",
  "invoice_hst_cents",
  "return_tax_cents",
  "gl_payable_cents",
  "variance_cents",
  "tied_out",
  "manual_entry_note",
  "lines",
] as const satisfies readonly (keyof TaxWorksheet)[];

export const TAX_WORKSHEET_LINE_KEYS = [
  "line_no",
  "label",
  "amount_cents",
  "entered_by_hand",
  "note",
] as const satisfies readonly (keyof TaxWorksheetLine)[];

export const TAX_WORKSHEET_ALLOWED_KEYS = new Set<string>([
  ...TAX_WORKSHEET_KEYS,
  ...TAX_WORKSHEET_LINE_KEYS,
]);

export const TAX_WORKSHEET_FIXTURE: TaxWorksheet = {
  year: 2026,
  filing_frequency: "QUARTERLY",
  quarter: 3,
  from_date: "2026-07-01",
  to_date: "2026-09-30",
  line_101_sales_cents: 3_618_000,
  line_103_collected_cents: 470_375,
  line_104_adjustments_cents: 0,
  line_105_total_hst_cents: 470_375,
  line_106_itcs_cents: 87_300,
  line_107_itc_adjustments_cents: 0,
  line_108_total_itcs_cents: 87_300,
  line_109_net_tax_cents: 383_075,
  line_110_instalments_cents: 0,
  line_111_rebates_cents: 0,
  line_112_total_other_credits_cents: 0,
  line_113_balance_cents: 383_075,
  line_114_refund_claimed_cents: 0,
  line_115_amount_owing_cents: 383_075,
  invoice_hst_cents: 482_375,
  return_tax_cents: 12_000,
  gl_payable_cents: 383_075,
  variance_cents: 0,
  tied_out: true,
  manual_entry_note:
    "Lines 101, 103, 105, 106, 108, 109, 112, 113, 114 and 115 are derived from the books. Lines 104 (GST/HST adjustments), 107 (ITC adjustments), 110 (instalments), 111 (rebates), 205 (GST/HST due on the purchase of real property or other taxable supplies) and 405 (other GST/HST to be self-assessed) are not tracked in the books and are entered by hand on the CRA return.",
  lines: [
    { line_no: "101", label: "Sales and other revenue", amount_cents: 3_618_000, entered_by_hand: false, note: "Posted net revenue on REVENUE accounts for the filing period, excluding year-end closing entries." },
    { line_no: "103", label: "GST/HST collected or collectible", amount_cents: 470_375, entered_by_hand: false, note: "Finalized customer invoices less credit memos and POS returns in the filing period." },
    { line_no: "104", label: "Adjustments to GST/HST", amount_cents: 0, entered_by_hand: true, note: "Entered by hand on the CRA return when a manual GST/HST adjustment applies." },
    { line_no: "105", label: "Total GST/HST and adjustments", amount_cents: 470_375, entered_by_hand: false, note: "Line 103 plus line 104." },
    { line_no: "106", label: "Input tax credits (ITCs)", amount_cents: 87_300, entered_by_hand: false, note: "GST/HST paid or payable on posted vendor bills in the filing period." },
    { line_no: "107", label: "ITC adjustments and other adjustments", amount_cents: 0, entered_by_hand: true, note: "Entered by hand on the CRA return when a manual ITC adjustment applies." },
    { line_no: "108", label: "Total ITCs and adjustments", amount_cents: 87_300, entered_by_hand: false, note: "Line 106 plus line 107." },
    { line_no: "109", label: "Net tax", amount_cents: 383_075, entered_by_hand: false, note: "Line 105 less line 108." },
    { line_no: "110", label: "Instalments and other annual filer payments", amount_cents: 0, entered_by_hand: true, note: "Entered by hand on the CRA return when instalments were remitted for the period." },
    { line_no: "111", label: "Rebates", amount_cents: 0, entered_by_hand: true, note: "Entered by hand on the CRA return when a GST/HST rebate is claimed on the return." },
    { line_no: "112", label: "Total other credits", amount_cents: 0, entered_by_hand: false, note: "Line 110 plus line 111." },
    { line_no: "113", label: "Balance", amount_cents: 383_075, entered_by_hand: false, note: "Line 109 less line 112." },
    { line_no: "114", label: "Refund claimed", amount_cents: 0, entered_by_hand: false, note: "Refund when line 113 is negative." },
    { line_no: "115", label: "Amount owing", amount_cents: 383_075, entered_by_hand: false, note: "Balance due when line 113 is positive." },
  ],
};

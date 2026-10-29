// The statements slice's plain logic: how cents read out, how the books' own periods are named, how the
// core's answers are read (and refused when they are not the shape the contract sends), the CSV a
// statement exports, and how a refusal reads out loud. No money is computed here; the cases hold words.
import { test } from "node:test";
import assert from "node:assert/strict";
import { centsDecimal, formatChange, formatMoney, MONEY_PLACEHOLDER } from "../src/finstmt/finstmt-money.ts";
import {
  accountTypeWord,
  bucketWord,
  cadenceWord,
  comparativeRows,
  csvDocument,
  csvFilename,
  defangCsv,
  parseCsv,
  periodLabel,
  periodYears,
  readAging,
  readBalance,
  readComparative,
  readGlDetail,
  readIncome,
  readPeriods,
  readSchedules,
  readTrial,
  refusalWords,
  reportWord,
  scopeLabel,
  validatePack,
} from "../src/finstmt/finstmt-logic.ts";
import { sourceWord } from "../src/shared/source-words.ts";

const INCOME = {
  year: 2000, month: 0, branch_id: "",
  revenue: [{ account_code: "4000", account_name: "Product Revenue", account_type: "REVENUE", amount_cents: 1200000 }],
  expenses: [{ account_code: "5000", account_name: "Cost of Goods Sold", account_type: "EXPENSE", amount_cents: 700000 }],
  revenue_cents: 1200000, expense_cents: 700000, net_income_cents: 500000,
};

test("cents read as dollars, and a missing figure is never a plausible zero", () => {
  assert.equal(formatMoney(750000), "$7,500.00");
  assert.equal(formatMoney(-1250), "-$12.50");
  assert.equal(formatMoney(0), "$0.00");
  assert.equal(formatMoney(undefined), MONEY_PLACEHOLDER);
  assert.equal(formatMoney(12.5), MONEY_PLACEHOLDER);
});

test("a change between two years is signed for the eye: up with a plus, down with a minus, and a missing figure is a placeholder", () => {
  assert.equal(formatChange(500), "+$5.00");
  assert.equal(formatChange(-500), "-$5.00");
  assert.equal(formatChange(0), "$0.00");
  assert.equal(formatChange(null), MONEY_PLACEHOLDER);
});

test("the plain decimal a spreadsheet reads is cut from the digits, with the sign kept and no grouping", () => {
  assert.equal(centsDecimal(123456), "1234.56");
  assert.equal(centsDecimal(5), "0.05");
  assert.equal(centsDecimal(0), "0.00");
  assert.equal(centsDecimal(-5), "-0.05");
  assert.equal(centsDecimal(-1234500), "-12345.00");
  assert.equal(centsDecimal(Number.MAX_SAFE_INTEGER), "90071992547409.91");
  assert.equal(centsDecimal(1.5), "");
});

test("periods are named by the books' own words: a fiscal year, or its numbered period", () => {
  assert.equal(periodLabel(2031, 0), "Fiscal year 2031");
  assert.equal(periodLabel(2031, 3), "Period 3 of fiscal year 2031");
  assert.deepEqual(periodYears([{ id: "a", year: 2031, month: 2, status: "OPEN" }, { id: "b", year: 2033, month: 1, status: "OPEN" }, { id: "c", year: 2031, month: 1, status: "CLOSED" }]), [2033, 2031]);
  assert.deepEqual(periodYears([]), []);
});

test("a branch reads by its name, the empty id reads all branches, and an id the list lacks is never shown bare", () => {
  const branches = [{ id: "b1", name: "Point Loma" }];
  assert.equal(scopeLabel("", branches), "All branches");
  assert.equal(scopeLabel("b1", branches), "Point Loma");
  assert.equal(scopeLabel("zz", branches), "A branch not in the list");
});

test("the closed vocabularies read as trade words, and an unknown value is opened out, never hidden", () => {
  assert.equal(accountTypeWord("REVENUE"), "Revenue");
  assert.equal(accountTypeWord("MYSTERY_KIND"), "Mystery kind");
  assert.equal(bucketWord("CURRENT"), "Current");
  assert.equal(bucketWord("1_30"), "1 to 30 days past due");
  assert.equal(bucketWord("90_PLUS"), "Over 90 days past due");
  assert.equal(sourceWord("PO_RECEIPT"), "Purchase receipt");
  assert.equal(sourceWord("CLOSING"), "Year end closing");
  assert.equal(reportWord("AR_AGING"), "Receivables aging");
  assert.equal(reportWord("GL_DETAIL"), "General ledger detail");
  assert.equal(cadenceWord("WEEKLY"), "Weekly");
});

test("each answer is read as the contract sends it, and anything else is refused whole", () => {
  assert.deepEqual(readIncome(INCOME), INCOME);
  assert.equal(readIncome({ ...INCOME, revenue: "none" }), null);
  assert.equal(readIncome({ ...INCOME, net_income_cents: 12.5 }), null);
  assert.equal(readIncome(null), null);
  assert.equal(readIncome({ ...INCOME, revenue: [{ account_code: "4000" }] }), null);
  const balance = { year: 2000, branch_id: "", assets: [], liabilities: [], equity: [], total_assets_cents: 0, total_liabilities_cents: 0, total_equity_cents: 0, net_income_cents: 0, balanced: true };
  assert.deepEqual(readBalance(balance), balance);
  assert.equal(readBalance({ ...balance, balanced: "yes" }), null);
  const trial = { year: 2000, rows: [{ account_code: "1000", account_name: "Cash", account_type: "ASSET", normal_balance: "DEBIT", total_debit_cents: 5, total_credit_cents: 0, balance_cents: 5 }], total_debit_cents: 5, total_credit_cents: 5, in_balance: true };
  assert.deepEqual(readTrial(trial), trial);
  assert.equal(readTrial({ ...trial, rows: [{ account_code: "1000" }] }), null);
  const compare = { rows: [{ account_code: "4000", account_name: "Product Revenue", account_type: "REVENUE", current_cents: 9, prior_cents: 4, delta_cents: 5 }] };
  assert.deepEqual(readComparative(compare), compare);
  assert.equal(readComparative({ rows: [{ account_code: "4000", current_cents: "9" }] }), null);
  assert.equal(readComparative({ rows: null }), null);
  const detail = { rows: [], tie_cents: 0 };
  assert.deepEqual(readGlDetail(detail), detail);
  assert.equal(readGlDetail({ rows: [] }), null);
  const aging = { branch_id: "", buckets: [{ bucket: "CURRENT", cents: 4 }], rows: [], grand_total_cents: 4 };
  assert.deepEqual(readAging(aging), aging);
  assert.equal(readAging({ ...aging, buckets: [{ bucket: "CURRENT", cents: "4" }] }), null);
});

test("the schedules list is read as the contract's page or as the key the core writes today, and nothing else", () => {
  const row = { id: "s1", report: "AR_AGING", recipients: "a@b.example", cadence: "WEEKLY", branch_id: "", is_active: true };
  assert.deepEqual(readSchedules({ items: [row], next_cursor: null }), [row]);
  assert.deepEqual(readSchedules({ schedules: [row] }), [row]);
  assert.deepEqual(readSchedules({ schedules: [] }), []);
  assert.equal(readSchedules({ items: [{ id: "s1" }] }), null);
  assert.equal(readSchedules({ rows: [row] }), null);
  assert.equal(readSchedules([row]), null);
  assert.deepEqual(readPeriods({ items: [{ id: "a", year: 2031, month: 1, status: "OPEN" }] }), [{ id: "a", year: 2031, month: 1, status: "OPEN" }]);
  assert.equal(readPeriods({ items: [{ id: "a", year: "2031" }] }), null);
});

test("a pack is checked before anything is sent, with the words the core uses", () => {
  const ok = validatePack({ report: "INCOME_STATEMENT", recipients: " a@b.example , c@d.example ", cadence: "MONTHLY", branchId: "", active: true });
  assert.deepEqual(ok, { ok: true, input: { report: "INCOME_STATEMENT", recipients: "a@b.example,c@d.example", cadence: "MONTHLY", branch_id: "", is_active: true } });
  assert.deepEqual(validatePack({ report: "NOPE", recipients: "a@b.example", cadence: "DAILY", branchId: "", active: true }), { ok: false, message: "Choose the statement the pack carries." });
  assert.deepEqual(validatePack({ report: "AR_AGING", recipients: "a@b.example", cadence: "", branchId: "", active: true }), { ok: false, message: "Choose how often the pack goes." });
  assert.deepEqual(validatePack({ report: "AR_AGING", recipients: "  ,  ", cadence: "DAILY", branchId: "", active: true }), { ok: false, message: "Give at least one recipient e-mail." });
  assert.deepEqual(validatePack({ report: "AR_AGING", recipients: "a@b.example, nobody", cadence: "DAILY", branchId: "", active: true }), { ok: false, message: "That is not an e-mail: nobody." });
});

test("a CSV is quoted the way a spreadsheet reads it, and a text cell that would run as a formula is defanged", () => {
  assert.equal(csvDocument([["Account", "Amount"], ["4000", "12.00"]]), "Account,Amount\r\n4000,12.00\r\n");
  assert.equal(csvDocument([["a,b", 'say "hi"', "two\nlines"]]), '"a,b","say ""hi""","two\nlines"\r\n');
  assert.equal(csvDocument([["-12.50", "-0.05"]]), "-12.50,-0.05\r\n");
  assert.equal(csvDocument([["=SUM(A1)", "+1", "@x", "-y", "fine"]]), "'=SUM(A1),'+1,'@x,'-y,fine\r\n");
});

test("only the app's own decimal shape is left alone: any other cell that starts like a formula is defanged", () => {
  assert.equal(csvDocument([["-2+3+cmd|' /C calc'!A0", "\t=1+1", "\rx", "-12.50", "12.50", "-5"]]), "'-2+3+cmd|' /C calc'!A0,'\t=1+1,\"'\rx\",-12.50,12.50,'-5\r\n");
});

test("a CSV text is read back into rows the way a spreadsheet reads it, quotes and line breaks inside a cell kept", () => {
  assert.deepEqual(parseCsv('Title\r\nA,B\r\n"a,b","say ""hi""","two\nlines"\r\n,,x\r\n'), [["Title"], ["A", "B"], ["a,b", 'say "hi"', "two\nlines"], ["", "", "x"]]);
  assert.deepEqual(parseCsv(""), []);
  assert.equal(defangCsv('a,"=1+1"\n-3.00,x\n'), "a,'=1+1\r\n-3.00,x\r\n");
});

test("the comparison exports the server's figures", () => {
  assert.deepEqual(comparativeRows([{ account_code: "4000", account_name: "Product Revenue", account_type: "REVENUE", current_cents: 900, prior_cents: 400, delta_cents: 500 }]), [
    ["Account", "Name", "This year", "Last year", "Change"],
    ["4000", "Product Revenue", "9.00", "4.00", "5.00"],
  ]);
});

test("a file is named for the statement and the period it covers", () => {
  assert.equal(csvFilename("income", 2031, 0), "income-statement-fiscal-year-2031.csv");
  assert.equal(csvFilename("income", 2031, 3), "income-statement-fiscal-year-2031-period-3.csv");
  assert.equal(csvFilename("balance", 2031, 3), "balance-sheet-fiscal-year-2031.csv");
  assert.equal(csvFilename("aging", 2031, 3), "receivables-aging.csv");
  assert.equal(csvFilename("trial", 2031, 0), "trial-balance-fiscal-year-2031.csv");
  assert.equal(csvFilename("gl", 2031, 0), "general-ledger-detail-fiscal-year-2031.csv");
});

test("a refusal names who may do it, never the bare gate word, and keeps the core's own sentence otherwise", () => {
  assert.equal(refusalWords("read", "Forbidden", 403), "Statements are for the Financial Controller or a manager.");
  assert.equal(refusalWords("read", "finstmt: caller cannot access this branch", 403), "You cannot read the books of that branch. Choose a branch you hold.");
  assert.equal(refusalWords("pack", "Forbidden", 403), "Saving a pack is for the Financial Controller.");
  assert.equal(refusalWords("read", "year is required", 400), "Year is required.");
  assert.equal(refusalWords("pack", "invalid recipient e-mail: \"x\"", 400), "Invalid recipient e-mail: \"x\".");
  assert.equal(refusalWords("pack", "not found", 404), "That pack is gone. Reload to see the list.");
  assert.equal(refusalWords("read", "", 500), "The books did not answer. Try again.");
});

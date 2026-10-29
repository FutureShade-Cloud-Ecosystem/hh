// The words the desk says for a source, in one place. The ledger's register names the source of a
// journal and the statements' rows name the source of a line: one source, one word. A desk that
// says two words for one source is a desk that has to be read twice.
//
// This module is shared by the slices inside the Money Desk, so the words a source carries are
// written down once and imported by whoever reads them. Nothing here computes money and nothing
// here is rendered.

/** An unknown value is opened out, never hidden: "PO_RECEIPT" reads "Po receipt". */
export function openOut(value: string): string {
  const words = value.toLowerCase().replace(/_/g, " ").trim();
  return words === "" ? "" : words.charAt(0).toUpperCase() + words.slice(1);
}

const SOURCE_WORD: Record<string, string> = {
  MANUAL: "Manual",
  INVOICE: "Invoice",
  PAYMENT: "Payment",
  FULFILLMENT: "Fulfillment",
  PO_RECEIPT: "Purchase receipt",
  ADJUSTMENT: "Adjustment",
  CLOSING: "Year end closing",
};

/** The source a journal or a statement line came from, in words a bookkeeper reads. */
export function sourceWord(source: string): string {
  return SOURCE_WORD[source] ?? openOut(source);
}

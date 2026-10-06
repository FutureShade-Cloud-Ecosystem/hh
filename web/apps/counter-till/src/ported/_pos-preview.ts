// The till's pre-ring totals (decision log row 985): the ticket's own subtotal, tax and total as
// the ring will compute them, read from POST /api/v1/pos/sale-preview and never assembled in the
// browser. Pure helpers only (the terminal owns the fetch), unit-tested in _pos-preview.test.mjs and
// read through the browser legs in test/total-split.pw.ts.

export interface PreviewLine {
  product_id: string;
  qty_entered: number;
  uom_entered: string;
}

export interface PreviewBody {
  branch_id: string;
  customer_id: string;
  sale_type: string;
  fulfilment?: string;
  lines: PreviewLine[];
}

/** What the till knows about the ticket's totals: nothing asked, asked and waiting, answered, or unreachable. */
export type PreviewState =
  | { kind: 'idle' }
  | { kind: 'pending' }
  | { kind: 'ready'; subtotalCents: number; taxCents: number; totalCents: number }
  | { kind: 'unavailable' };

/** The preview's request for the ticket, shaped like the ring's own body minus every tender. */
export function previewBody(facts: {
  branchId: string;
  customerId: string;
  saleType: string;
  fulfilment: string;
  lines: readonly { productId: string; qty: number; uom: string }[];
}): PreviewBody | null {
  if (facts.lines.length === 0) return null;
  return {
    branch_id: facts.branchId,
    customer_id: facts.customerId,
    sale_type: facts.saleType,
    // The ring sends the fulfilment only on a COD sale; the preview follows it.
    fulfilment: facts.saleType === 'COD' ? facts.fulfilment : undefined,
    lines: facts.lines.map((l) => ({ product_id: l.productId, qty_entered: l.qty, uom_entered: l.uom })),
  };
}

/** The fingerprint of a request: a change in it is the reason to ask again. '' means nothing to ask. */
export function previewKey(body: PreviewBody | null): string {
  return body === null ? '' : JSON.stringify(body);
}

/** Read an answer; anything that is not three whole-cent figures is not an answer. */
export function readPreview(answer: unknown): PreviewState {
  if (answer === null || typeof answer !== 'object') return { kind: 'unavailable' };
  const a = answer as Record<string, unknown>;
  const { subtotal_cents: s, tax_cents: t, total_cents: n } = a;
  if (![s, t, n].every((v) => typeof v === 'number' && Number.isInteger(v))) return { kind: 'unavailable' };
  return { kind: 'ready', subtotalCents: s as number, taxCents: t as number, totalCents: n as number };
}

/** The words for the split refusal, from its fields (the server's `split_total_mismatch`). */
export function splitMismatchWords(
  err: { tendered_cents?: number; total_cents?: number; message?: string },
  money: (cents: number) => string,
): string {
  const { tendered_cents: tendered, total_cents: total } = err;
  if (Number.isInteger(tendered) && Number.isInteger(total)) {
    return `The tenders add up to ${money(tendered as number)}, but the sale total is ${money(total as number)}. Change the legs so they match.`;
  }
  return err.message && err.message !== '' ? err.message : 'The tenders do not add up to the sale total. Change the legs so they match.';
}

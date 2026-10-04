// The page's client: the one door to the share link's routes, through the
// generated api-client, every answer validated field by field before the page
// renders a word of it. Nothing here computes money or decides whether an
// acceptance may happen: the core holds every gate and answers the figures;
// this module reads those figures off the wire and refuses everything it
// cannot prove (a missing field is a refusal, never a default).
//
// No credential rides any call: the client is built with an empty base URL
// and NO header provider, because a customer on a share link holds no
// credential at all. The token in the path is the credential.
import { err, ok, type ApiClient, type Result } from "@hardscapehouse/api-client";
import { publicApi, staffProduct } from "@hardscapehouse/api-client";

export const QUOTE_STATUSES = ["DRAFT", "SENT", "ACCEPTED", "CONVERTED", "EXPIRED", "LOST"] as const;
export type QuoteStatus = (typeof QUOTE_STATUSES)[number];

export interface QuoteLineRef {
  lineNo: number;
  description: string;
  qtyEntered: string;
  uomEntered: string;
  unitPriceCents: number;
  lineTotalCents: number;
  /** Present when the line carries a discount; absent is honest. */
  discPct: number | null;
  preDiscountTotalCents: number | null;
  /** The thumbnail reference; absent is honest, and no image is the honest tile. */
  imageRef: string | null;
}

export interface QuoteViewRef {
  quoteNo: string;
  customerName: string;
  status: QuoteStatus;
  validUntil: string | null;
  expired: boolean;
  lines: QuoteLineRef[];
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  asOf: string;
  acceptable: boolean;
  customerNotes: string | null;
  depositRequired: boolean | null;
  depositPct: number | null;
  depositCents: number | null;
  includeImages: boolean | null;
  fulfillment: string | null;
  deliveryAddress: string | null;
  requestedDate: string | null;
  deliveryFeeCents: number | null;
}

export interface AcceptanceRef {
  quoteNo: string;
  typedName: string;
  acceptedAt: string;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
}

// -- field readers: a response field either proves itself or the answer is
// refused as data, like every other refusal in this app.

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function stringAt(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  return typeof value === "string" && value !== "" ? value : null;
}

function integerAt(record: Record<string, unknown>, key: string): number | null {
  const value = record[key];
  return typeof value === "number" && Number.isInteger(value) ? value : null;
}

function booleanAt(record: Record<string, unknown>, key: string): boolean | null {
  const value = record[key];
  return typeof value === "boolean" ? value : null;
}

/**
 * Field-by-field validation for the quote view, never a cast. Strict by the
 * file's own rule: a missing required field refuses the whole answer, so the
 * page never renders a half-known quote to the one person with no one beside
 * them to translate.
 */
export function validateQuoteView(value: unknown): QuoteViewRef | null {
  const record = value as unknown;
  if (!isRecord(record)) return null;
  const quoteNo = stringAt(record, "quote_no");
  const customerName = stringAt(record, "customer_name");
  const status = record["status"];
  const expired = booleanAt(record, "expired");
  const subtotalCents = integerAt(record, "subtotal_cents");
  const taxCents = integerAt(record, "tax_cents");
  const totalCents = integerAt(record, "total_cents");
  const asOf = stringAt(record, "as_of");
  const acceptable = booleanAt(record, "acceptable");
  if (
    quoteNo === null ||
    customerName === null ||
    typeof status !== "string" ||
    !QUOTE_STATUSES.includes(status as QuoteStatus) ||
    expired === null ||
    subtotalCents === null ||
    taxCents === null ||
    totalCents === null ||
    asOf === null ||
    acceptable === null
  ) {
    return null;
  }
  const rawLines = record["lines"];
  if (!Array.isArray(rawLines)) return null;
  const lines: QuoteLineRef[] = [];
  for (const line of rawLines) {
    const row = line as unknown;
    if (!isRecord(row)) return null;
    const lineNo = integerAt(row, "line_no");
    const description = stringAt(row, "description");
    const qtyEntered = stringAt(row, "qty_entered");
    const uomEntered = stringAt(row, "uom_entered");
    const unitPriceCents = integerAt(row, "unit_price_cents");
    const lineTotalCents = integerAt(row, "line_total_cents");
    if (
      lineNo === null ||
      description === null ||
      qtyEntered === null ||
      uomEntered === null ||
      unitPriceCents === null ||
      lineTotalCents === null
    ) {
      return null;
    }
    lines.push({
      lineNo,
      description,
      qtyEntered,
      uomEntered,
      unitPriceCents,
      lineTotalCents,
      discPct: integerAt(row, "disc_pct"),
      preDiscountTotalCents: integerAt(row, "pre_discount_total_cents"),
      imageRef: stringAt(row, "image_ref"),
    });
  }
  return {
    quoteNo,
    customerName,
    status: status as QuoteStatus,
    validUntil: stringAt(record, "valid_until"),
    expired,
    lines,
    subtotalCents,
    taxCents,
    totalCents,
    asOf,
    acceptable,
    customerNotes: stringAt(record, "customer_notes"),
    depositRequired: booleanAt(record, "deposit_required"),
    depositPct: integerAt(record, "deposit_pct"),
    depositCents: integerAt(record, "deposit_cents"),
    includeImages: booleanAt(record, "include_images"),
    fulfillment: stringAt(record, "fulfillment"),
    deliveryAddress: stringAt(record, "delivery_address"),
    requestedDate: stringAt(record, "requested_date"),
    deliveryFeeCents: integerAt(record, "delivery_fee_cents"),
  };
}

/**
 * Field-by-field validation for the acceptance echo, the customer's own
 * record of what they agreed to: every figure the confirmation shows is one
 * the server sealed, re-validated like the quote's own.
 */
export function validateAcceptance(value: unknown): AcceptanceRef | null {
  const record = value as unknown;
  if (!isRecord(record)) return null;
  const quoteNo = stringAt(record, "quote_no");
  const typedName = stringAt(record, "typed_name");
  const acceptedAt = stringAt(record, "accepted_at");
  const subtotalCents = integerAt(record, "subtotal_cents");
  const taxCents = integerAt(record, "tax_cents");
  const totalCents = integerAt(record, "total_cents");
  if (quoteNo === null || typedName === null || acceptedAt === null || subtotalCents === null || taxCents === null || totalCents === null) {
    return null;
  }
  return { quoteNo, typedName, acceptedAt, subtotalCents, taxCents, totalCents };
}

export class PublicQuoteClient {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The quote behind a share link token, validated field by field. */
  async viewQuote(token: string): Promise<Result<QuoteViewRef>> {
    const result = await publicApi.quotePublicViewQuote(this.#client, token);
    if (!result.ok) return result;
    const view = validateQuoteView(result.value);
    return view === null ? err("the quote on this link is not readable") : ok(view);
  }

  /** The typed name acceptance: the server seals the name, the figures and the moment. */
  async acceptQuote(token: string, typedName: string): Promise<Result<AcceptanceRef>> {
    const result = await publicApi.quotePublicAcceptQuote(this.#client, token, {
      typed_name: typedName,
      accepted: true,
    });
    if (!result.ok) return result;
    const acceptance = validateAcceptance(result.value);
    return acceptance === null ? err("the acceptance answer is not readable") : ok(acceptance);
  }

  /** A line thumbnail's bytes, through the generated image read. */
  async productImage(imageRef: string): Promise<Result<Uint8Array>> {
    return staffProduct.productGetImage(this.#client, imageRef);
  }
}

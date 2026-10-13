// The vendors area's plain logic: the checks made before a request is sent, which mirror the
// core's own validators so a person reads the sentence once, beside the field (the core stays the
// authority and its refusals are shown as it words them); the bodies the core expects; and the
// words for what is on file. Nothing is sent when a check fails.
//
// Port provenance: the vendor records, the ordering contact and the vendor's own codes follow the
// original app's purchasing console (repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab: the purchasing console and api screens); the words are HH's.
import type {
  PurchasingUpdateVendorRequest,
  PurchasingVendor,
  PurchasingVendorContactWrite,
  PurchasingVendorPage,
  PurchasingVendorSKUWrite,
} from "@hardscapehouse/api-client";
import { parseQty } from "../purch/purch-money.js";

/** The vendor list, which the core answers as {"items":null} when there is none. */
export function vendorRows(page: Partial<Omit<PurchasingVendorPage, "items">> & { items?: PurchasingVendor[] | null }): PurchasingVendor[] {
  return page.items ?? [];
}

/** The list the screen shows: matched on name or number, ignoring case; inactive vendors only when asked. */
export function filterVendors<T extends { vendor_no: string; name: string; is_active: boolean }>(rows: readonly T[], query: string, showInactive: boolean): T[] {
  const q = query.trim().toLowerCase();
  return rows.filter((v) => (showInactive || v.is_active) && (q === "" || v.name.toLowerCase().includes(q) || v.vendor_no.toLowerCase().includes(q)));
}

export type Check<T> = { ok: true; body: T } | { ok: false; words: string };

const fail = (words: string): { ok: false; words: string } => ({ ok: false, words });

// One address, as the core reads one: no spaces, no list, one "@" with text each side.
const ONE_ADDRESS = /^[^\s@,;<>]+@[^\s@,;<>]+$/;

export interface VendorText {
  readonly name: string;
  readonly terms: string;
  readonly email: string;
  /** Clear the email on file: the list never reads the email back, so clearing is its own act. */
  readonly clearEmail: boolean;
}

/**
 * A new vendor (current null) or a correction. A correction sends only what changed; a new vendor
 * sends what was written, the core filling the rest (30 days of terms, no email). The number is
 * minted by the core.
 */
export function vendorBody(form: VendorText, current: { name: string; payment_terms_days: number } | null): Check<PurchasingUpdateVendorRequest> {
  const name = form.name.trim();
  if (name === "") return fail("Vendor name is required.");
  if (name.length > 200) return fail("Vendor name is too long: 200 characters at most.");
  const body: PurchasingUpdateVendorRequest = {};
  if (current === null || name !== current.name) body.name = name;
  const termsText = form.terms.trim();
  if (termsText !== "") {
    const m = termsText.match(/^\d{1,3}$/);
    const days = m === null ? -1 : Number(termsText);
    if (days < 0 || days > 365) return fail("Write the payment terms as a whole number of days from 0 to 365.");
    if (current === null || days !== current.payment_terms_days) body.payment_terms_days = days;
  }
  const email = form.email.trim();
  if (email !== "") {
    if (!ONE_ADDRESS.test(email)) return fail("Write the order email as one email address.");
    body.order_email = email;
  } else if (form.clearEmail && current !== null) {
    body.order_email = "";
  }
  if (current !== null && Object.keys(body).length === 0) return fail("Nothing changed on this vendor yet.");
  return { ok: true, body };
}

export interface ContactText {
  readonly name: string;
  readonly email: string;
  readonly phone: string;
}

/** An ordering contact, as the core accepts one: at least an email or a phone, each within its size. */
export function contactBody(form: ContactText): Check<PurchasingVendorContactWrite> {
  const name = form.name.trim();
  const email = form.email.trim();
  const phone = form.phone.trim();
  if ([...name].length > 120) return fail("The contact name is at most 120 characters.");
  if (email.length > 254 || phone.length > 32) return fail("An ordering email is at most 254 characters and a phone at most 32.");
  if (email !== "" && (!email.includes("@") || /\s/.test(email))) return fail("That ordering email address is not an address.");
  if (email === "" && phone === "") return fail("An ordering contact needs an email or a phone: one of the two is what makes it a contact.");
  return { ok: true, body: { contact_name: name, email, phone } };
}

export interface MappingText {
  readonly productId: string;
  readonly code: string;
  readonly unit: string;
  readonly factor: string;
}

/** One of the vendor's own codes for a product of ours, with its unit when it sells in another one. */
export function mappingBody(form: MappingText): Check<PurchasingVendorSKUWrite> {
  const code = form.code.trim();
  const unit = form.unit.trim().toUpperCase();
  const factorText = form.factor.trim();
  if (form.productId === "") return fail("Pick the product this code is for.");
  if ([...code].length > 64) return fail("A vendor code is at most 64 characters.");
  if ((unit === "") !== (factorText === "")) return fail("A vendor unit needs the number of our units it holds, and the number needs the unit: one without the other cannot be counted.");
  let factor: number | undefined;
  if (factorText !== "") {
    const checked = parseQty(factorText);
    if (!checked.ok) return fail("Write how many of our units one vendor unit holds, as a number with four decimals at most.");
    factor = checked.value;
  }
  if (code === "" && unit === "") return fail("Write a vendor code or a vendor unit, or remove the mapping instead.");
  const body: PurchasingVendorSKUWrite = { product_id: form.productId };
  if (code !== "") body.vendor_sku = code;
  if (unit !== "") {
    body.vendor_uom = unit;
    body.qty_per_vendor_uom = factor;
  }
  return { ok: true, body };
}

/** The order number a vendor quoted back, asked for in words; "" when there is something to look up. */
export function refWords(ref: string): string {
  return ref.trim() === "" ? "Type the order number the vendor quoted." : "";
}

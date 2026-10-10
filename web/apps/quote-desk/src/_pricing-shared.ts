// Ported from hardscapeos_dibbits master 87d7941b app/src/pricing-shared.ts; adapted for HH.
//
// _pricing-shared.ts: the words and number handling every tab of the pricing panel
// shares (DOM-free, unit tested on the Node runner). Three decisions live here:
//
//   MONEY AND PERCENT MOVE BY DIGITS: a typed 1.005 is refused, never multiplied by
//     100 (that gives 100.49999999999999). The wire is integer cents and basis points;
//     the screen is two decimals; the two meet by string handling only.
//   CATEGORIES: the contract has no category list, so the six seeded categories are
//     named from their stable ids and anything else degrades to a short id, as the
//     ERP's console does. This is a stand-in until a categories read exists.
//   WORDS: one enum to label map per kind of value, so a scope reads the same in the
//     rules list, the filter and the retire confirmation.

/** The two-decimal reading of a typed number, as a scaled integer, or null. */
function scaled(text: string): number | null {
  const t = text.trim();
  const m = /^(\d+)(?:\.(\d{1,2}))?$|^\.(\d{1,2})$/.exec(t);
  if (m === null) return null;
  const whole = m[1] ?? "0";
  const frac = (m[2] ?? m[3] ?? "").padEnd(2, "0");
  const value = Number(whole) * 100 + Number(frac);
  return Number.isSafeInteger(value) ? value : null;
}

/** A typed percentage ("18", "18.5") to integer basis points, or null when it is not one. */
export function pctToBps(text: string): number | null {
  return scaled(text);
}

/** A typed dollar amount to integer cents, or null when it is not one. */
export function dollarsToCents(text: string): number | null {
  return scaled(text);
}

function twoDecimals(scaledValue: number): string {
  const sign = scaledValue < 0 ? "-" : "";
  const abs = Math.abs(scaledValue);
  return `${sign}${Math.trunc(abs / 100)}.${String(abs % 100).padStart(2, "0")}`;
}

/** Basis points as the edit box shows them: 3400 is "34.00". */
export function bpsText(bps: number): string {
  return twoDecimals(bps);
}

/** Cents as the edit box shows them: 1999 is "19.99". */
export function centsText(cents: number): string {
  return twoDecimals(cents);
}

/** Basis points as a reading: 3000 is "30.00%". */
export function formatPct(bps: number): string {
  return `${twoDecimals(bps)}%`;
}

/** Integer cents as money: 123456 is "$1,234.56". */
export function formatMoney(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  const abs = Math.abs(cents);
  const whole = Math.trunc(abs / 100).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${sign}$${whole}.${String(abs % 100).padStart(2, "0")}`;
}

/** Category ids the seed names, until the contract carries a category list. */
export const CATEGORY_NAMES: Readonly<Record<string, string>> = {
  "ca700000-0000-4000-9000-000000000001": "Pavers",
  "ca700000-0000-4000-9000-000000000002": "Retaining Walls",
  "ca700000-0000-4000-9000-000000000003": "Slabs & Steps",
  "ca700000-0000-4000-9000-000000000004": "Aggregates",
  "ca700000-0000-4000-9000-000000000005": "Accessories",
  "ca700000-0000-4000-9000-000000000006": "Bulk",
};

export function categoryLabel(id: string): string {
  return CATEGORY_NAMES[id] ?? "a category";
}

export function isKnownCategory(id: string): boolean {
  return id in CATEGORY_NAMES;
}

export const CUSTOMER_TYPES = ["CONTRACTOR", "RETAIL", "BUILDER", "GOVERNMENT", "INTERNAL"] as const;

const SCOPE_LABELS: Readonly<Record<string, string>> = {
  TIER_DEFAULT: "Tier default",
  TIER_CATEGORY: "Tier + category",
  CUSTOMER_CATEGORY: "Customer + category",
  CUSTOMER_PRODUCT: "Customer + product",
};

/** A rule scope for people; an unknown value passes through rather than vanishing. */
export function scopeLabel(scope: string): string {
  return SCOPE_LABELS[scope] ?? scope;
}

export const ENTITY_TYPES = ["pricing_rule", "category_policy", "customer_override", "quantity_break", "margin_enforcement"] as const;

const ENTITY_LABELS: Readonly<Record<string, string>> = {
  pricing_rule: "Pricing rule",
  category_policy: "Category policy",
  customer_override: "Customer override",
  quantity_break: "Quantity break",
  margin_enforcement: "Margin enforcement",
};

/** An audit entity for people; an unknown value passes through. */
export function entityLabel(entity: string): string {
  return ENTITY_LABELS[entity] ?? entity;
}

export const AUDIT_ACTIONS = ["CREATE", "UPDATE", "DELETE", "BYPASS"] as const;

const ACTION_WORDS: Readonly<Record<string, string>> = {
  CREATE: "Created",
  UPDATE: "Updated",
  DELETE: "Retired",
  BYPASS: "Bypassed",
};

/** An audit action for people; an unmapped action degrades to lower case words. */
export function auditActionWord(action: string): string {
  return ACTION_WORDS[action] ?? action.toLowerCase().replace(/_/g, " ");
}

const FIELD_WORDS: Readonly<Record<string, string>> = {
  floor_warning_confirmed: "Floor warning confirmed",
};

/** An audit field for people; the field names the log does not rename are shown as stored. */
export function auditFieldWord(field: string | undefined): string {
  if (field === undefined || field === "") return "";
  return FIELD_WORDS[field] ?? field;
}

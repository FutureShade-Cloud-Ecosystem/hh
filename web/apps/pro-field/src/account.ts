// The account words: the profile a person reads about themselves, the
// addresses the deliveries go to, and the one local check the password form
// makes before the server is asked. Every figure and every rule past these
// words is the server's.
import type { TenantAddress } from "@hardscapehouse/api-client";

const KIND_WORDS: Readonly<Record<string, string>> = { billing: "Billing", job_site: "Job site", delivery: "Delivery" };

/** The kind of an address as the words a person uses; a kind the contract does not ship reads as nothing. */
export function addressKindWords(kind: string): string {
  return Object.prototype.hasOwnProperty.call(KIND_WORDS, kind) ? KIND_WORDS[kind] : "";
}

/** One address row's readable words, for the tests and for nothing else. */
export function addressRowWords(address: TenantAddress): string {
  const where = [address.line1, address.line2].filter((line) => line !== "").join(", ");
  return [address.label, addressKindWords(address.kind), where, `${address.city}, ${address.province} ${address.postal}`].join(" ");
}

/** The one check made before the server is asked; everything else is the server's answer. */
export function passwordProblem(current: string, next: string, confirm: string): string {
  if (current === "" || next === "") return "Type your current password and a new one.";
  if (next !== confirm) return "The new passwords do not match.";
  return "";
}

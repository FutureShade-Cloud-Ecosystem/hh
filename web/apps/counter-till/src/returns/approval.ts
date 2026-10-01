// The refund approval, as the contract's 409 names it: the dealer's approval
// amount and the role keys that hold the approval, present exactly on the
// approval refusal (the error string "refund_approval_required"). The desk
// maps the role keys to the labels a person knows; a key this desk has no
// label for is shown as the key itself, because that is what the server said
// and inventing a friendlier word would be the confident wrong answer this
// app does not give.
import type { PosRefundApproval } from "@hardscapehouse/api-client";

/** Field by field, never a cast: the approval a refusal names, or null. */
export function validateRefundApproval(raw: unknown): PosRefundApproval | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  const amountCents = record["amount_cents"];
  const approvers = record["approvers"];
  if (typeof amountCents !== "number" || !Number.isSafeInteger(amountCents)) return null;
  if (!Array.isArray(approvers) || approvers.length === 0) return null;
  for (const role of approvers) {
    if (typeof role !== "string" || role === "") return null;
  }
  return { amount_cents: amountCents, approvers: approvers as string[] };
}

const ROLE_LABELS: Record<string, string> = {
  sales_manager: "Sales manager",
  branch_manager: "Branch manager",
};

export function approverLabels(roleKeys: readonly string[]): string[] {
  return roleKeys.map((key) => ROLE_LABELS[key] ?? key);
}

/**
 * The one time credential slot. The approver's token rests here between the
 * moment the dialog's card minted it and the moment the one return call's
 * header provider takes it: taken, it is gone, whatever the call's answer
 * turns out to be. It lives in memory alone, never in any storage, never in
 * a log, and the transport calls the provider exactly once per request, so
 * one send consumes one credential.
 */
export class OneTimeCredential {
  #token: string | null = null;

  set(token: string): void {
    this.#token = token;
  }

  take(): string | null {
    const token = this.#token;
    this.#token = null;
    return token;
  }

  drop(): void {
    this.#token = null;
  }
}

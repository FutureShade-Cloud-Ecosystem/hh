// Which kind the Approvals view opens on. The tabs are the closed kind
// vocabulary in the core's own order, and the view used to open on the first of
// them whatever was waiting, so a controller with two freight raises landed on
// an empty Margin tab and had to find Freight themselves. The core's whole list
// (an empty kind) is every request in the caller's branches, kinds the caller
// cannot decide included, so it only names what is waiting: the view tries the
// waiting kinds in vocabulary order and keeps the first one the core does not
// refuse (app-root.ts), else the first kind.
import { APPROVAL_KINDS } from "./manager-logic.js";
import type { ApprovalRow } from "./manager-api.js";

/** Every kind with a waiting row, in the vocabulary's order, once each. */
export function waitingKinds(rows: readonly Pick<ApprovalRow, "policy_type" | "status">[]): string[] {
  const waiting = new Set(rows.filter((r) => r.status === "PENDING").map((r) => r.policy_type));
  return APPROVAL_KINDS.filter((k) => waiting.has(k.kind)).map((k) => k.kind);
}

export function openingKind(rows: readonly Pick<ApprovalRow, "policy_type" | "status">[]): string {
  return waitingKinds(rows)[0] ?? APPROVAL_KINDS[0].kind;
}

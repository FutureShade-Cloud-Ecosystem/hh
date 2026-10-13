// Which kind the Approvals view opens on. The tabs are the closed kind
// vocabulary in the core's own order, and the view used to open on the first of
// them whatever was waiting, so a controller with two freight raises landed on
// an empty Margin tab and had to find Freight themselves. The whole list the
// caller decides (the core answers only those) names what is waiting; the view
// opens on the first kind in vocabulary order that has a waiting row.
import { APPROVAL_KINDS } from "./manager-logic.js";
import type { ApprovalRow } from "./manager-api.js";

export function openingKind(rows: readonly Pick<ApprovalRow, "policy_type" | "status">[]): string {
  const waiting = new Set(rows.filter((r) => r.status === "PENDING").map((r) => r.policy_type));
  return APPROVAL_KINDS.find((k) => waiting.has(k.kind))?.kind ?? APPROVAL_KINDS[0].kind;
}

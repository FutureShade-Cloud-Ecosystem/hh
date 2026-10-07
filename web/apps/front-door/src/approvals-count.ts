// The count behind the Quote Desk tile's note. It is the number the Quote
// Desk's Approvals tab shows: pending rows of the margin below minimum kind
// only. The core answers a bare array and the contract says a page object, so
// both shapes are read, every row field checked, and a page cursor is followed
// to its end.

/** The kind the Quote Desk's Approvals tab lists. */
export const MARGIN_POLICY = "MARGIN_BELOW_MINIMUM";

/** A hard stop on cursor following, so a server that never ends cannot hold the home. */
export const MAX_APPROVAL_PAGES = 50;

export interface ApprovalRow {
  readonly policy_type: string;
  readonly status: string;
}

export interface ApprovalsPage {
  readonly items: readonly ApprovalRow[];
  readonly nextCursor: string | undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Read either shape. Null when the answer is not readable; one bad row refuses the page. */
export function readApprovalsPage(value: unknown): ApprovalsPage | null {
  let rows: unknown[];
  let nextCursor: string | undefined;
  if (Array.isArray(value)) {
    rows = value;
  } else if (isRecord(value) && Array.isArray(value["items"])) {
    rows = value["items"];
    const cursor = value["next_cursor"];
    if (cursor !== undefined && cursor !== null) {
      if (typeof cursor !== "string") return null;
      if (cursor !== "") nextCursor = cursor;
    }
  } else {
    return null;
  }
  const items: ApprovalRow[] = [];
  for (const raw of rows) {
    if (!isRecord(raw)) return null;
    const policy = raw["policy_type"];
    const status = raw["status"];
    if (typeof policy !== "string" || typeof status !== "string") return null;
    items.push({ policy_type: policy, status });
  }
  return { items, nextCursor };
}

/**
 * Count the pending margin approvals, following every cursor. Null when any
 * page fails or is unreadable, so the caller shows no note rather than a short one.
 */
export async function countMarginApprovals(
  fetchPage: (cursor: string | undefined) => Promise<{ ok: boolean; value?: unknown }>,
): Promise<number | null> {
  let count = 0;
  const seen = new Set<string>();
  let cursor: string | undefined;
  for (let pages = 0; pages < MAX_APPROVAL_PAGES; pages += 1) {
    const result = await fetchPage(cursor);
    if (!result.ok) return null;
    const page = readApprovalsPage(result.value);
    if (page === null) return null;
    for (const row of page.items) {
      if (row.policy_type === MARGIN_POLICY && row.status === "PENDING") count += 1;
    }
    if (page.nextCursor === undefined) return count;
    if (seen.has(page.nextCursor)) return null;
    seen.add(page.nextCursor);
    cursor = page.nextCursor;
  }
  return null;
}

// _seam-wire.ts: the field readers for the third coder's panels (the assist
// panel, the follow-ups, the hold expiry status, the availability chips, the
// floor check and the approvals list). Each reader either proves every field
// it needs or answers null, and the client turns a null into a refusal in
// words: a missing field is a refusal, never a default. DOM-free so the Node
// runner can drive it. The shapes are the contract's own types.
import type {
  IdentityApproval,
  IdentityPolicyCheckResponse,
  QuoteAssistExtraction,
  QuoteAssistScopeGaps,
  QuoteBranchPeek,
  QuoteBranchStock,
  QuoteFloorApprovalAnswer,
  QuoteFollowUpHistory,
  QuoteHoldExpiryView,
  QuoteLineAvailability,
} from "@hardscapehouse/api-client";

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
function str(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  return typeof value === "string" && value !== "" ? value : null;
}
function text(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  return typeof value === "string" ? value : null;
}
function int(record: Record<string, unknown>, key: string): number | null {
  const value = record[key];
  return typeof value === "number" && Number.isInteger(value) ? value : null;
}
function num(record: Record<string, unknown>, key: string): number | null {
  const value = record[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}
function bool(record: Record<string, unknown>, key: string): boolean | null {
  const value = record[key];
  return typeof value === "boolean" ? value : null;
}
function list(record: Record<string, unknown>, key: string): unknown[] | null {
  const value = record[key];
  return Array.isArray(value) ? value : null;
}

const CONFIDENCE = new Set(["HIGH", "MEDIUM", "LOW"]);

/** The assist extraction: one unreadable proposal refuses the whole answer rather than showing it short. */
export function readExtraction(value: unknown): QuoteAssistExtraction | null {
  if (!isRecord(value)) return null;
  const proposalsRaw = list(value, "proposals");
  const unmatchedRaw = list(value, "unmatched");
  const source = str(value, "source");
  if (proposalsRaw === null || unmatchedRaw === null || (source !== "deterministic" && source !== "ai-assisted")) return null;
  const proposals: QuoteAssistExtraction["proposals"] = [];
  for (const raw of proposalsRaw) {
    if (!isRecord(raw)) return null;
    const ref = str(raw, "ref");
    const productId = str(raw, "product_id");
    const name = str(raw, "name");
    const sellUom = str(raw, "sell_uom");
    const baseUom = str(raw, "base_uom");
    const confidence = str(raw, "confidence");
    const needsQty = bool(raw, "needs_qty");
    const needsUom = bool(raw, "needs_uom");
    const qty = text(raw, "qty_entered");
    const uom = text(raw, "uom_entered");
    const sourceText = text(raw, "source_text");
    if (
      ref === null || productId === null || name === null || sellUom === null || baseUom === null ||
      confidence === null || !CONFIDENCE.has(confidence) || needsQty === null || needsUom === null ||
      qty === null || uom === null || sourceText === null
    ) {
      return null;
    }
    const alternatesRaw = list(raw, "alternates") ?? [];
    const alternates: QuoteAssistExtraction["proposals"][number]["alternates"] = [];
    for (const alt of alternatesRaw) {
      if (!isRecord(alt)) return null;
      const altId = str(alt, "product_id");
      const altName = str(alt, "name");
      const altSell = str(alt, "sell_uom");
      const altBase = str(alt, "base_uom");
      if (altId === null || altName === null || altSell === null || altBase === null) return null;
      alternates.push({
        product_id: altId,
        sku: text(alt, "sku") ?? "",
        name: altName,
        match_reason: text(alt, "match_reason") ?? "",
        sell_uom: altSell,
        base_uom: altBase,
      });
    }
    const why = list(raw, "why") ?? [];
    proposals.push({
      ref,
      source_text: sourceText,
      product_id: productId,
      sku: text(raw, "sku") ?? "",
      name,
      sell_uom: sellUom,
      base_uom: baseUom,
      qty_entered: qty,
      uom_entered: uom,
      ...(typeof raw["unit_note"] === "string" && raw["unit_note"] !== "" ? { unit_note: raw["unit_note"] } : {}),
      confidence: confidence as "HIGH" | "MEDIUM" | "LOW",
      match_reason: text(raw, "match_reason") ?? "",
      why: why.filter((w): w is string => typeof w === "string"),
      needs_qty: needsQty,
      needs_uom: needsUom,
      alternates,
    });
  }
  const unmatched: QuoteAssistExtraction["unmatched"] = [];
  for (const raw of unmatchedRaw) {
    if (!isRecord(raw)) return null;
    const sourceText = text(raw, "source_text");
    const reason = text(raw, "reason");
    if (sourceText === null || reason === null) return null;
    unmatched.push({ source_text: sourceText, reason, ...(typeof raw["query"] === "string" ? { query: raw["query"] } : {}) });
  }
  const note = text(value, "ai_note");
  return { proposals, unmatched, source, ...(note !== null && note !== "" ? { ai_note: note } : {}) };
}

/** The scope gaps: advice rows, each with its triggering lines and what is missing. */
export function readScopeGaps(value: unknown): QuoteAssistScopeGaps | null {
  if (!isRecord(value)) return null;
  const quoteId = str(value, "quote_id");
  const findingsRaw = list(value, "findings");
  const linesChecked = int(value, "lines_checked");
  const rulesEvaluated = int(value, "rules_evaluated");
  if (quoteId === null || findingsRaw === null || linesChecked === null || rulesEvaluated === null) return null;
  const findings: QuoteAssistScopeGaps["findings"] = [];
  for (const raw of findingsRaw) {
    if (!isRecord(raw)) return null;
    const ruleCode = str(raw, "rule_code");
    const title = str(raw, "title");
    const advice = text(raw, "advice");
    const triggeredRaw = list(raw, "triggered_by");
    const missingRaw = list(raw, "missing");
    if (ruleCode === null || title === null || advice === null || triggeredRaw === null || missingRaw === null) return null;
    const triggered: QuoteAssistScopeGaps["findings"][number]["triggered_by"] = [];
    for (const t of triggeredRaw) {
      if (!isRecord(t)) return null;
      const lineNo = int(t, "line_no");
      const name = str(t, "name");
      if (lineNo === null || name === null) return null;
      triggered.push({ line_no: lineNo, name });
    }
    findings.push({
      rule_code: ruleCode,
      title,
      advice,
      triggered_by: triggered,
      missing: missingRaw.filter((m): m is string => typeof m === "string"),
      suggest_query: text(raw, "suggest_query") ?? "",
    });
  }
  return { quote_id: quoteId, findings, lines_checked: linesChecked, rules_evaluated: rulesEvaluated };
}

const CHANNELS = new Set(["EMAIL", "SMS", "BOTH"]);
const EVENT_STATUS = new Set(["SENT", "SKIPPED", "CANCELLED"]);

/** The follow-up history: the plan when one exists, what went out, what is next. */
export function readFollowUps(value: unknown): QuoteFollowUpHistory | null {
  if (!isRecord(value)) return null;
  const quoteId = str(value, "quote_id");
  const eventsRaw = list(value, "events");
  const upcomingRaw = list(value, "upcoming");
  const automated = bool(value, "automated");
  if (quoteId === null || eventsRaw === null || upcomingRaw === null || automated === null) return null;
  let plan: QuoteFollowUpHistory["plan"];
  const planRaw = value["plan"];
  if (planRaw !== undefined && planRaw !== null) {
    if (!isRecord(planRaw)) return null;
    const channel = str(planRaw, "channel");
    const enabled = bool(planRaw, "enabled");
    const startedAt = str(planRaw, "started_at");
    const offsetsRaw = list(planRaw, "offsets_days");
    if (channel === null || !CHANNELS.has(channel) || enabled === null || startedAt === null || offsetsRaw === null) return null;
    const offsets = offsetsRaw.filter((n): n is number => typeof n === "number" && Number.isInteger(n));
    if (offsets.length !== offsetsRaw.length) return null;
    plan = {
      quote_id: quoteId,
      enabled,
      channel: channel as "EMAIL" | "SMS" | "BOTH",
      offsets_days: offsets,
      started_at: startedAt,
      ...(typeof planRaw["disabled_at"] === "string" ? { disabled_at: planRaw["disabled_at"] } : {}),
      ...(typeof planRaw["disabled_why"] === "string" ? { disabled_why: planRaw["disabled_why"] } : {}),
    };
  }
  const events: QuoteFollowUpHistory["events"] = [];
  for (const raw of eventsRaw) {
    if (!isRecord(raw)) return null;
    const stepDay = int(raw, "step_day");
    const channel = str(raw, "channel");
    const status = str(raw, "status");
    const scheduledFor = str(raw, "scheduled_for");
    const occurredAt = str(raw, "occurred_at");
    if (
      stepDay === null || channel === null || !CHANNELS.has(channel) || status === null || !EVENT_STATUS.has(status) ||
      scheduledFor === null || occurredAt === null
    ) {
      return null;
    }
    events.push({
      step_day: stepDay,
      channel: channel as "EMAIL" | "SMS" | "BOTH",
      status: status as "SENT" | "SKIPPED" | "CANCELLED",
      ...(typeof raw["reason"] === "string" ? { reason: raw["reason"] } : {}),
      scheduled_for: scheduledFor,
      occurred_at: occurredAt,
    });
  }
  const upcoming: QuoteFollowUpHistory["upcoming"] = [];
  for (const raw of upcomingRaw) {
    if (!isRecord(raw)) return null;
    const stepDay = int(raw, "step_day");
    const channel = str(raw, "channel");
    const dueAt = str(raw, "due_at");
    const scheduled = bool(raw, "scheduled");
    if (stepDay === null || channel === null || !CHANNELS.has(channel) || dueAt === null || scheduled === null) return null;
    upcoming.push({ step_day: stepDay, channel: channel as "EMAIL" | "SMS" | "BOTH", due_at: dueAt, scheduled });
  }
  return { quote_id: quoteId, ...(plan !== undefined ? { plan } : {}), events, upcoming, automated };
}

/** The hold expiry status. */
export function readHoldExpiry(value: unknown): QuoteHoldExpiryView | null {
  if (!isRecord(value)) return null;
  const expiry = value["expiry"];
  const seamWired = bool(value, "seam_wired");
  if (!isRecord(expiry) || seamWired === null) return null;
  const quoteId = str(expiry, "quote_id");
  const activeCount = int(expiry, "active_count");
  const expiredRaw = list(expiry, "expired");
  const anyExpired = bool(expiry, "any_expired");
  if (quoteId === null || activeCount === null || expiredRaw === null || anyExpired === null) return null;
  const expired: QuoteHoldExpiryView["expiry"]["expired"] = [];
  for (const raw of expiredRaw) {
    if (!isRecord(raw)) return null;
    const reservationId = str(raw, "reservation_id");
    const productId = str(raw, "product_id");
    const productName = str(raw, "product_name");
    const branchId = str(raw, "branch_id");
    const qtyBase = num(raw, "qty_base");
    const status = str(raw, "status");
    if (reservationId === null || productId === null || productName === null || branchId === null || qtyBase === null) return null;
    if (status !== "EXPIRED" && status !== "RELEASED") return null;
    expired.push({
      reservation_id: reservationId,
      product_id: productId,
      product_name: productName,
      sku: text(raw, "sku") ?? "",
      branch_id: branchId,
      qty_base: qtyBase,
      status,
      ...(typeof raw["expires_at"] === "string" ? { expires_at: raw["expires_at"] } : {}),
      ...(typeof raw["ended_at"] === "string" ? { ended_at: raw["ended_at"] } : {}),
    });
  }
  return { expiry: { quote_id: quoteId, active_count: activeCount, expired, any_expired: anyExpired }, seam_wired: seamWired };
}

/** Holds applied by a re-hold. */
export function readHoldsApplied(value: unknown): number | null {
  return isRecord(value) ? int(value, "holds_applied") : null;
}

function readBranchStock(raw: unknown): QuoteBranchStock | null {
  if (!isRecord(raw)) return null;
  const productId = str(raw, "product_id");
  const branchId = str(raw, "branch_id");
  const branchCode = str(raw, "branch_code");
  const branchName = str(raw, "branch_name");
  const onHand = num(raw, "on_hand_base");
  const reserved = num(raw, "reserved_base");
  const inTransit = num(raw, "in_transit_base");
  const available = num(raw, "available_base");
  const softHeld = num(raw, "soft_held_base");
  if (
    productId === null || branchId === null || branchCode === null || branchName === null || onHand === null ||
    reserved === null || inTransit === null || available === null || softHeld === null
  ) {
    return null;
  }
  // The average cost rides only for a caller on the cost list; it is passed through, never defaulted.
  return {
    product_id: productId,
    branch_id: branchId,
    branch_code: branchCode,
    branch_name: branchName,
    on_hand_base: onHand,
    reserved_base: reserved,
    in_transit_base: inTransit,
    available_base: available,
    soft_held_base: softHeld,
    ...(int(raw, "avg_cost_cents") !== null ? { avg_cost_cents: int(raw, "avg_cost_cents") as number } : {}),
  };
}

const GRADES = new Set(["GREEN", "THIN", "NONE", "UNKNOWN"]);

/** The per line availability list. */
export function readAvailability(value: unknown): { lines: QuoteLineAvailability[]; seamWired: boolean } | null {
  if (!isRecord(value)) return null;
  const linesRaw = list(value, "lines");
  const seamWired = bool(value, "seam_wired");
  if (linesRaw === null || seamWired === null) return null;
  const lines: QuoteLineAvailability[] = [];
  for (const raw of linesRaw) {
    if (!isRecord(raw)) return null;
    const lineNo = int(raw, "line_no");
    const productId = str(raw, "product_id");
    const qtyBase = num(raw, "qty_base");
    const grade = str(raw, "grade");
    const branch = readBranchStock(raw["branch"]);
    if (lineNo === null || productId === null || qtyBase === null || grade === null || !GRADES.has(grade) || branch === null) return null;
    lines.push({ line_no: lineNo, product_id: productId, qty_base: qtyBase, grade: grade as QuoteLineAvailability["grade"], branch });
  }
  return { lines, seamWired };
}

/** One row of the cross branch peek: quantities only, so a cost the wire should not carry is dropped, never passed on. */
function readBranchPeek(raw: unknown): QuoteBranchPeek | null {
  const row = readBranchStock(raw);
  if (row === null) return null;
  const { avg_cost_cents: _dropped, ...quantities } = row;
  return quantities;
}

/** The cross branch peek behind a short line. */
export function readBranchPeeks(value: unknown): QuoteBranchPeek[] | null {
  if (!isRecord(value)) return null;
  const rows = list(value, "branches");
  if (rows === null) return null;
  const out: QuoteBranchPeek[] = [];
  for (const raw of rows) {
    const row = readBranchPeek(raw);
    if (row === null) return null;
    out.push(row);
  }
  return out;
}

/** The floor check's answer. */
export function readPolicyCheck(value: unknown): IdentityPolicyCheckResponse | null {
  if (!isRecord(value)) return null;
  const allowed = bool(value, "allowed");
  const marginBps = int(value, "margin_bps");
  const override = bool(value, "override");
  if (allowed === null || marginBps === null || override === null) return null;
  return { allowed, margin_bps: marginBps, override };
}

/** The filing answer: whether it filed, whether the caller may clear its own price, and the request's state. */
export function readFloorAnswer(value: unknown): QuoteFloorApprovalAnswer | null {
  if (!isRecord(value)) return null;
  const filed = bool(value, "filed");
  const mayClear = bool(value, "may_clear_itself");
  if (filed === null || mayClear === null) return null;
  let request: QuoteFloorApprovalAnswer["request"];
  const raw = value["request"];
  if (raw !== undefined && raw !== null) {
    if (!isRecord(raw)) return null;
    const id = str(raw, "id");
    const status = str(raw, "status");
    const lineNo = int(raw, "line_no");
    const disc = num(raw, "disc_pct");
    if (id === null || (status !== "PENDING" && status !== "APPROVED" && status !== "REJECTED") || lineNo === null || disc === null) return null;
    request = { id, status, line_no: lineNo, disc_pct: disc };
  }
  return { filed, may_clear_itself: mayClear, ...(request !== undefined ? { request } : {}) };
}

const POLICY_TYPES = new Set(["MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"]);

/** One page of approvals: the rows and the cursor to the next page, when the server sent one. */
export interface ApprovalsPage {
  items: IdentityApproval[];
  nextCursor?: string;
}

/**
 * The approvals the server returns, in either shape. The contract says the
 * list answers an IdentityApprovalPage ({items, next_cursor}); the core's
 * handler (staffcontext/approvals_handler.go listApprovals) writes a BARE JSON
 * array. The two disagree, so the reader takes both: a bare array, or the page
 * object with items and an optional next_cursor. Every row is validated field
 * by field, and one unreadable row refuses the page rather than showing it short.
 */
export function readApprovalsPage(value: unknown): ApprovalsPage | null {
  let items: unknown[];
  let nextCursor: string | undefined;
  if (Array.isArray(value)) {
    items = value;
  } else if (isRecord(value)) {
    const rows = list(value, "items");
    if (rows === null) return null;
    items = rows;
    const cursor = value["next_cursor"];
    if (cursor !== undefined && cursor !== null) {
      if (typeof cursor !== "string") return null;
      if (cursor !== "") nextCursor = cursor;
    }
  } else {
    return null;
  }
  const out: IdentityApproval[] = [];
  for (const raw of items) {
    if (!isRecord(raw)) return null;
    const id = str(raw, "id");
    const requestedBy = str(raw, "requested_by");
    const branchId = str(raw, "branch_id");
    const policy = str(raw, "policy_type");
    const detail = text(raw, "detail");
    const status = str(raw, "status");
    if (
      id === null || requestedBy === null || branchId === null || policy === null || !POLICY_TYPES.has(policy) ||
      detail === null || (status !== "PENDING" && status !== "APPROVED" && status !== "REJECTED")
    ) {
      return null;
    }
    out.push({
      id,
      requested_by: requestedBy,
      branch_id: branchId,
      policy_type: policy as IdentityApproval["policy_type"],
      detail,
      ...(typeof raw["subject_ref"] === "string" ? { subject_ref: raw["subject_ref"] } : {}),
      status,
      ...(typeof raw["decided_by"] === "string" ? { decided_by: raw["decided_by"] } : {}),
    });
  }
  return { items: out, ...(nextCursor !== undefined ? { nextCursor } : {}) };
}

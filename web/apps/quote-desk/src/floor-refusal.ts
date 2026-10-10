// floor-refusal.ts: the typed 409 the floor decision answers, on this surface.
//
// QD-1's contract gives the below-floor refusal a typed body (QuoteFloorRefusal:
// the policy, who may approve, whether a manager of the branch may set it, the
// caller's standing request, and the margin and floor figures for callers on
// the cost and margin list only). The generated transport flattens every
// refusal to its error string; for the typed body there is no error field, so
// the string the transport carries IS the raw body text. This module parses
// that text back into the typed shape, validating field by field like every
// other read in this app, and refuses anything it cannot prove. The work area
// surfaces the plain message; the refusal DIALOG is the third coder's panel,
// and the seam at the bottom of this file is where it opens.
import { marginPctLabel } from "./_quote-workarea.js";

/** The typed refusal as this app reads it, every field proven. */
export interface FloorRefusalView {
  policy: string;
  approverRoles: string[];
  managerMaySet: boolean;
  requestId: string | null;
  requestStatus: string | null;
  marginBps: number | null;
  floorBps: number | null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/**
 * parseFloorRefusal: the typed body out of the flattened refusal text. The
 * required fields must prove themselves (the policy as a string, the
 * approver roles as a list of strings, the manager way forward as a
 * boolean); the figures are optional exactly as the contract makes them
 * optional (cost list callers only), and anything else is not this refusal,
 * so the caller shows the server's own sentence instead. An envelope that
 * names an error is a sentence, not a typed body, and says so by answering
 * null.
 */
export function parseFloorRefusal(text: string): FloorRefusalView | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return null;
  }
  if (!isRecord(parsed)) return null;
  if (typeof parsed["error"] === "string") return null;
  const policy = parsed["policy"];
  const approvers = parsed["approver_roles"];
  const managerMaySet = parsed["manager_may_set"];
  if (typeof policy !== "string" || policy === "" || !Array.isArray(approvers) || typeof managerMaySet !== "boolean") {
    return null;
  }
  const roles: string[] = [];
  for (const role of approvers) {
    if (typeof role !== "string" || role === "") return null;
    roles.push(role);
  }
  if (roles.length === 0) return null;
  const request = parsed["existing_request"];
  let requestId: string | null = null;
  let requestStatus: string | null = null;
  if (isRecord(request)) {
    const id = request["id"];
    const status = request["status"];
    if (typeof id === "string" && typeof status === "string") {
      requestId = id;
      requestStatus = status;
    }
  }
  const marginBps = parsed["margin_bps"];
  const floorBps = parsed["floor_bps"];
  return {
    policy,
    approverRoles: roles,
    managerMaySet,
    requestId,
    requestStatus,
    marginBps: typeof marginBps === "number" && Number.isInteger(marginBps) ? marginBps : null,
    floorBps: typeof floorBps === "number" && Number.isInteger(floorBps) ? floorBps : null,
  };
}

/**
 * floorRefusalMessage: the typed refusal spoken for a counter person. The
 * way forward is the body's own: who may approve (the General Manager, the
 * Financial Controller, and since CP0-1 answer B the branch's Sales
 * manager), and that the branch's Sales manager may set the price itself
 * when the body says so. The figures ride only when the server sent them
 * (cost list callers); a caller with no figures reads the same sentence
 * without them, never a guessed number.
 */
export function floorRefusalMessage(refusal: FloorRefusalView): string {
  const spoken = refusal.approverRoles.map((role) => (role.startsWith("the ") ? role : `the ${role}`));
  const joined = spoken.join(", ").replace(/, ([^,]*)$/, " or $1");
  let message = `This discount puts the line below the margin floor. Ask ${joined} to approve it`;
  if (refusal.managerMaySet) {
    message += ", or have the branch's Sales manager set the price itself";
  }
  message += ".";
  if (refusal.marginBps !== null && refusal.floorBps !== null) {
    message += ` The margin at this discount is ${marginPctLabel(refusal.marginBps)} against a floor of ${marginPctLabel(refusal.floorBps)}.`;
  }
  if (refusal.requestStatus !== null) {
    message += ` Your request for this line is ${refusal.requestStatus.toLowerCase()}.`;
  }
  return message;
}

/**
 * THE SEAM FOR THE THIRD CODER (the refusal dialog). The work area calls
 * this the moment a typed refusal arrives, beside showing the plain message
 * above. The third coder's panel (the guard dialog that names the policy and
 * files the request through the client's requestFloorApproval door) replaces
 * this body; the signature carries everything it needs: the typed refusal,
 * the quote, the line and the discount that was asked for. Until then the
 * seam records the refusal where a test can see it and stays quiet: an
 * honest empty seam, never a faked dialog.
 */
export interface FloorRefusalSeamEvent {
  refusal: FloorRefusalView;
  quoteId: string;
  lineNo: number;
  discPct: number;
}

export const FLOOR_REFUSAL_SEAM_EVENT = "qd-floor-refusal";

/** The dialog hands the host a refusal it did not open (its quote is not on screen): { quoteId, lineNo }. */
export const FLOOR_REFUSAL_DROPPED_EVENT = "qd-floor-refusal-dropped";

/** A write the server refused after the person had left its quote: the host says it on the list, and the quote says it when they return. */
export const LATE_REFUSAL_EVENT = "qd-late-refusal";

/**
 * The window event the dialog fires when it closes, so the surface that
 * spoke the refusal's plain message in its outcome line can take it down: the
 * dialog has said the same words, and a line left under the actions after it
 * closes only repeats them.
 */
export const FLOOR_REFUSAL_CLOSED_EVENT = "qd-floor-refusal-closed";

export function openFloorRefusalDialog(refusal: FloorRefusalView, quoteId: string, lineNo: number, discPct: number): void {
  const detail: FloorRefusalSeamEvent = { refusal, quoteId, lineNo, discPct };
  window.dispatchEvent(new CustomEvent<FloorRefusalSeamEvent>(FLOOR_REFUSAL_SEAM_EVENT, { detail }));
}

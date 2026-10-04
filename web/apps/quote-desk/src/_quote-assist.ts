// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-assist.ts;
// adapted for HH.
//
// _quote-assist.ts: the decisions behind the assist panel (DOM-free, unit
// tested on the Node runner). The element renders these answers and owns no
// rule of its own. Five of them decide whether a rep is told the truth and
// whether they stay in control:
//
//   WHAT IS CONFIRMABLE      a proposal missing a quantity or a unit cannot be confirmed
//   WHAT IS SELECTED         nothing, until the rep says so (selectionOf)
//   WHAT GETS SENT           one add per CONFIRMED row, and only confirmed rows
//   WHICH SUGGESTIONS SHOW   the ones not dismissed, and none when there are none
//   WHAT A SCREEN READER HEARS  an aria-live sentence for each of the two lists
//
// THE INVARIANT THIS MODULE EXISTS TO HOLD: extraction never adds a line, a
// rep does. The confirm state starts EMPTY, the ticked refs are the only thing
// addPayloads reads, and no path here returns a payload for a row nobody
// ticked. A scope gap is ADVICE, not a fault: the element renders findings as
// role="status", never role="alert", and each is dismissible.
//
// The shapes are the contract's own types. Nothing here computes money.
import type {
  QuoteAssistAlternate,
  QuoteAssistExtraction,
  QuoteAssistFinding,
  QuoteAssistProposal,
  QuoteAssistScopeGaps,
  QuoteAssistUnmatched,
} from "@hardscapehouse/api-client";

export type Proposal = QuoteAssistProposal;
export type Alternate = QuoteAssistAlternate;
export type Unmatched = QuoteAssistUnmatched;
export type Extraction = QuoteAssistExtraction;
export type Finding = QuoteAssistFinding;
export type ScopeGaps = QuoteAssistScopeGaps;

/** The confidence bands, spelled exactly as the server does. */
export const CONFIDENCE_HIGH = "HIGH";
export const CONFIDENCE_MEDIUM = "MEDIUM";
export const CONFIDENCE_LOW = "LOW";

/**
 * confidenceLabel turns a band into the words on the chip. Lower case and
 * unhedged: the band already IS the hedge, and the evidence underneath it
 * (Proposal.why) is what a rep reads. An unknown band renders as "unrated"
 * rather than an empty chip, because a blank chip reads as a rendering bug.
 */
export function confidenceLabel(band: string | undefined): string {
  switch ((band ?? "").toUpperCase()) {
    case CONFIDENCE_HIGH:
      return "high confidence";
    case CONFIDENCE_MEDIUM:
      return "worth a look";
    case CONFIDENCE_LOW:
      return "check this one";
    default:
      return "unrated";
  }
}

/**
 * canConfirm reports whether a proposal is complete enough to become a line.
 * A row with no quantity, or with a unit the catalogue keeps no factor for, is
 * not confirmable: the tick is disabled and the row says why. It reads the
 * FLAGS the server sets deliberately, then the fields.
 */
export function canConfirm(p: Proposal): boolean {
  if (!p || !p.product_id) return false;
  if (p.needs_qty || p.needs_uom) return false;
  return (p.qty_entered ?? "").trim() !== "" && (p.uom_entered ?? "").trim() !== "";
}

/** The one short sentence beside a row that cannot be confirmed; empty when it can. */
export function blockedReason(p: Proposal): string {
  if (!p || !p.product_id) return "No product matched, so there is nothing to add.";
  if (p.needs_qty && p.needs_uom) return "Add a quantity and pick a unit before you add this line.";
  if (p.needs_qty) return "Add a quantity before you add this line.";
  if (p.needs_uom) return "Pick a unit before you add this line.";
  return "";
}

/**
 * editedProposal fills in what the parser could not read, in place. Both
 * flags are RECOMPUTED from what is now in the fields, so typing a quantity
 * into a blocked row unblocks it without a round trip. The server still
 * validates the unit when the line is added.
 */
export function editedProposal(p: Proposal, patch: Partial<Proposal>): Proposal {
  const next = { ...p, ...patch };
  next.needs_qty = (next.qty_entered ?? "").trim() === "";
  next.needs_uom = (next.uom_entered ?? "").trim() === "";
  return next;
}

/**
 * selectionOf is the confirm state, and it STARTS EMPTY. Given an extraction
 * and the refs the rep ticked it returns the ticked ones, and given no ticks
 * it returns nothing. No select-all, no auto-tick of high confidence rows.
 * Refs not in the extraction are ignored: a stale tick from a previous paste
 * must not survive into a new one.
 */
export function selectionOf(ex: Extraction | null | undefined, selectedRefs: readonly string[]): Proposal[] {
  const proposals = ex?.proposals ?? [];
  if (!Array.isArray(proposals) || proposals.length === 0) return [];
  const ticked = new Set(selectedRefs ?? []);
  return proposals.filter((p) => ticked.has(p.ref));
}

/** What one confirmed row sends through the client's addLine. */
export interface AddLinePayload {
  ref: string;
  product_id: string;
  description: string;
  qty_entered: string;
  uom_entered: string;
}

/**
 * addPayloads is what actually gets sent: one payload per TICKED and
 * CONFIRMABLE row. selectionOf drops what the rep did not tick; canConfirm
 * drops a ticked row that became incomplete. The description is the
 * catalogue's name, not the rep's phrase.
 */
export function addPayloads(ex: Extraction | null | undefined, selectedRefs: readonly string[]): AddLinePayload[] {
  return selectionOf(ex, selectedRefs)
    .filter((p) => canConfirm(p))
    .map((p) => ({
      ref: p.ref,
      product_id: p.product_id,
      description: p.name,
      qty_entered: (p.qty_entered ?? "").trim(),
      uom_entered: (p.uom_entered ?? "").trim(),
    }));
}

/** toggleRef flips one ref in the confirm set, returning a NEW array (Lit re-renders on identity). */
export function toggleRef(selectedRefs: readonly string[], ref: string): string[] {
  const current = selectedRefs ?? [];
  return current.includes(ref) ? current.filter((r) => r !== ref) : [...current, ref];
}

/**
 * withAlternate swaps a proposal's product for one of its alternates, in
 * place. The quantity survives (480 sq ft was about the patio, not the
 * product) and the unit does not: it belongs to the NEW product and whether
 * the catalogue keeps a factor for it is a server question, so the swapped
 * row comes back needing the unit confirmed. A stale ref or product is a
 * no-op, never a half change.
 */
export function withAlternate(proposals: readonly Proposal[], ref: string, productID: string): Proposal[] {
  const list = proposals ?? [];
  const i = list.findIndex((p) => p.ref === ref);
  if (i < 0) return [...list];
  const alt = (list[i].alternates ?? []).find((a) => a.product_id === productID);
  if (!alt) return [...list];
  const swapped: Proposal = {
    ...list[i],
    product_id: alt.product_id,
    sku: alt.sku,
    name: alt.name,
    match_reason: alt.match_reason,
    sell_uom: alt.sell_uom,
    base_uom: alt.base_uom,
    uom_entered: "",
    needs_uom: true,
    unit_note: `Swapped to ${alt.sku}: confirm the unit for this product.`,
    confidence: CONFIDENCE_LOW,
    why: ["you picked this one instead of the first match", ...(list[i].why ?? []).slice(1)],
  };
  const out = [...list];
  out[i] = swapped;
  return out;
}

/**
 * visibleFindings is the suggestions a rep still has on screen. Dismissal is
 * by RULE CODE, not index, so a re-read after every added line does not
 * resurrect what they dismissed or silence a different suggestion that moved
 * into the same position.
 */
export function visibleFindings(gaps: ScopeGaps | null | undefined, dismissed: readonly string[]): Finding[] {
  const findings = gaps?.findings ?? [];
  if (!Array.isArray(findings)) return [];
  const gone = new Set(dismissed ?? []);
  return findings.filter((f) => !gone.has(f.rule_code));
}

/**
 * gapStatusText is what a screen reader is told about the suggestions, and it
 * is EMPTY when there is nothing to say: no "0 suggestions", no "the quote
 * looks complete". A reassurance announced on every quote is one people stop
 * listening to.
 */
export function gapStatusText(gaps: ScopeGaps | null | undefined, dismissed: readonly string[]): string {
  const shown = visibleFindings(gaps, dismissed);
  if (shown.length === 0) return "";
  const n = shown.length;
  return `${n} suggestion${n === 1 ? "" : "s"} for this quote. ${shown.map((f) => f.title).join(". ")}.`;
}

/**
 * gapEmptyText is the quiet line when a read came back with no gaps, and it
 * separates the silences the server keeps apart: every rule switched off (a
 * configuration, said aloud), nothing on the quote to check yet, or checked
 * with nothing to add. It is not announced; it is there for a person who
 * went looking.
 */
export function gapEmptyText(gaps: ScopeGaps | null | undefined): string {
  if (!gaps) return "";
  if ((gaps.rules_evaluated ?? 0) === 0) {
    return "Scope suggestions are switched off: an administrator turns the rules on.";
  }
  if ((gaps.lines_checked ?? 0) === 0) {
    return "Nothing to check yet: add a line and I will look for the usual companions.";
  }
  return `Checked ${gaps.lines_checked} line${gaps.lines_checked === 1 ? "" : "s"} against ${gaps.rules_evaluated} rule${gaps.rules_evaluated === 1 ? "" : "s"}, nothing obviously missing.`;
}

/**
 * extractStatusText is the aria-live sentence for the proposal list. It names
 * BOTH counts, always, including zero unmatched: a screen-reader user cannot
 * see the second list.
 */
export function extractStatusText(opts: { loading: boolean; error: string; extraction: Extraction | null }): string {
  if (opts.error) return opts.error;
  if (opts.loading) return "Reading the description...";
  const ex = opts.extraction;
  if (!ex) return "Paste a job description and I will suggest quote lines for you to confirm.";
  const p = ex.proposals?.length ?? 0;
  const u = ex.unmatched?.length ?? 0;
  if (p === 0 && u === 0) return "Nothing to suggest from that text.";
  const parts = [`${p} suggested line${p === 1 ? "" : "s"} to review`];
  parts.push(u === 0 ? "nothing left unmatched" : `${u} that I couldn't match`);
  return `${parts.join(", ")}. Nothing has been added to the quote yet: tick the ones you want.`;
}

/** confirmButtonLabel counts what WILL be added (ticked and completable), so it never overstates. */
export function confirmButtonLabel(ex: Extraction | null | undefined, selectedRefs: readonly string[]): string {
  const n = addPayloads(ex, selectedRefs).length;
  if (n === 0) return "Add the ticked lines";
  return `Add ${n} line${n === 1 ? "" : "s"} to the quote`;
}

/**
 * addResultText summarises a confirm run in one sentence, naming failures
 * individually: a partial success is the case a single boolean would lose, and
 * a rep who re-runs the batch would duplicate the lines that did land.
 */
export function addResultText(added: number, failures: readonly { ref: string; message: string }[]): string {
  const f = failures ?? [];
  const addedPart = added === 0 ? "No lines were added" : `Added ${added} line${added === 1 ? "" : "s"}`;
  if (f.length === 0) return `${addedPart}.`;
  return `${addedPart}. ${f.length} could not be added: ${f.map((x) => x.message).join(" ")}`;
}

/**
 * interruptedAddText: said when the person opened another quote while a confirm run was still adding lines.
 * The run stops at once, so the lines that had landed are named and the rest are said to be not added; it
 * names no quote, because it is read on the quote opened since.
 */
export function interruptedAddText(added: number, total: number): string {
  if (added >= total) return `You moved to another quote while lines were being added. All ${total} selected line${total === 1 ? " was" : "s were"} added to the quote you left.`;
  const landed = added === 0 ? "None of the" : `${added} of the`;
  return `You moved to another quote while lines were being added. ${landed} ${total} selected line${total === 1 ? " was" : "s were"} added to the quote you left, and the rest were not added.`;
}

/**
 * aiHelperLatched: the contract has no availability read, so the AI helper
 * starts available. It latches DISABLED only when the server answered an
 * extraction asked with use_ai true by source deterministic and an ai_note:
 * the server's own statement that no key is configured. A missing note, an
 * ai-assisted answer or an extraction not asked with the helper never latch.
 */
export function aiHelperLatched(askedAi: boolean, ex: Extraction | null | undefined): boolean {
  if (!askedAi || !ex) return false;
  return ex.source === "deterministic" && (ex.ai_note ?? "").trim() !== "";
}

/** Which reader answered, in the server's own word; empty with no extraction. */
export function sourceText(ex: Extraction | null | undefined): string {
  return ex ? `Read by: ${ex.source}` : "";
}

/** The one-line explanation beside the AI helper. */
export const AI_OFF_HINT =
  "Off: your text is read here, with no AI and no cost. On: an AI pass helps split awkward wording, and it counts against your AI allowance.";

/** The label of the button that reads the paste box. */
export const READ_LABEL = "Suggest lines from this text";

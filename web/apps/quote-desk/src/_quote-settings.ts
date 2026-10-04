// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-surface-settings.ts,
// quote-accept-admin.ts and quote-scope-rules.ts (their logic halves);
// adapted for HH.
//
// _quote-settings.ts: the three settings screens' pure tables (DOM-free, unit
// tested on the Node runner). The app never decides who may save: it validates
// the values the way the server's bounds do for an immediate answer, builds
// the write bodies, and speaks whatever the server answers, refusals included.
import {
  effectiveItems,
  QB_ACTIONS,
  QB_COLUMNS,
  QB_PANELS,
  type ResolvedItem,
  type SurfaceItemKind,
  type WireItem,
} from "./quote-surface.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { ARRANGEMENTS, COST_VISIBILITIES, type QuotePolicyWire, type ScopeRuleRow } from "./_panel-wire.js";

/** The role profiles the curation page edits, in the reference's order. */
export const PROFILES = ["counter", "yard", "driver", "manager", "admin"] as const;
export type CurationProfile = (typeof PROFILES)[number];

/** The kinds the curation page lists, in the reference's order. */
export const CURATION_KINDS: SurfaceItemKind[] = ["panel", "column", "action"];

const KIND_HEADING: Record<string, string> = {
  panel: "Panels",
  column: "Line table columns",
  action: "Actions",
};

export function kindHeading(kind: SurfaceItemKind): string {
  return KIND_HEADING[kind] ?? kind;
}

function inRange(n: number, lo: number, hi: number): boolean {
  return Number.isInteger(n) && n >= lo && n <= hi;
}

/**
 * validatePolicy mirrors the server's bounds so the form answers at once and
 * names the knob. The server re-validates regardless and its sentence wins.
 */
export function validatePolicy(p: QuotePolicyWire): string | null {
  if (!(COST_VISIBILITIES as readonly string[]).includes(p.cost_visibility)) return "Choose who sees cost and margin visibility.";
  if (!inRange(p.default_validity_days, 1, 365)) return "Default validity must be a whole number of days from 1 to 365.";
  if (!inRange(p.default_deposit_pct, 0, 100)) return "Default deposit must be a whole percent from 0 to 100.";
  if (!inRange(p.workspace_split_pct, 20, 80)) return "Default panel split must be a whole percent from 20 to 80.";
  if (!(ARRANGEMENTS as readonly string[]).includes(p.workspace_arrangement)) return "Choose a default arrangement.";
  return null;
}

/**
 * The write body for one scope. A branch row names its branch; the dealer
 * default sends none. The server decides who may write which: the Sales
 * manager a branch row it reaches, the General Manager the dealer default.
 * The derived effective_chips never rides a write.
 */
export function policyBody(p: QuotePolicyWire, branchId: string): QuotePolicyWire {
  const { effective_chips: _effective, branch_id: _branch, ...rest } = p;
  return branchId === "" ? { ...rest } : { ...rest, branch_id: branchId };
}

/**
 * A refusal on a write or a read, in the server's words as a counter person
 * reads them (module prefixes and internal enums peeled), or a plain sentence
 * when the server sent none.
 */
export function writeRefusalLine(status: number | undefined, detail: string): string {
  const d = spokenRefusal(detail);
  if (d !== "") return d;
  if (status === 403) return "The server refused this change for your role.";
  return "The change was not saved.";
}

const CATALOG: Record<string, typeof QB_COLUMNS> = {
  panel: QB_PANELS,
  column: QB_COLUMNS,
  action: QB_ACTIONS,
};

/** One kind's draft through the registry's own resolver (role rows beat the catalog; cost columns ship hidden). */
export function curationDraft(kind: SurfaceItemKind, roleItems: WireItem[]): ResolvedItem[] {
  return effectiveItems(kind, CATALOG[kind] ?? [], roleItems, null);
}

/** Flip an item's shown flag. The minimum panels are always on and never flip. */
export function toggleDraftItem(items: ResolvedItem[], id: string): ResolvedItem[] {
  return items.map((c) => (c.id === id && c.min !== true ? { ...c, visible: !c.visible } : c));
}

/** Move an item one place; an edge move changes nothing. */
export function moveDraftItem(items: ResolvedItem[], id: string, dir: -1 | 1): ResolvedItem[] {
  const next = [...items];
  const i = next.findIndex((c) => c.id === id);
  const j = i + dir;
  if (i < 0 || j < 0 || j >= next.length) return items;
  const a = next[i] as ResolvedItem;
  const b = next[j] as ResolvedItem;
  next[i] = b;
  next[j] = a;
  return next;
}

/** The role items write: every kind in draft order with positions; the minimum panels stay shown. */
export function buildItemsBody(
  profile: CurationProfile,
  drafts: Record<string, ResolvedItem[]>,
): { profile: CurationProfile; items: WireItem[] } {
  const items: WireItem[] = CURATION_KINDS.flatMap((kind) =>
    (drafts[kind] ?? []).map((c, position) => ({
      kind,
      id: c.id,
      position,
      visible: c.min === true ? true : c.visible,
    })),
  );
  return { profile, items };
}

/** What each position of the one acceptance switch means, said after a save. */
export function acceptanceNotice(allowMulti: boolean): string {
  return allowMulti
    ? "Multiple acceptance is on: a quote that was already accepted can be accepted again, and every document records where it came from."
    : "Multiple acceptance is off: a quote can be accepted once; a second try points at the existing order.";
}

/** The scope rule table's count line; a switched-off rule stays listed. */
export function scopeRuleSummary(rules: ReadonlyArray<Pick<ScopeRuleRow, "enabled">>): {
  on: number;
  total: number;
  anyOff: boolean;
  line: string;
} {
  const on = rules.filter((r) => r.enabled).length;
  const total = rules.length;
  return { on, total, anyOff: on < total, line: `${on} of ${total} ${total === 1 ? "rule is" : "rules are"} on.` };
}

/** The chip vocabulary: every active product's application, unique and sorted. */
export function chipChoices(products: ReadonlyArray<{ is_active?: boolean; application?: string }>): string[] {
  const set = new Set<string>();
  for (const p of products) if (p.is_active === true && p.application !== undefined && p.application !== "") set.add(p.application);
  return [...set].sort();
}

/** Picking a category appends it (click order is chip order); picking it again removes it. */
export function toggleChip(picks: readonly string[], category: string): string[] {
  return picks.includes(category) ? picks.filter((c) => c !== category) : [...picks, category];
}

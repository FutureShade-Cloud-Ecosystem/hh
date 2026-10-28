// The platform key roster's pure half: the two surfaces' chord grammars, the
// action registries, the payload normaliser, the collision rule and the copy.
// No DOM, no Lit, no client: the same shape as the original's
// _platform-hotkeys.ts, so the Node runner exercises it without a browser.
// views-hotkeys.ts holds the element side; app-root the capture.
//
// Port provenance: the two grammars, the registries, the verdict and the
// sentences are the original app's hotkey-logic.ts, _pos-hotkeys.ts and
// _platform-hotkeys.ts (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md), narrowed to what the configurator shows.
//
// ── WHY TWO ROSTERS AND NOT ONE LIST ─────────────────────────────────────────
//
// The product has two hotkey substrates. They have different action
// namespaces, different chord grammars, and different rules about which one
// wins when both bind the same physical key (the till listens in the capture
// phase and wins while a sale is open). Presenting them as one list would be
// a lie that reads as a kindness. So: two panels, each with its own heading,
// its own one-line scope, its own grammar, and its own Save. EVERY function
// below takes a surface, and none has a default.

import { UNKNOWN_ACTION, UNKNOWN_SURFACE } from "./placeholders.js";

/** The app-wide navigation map's surface id. */
export const PLATFORM_SHELL_SURFACE = "app.shell";
/** The counter roster's surface id. */
export const PLATFORM_COUNTER_SURFACE = "pos.counter";

/** The two surfaces, in the order the screen renders them. */
export const PLATFORM_SURFACES: readonly string[] = [PLATFORM_SHELL_SURFACE, PLATFORM_COUNTER_SURFACE];

/** One platform binding as the API sends it. */
export interface PlatformKey {
  readonly action: string;
  readonly binding: string;
  readonly position: number;
}

/** One surface's whole platform roster, and the tier immediately above it. */
export interface PlatformRosterView {
  readonly surface: string;
  readonly keys: readonly PlatformKey[];
  /** The dealer override in force above this roster. Empty for app.shell, always. */
  readonly dealer: readonly PlatformKey[];
}

/** One keystroke the app shell owns outright: no tier binds it, no tier can free it. */
export interface ReservedKey {
  readonly binding: string;
  /** Plain language, from the server. Never an action id. */
  readonly holds: string;
}

/** The GET /platform/hotkeys answer, normalised. */
export interface PlatformHotkeyView {
  readonly rosters: readonly PlatformRosterView[];
  /** The SERVER's answer to "may this caller change the roster". Never computed here. */
  readonly canEdit: boolean;
  /** The shell's own hardcoded keys, served rather than mirrored here. */
  readonly reserved: readonly ReservedKey[];
}

/** The screen's name for each roster: what the keys DO, not which table they live in. */
export const PLATFORM_SURFACE_LABEL: Readonly<Record<string, string>> = Object.freeze({
  [PLATFORM_SHELL_SURFACE]: "Everywhere in the app",
  [PLATFORM_COUNTER_SURFACE]: "At the counter",
});

/** The one explanatory string each panel is allowed: who obeys these keys. */
export const PLATFORM_SURFACE_SCOPE: Readonly<Record<string, string>> = Object.freeze({
  [PLATFORM_SHELL_SURFACE]: "Every screen obeys these.",
  [PLATFORM_COUNTER_SURFACE]: "Only the till obeys these, and they win while a sale is open.",
});

/**
 * Sentence labels for the counter's five actions. The screen renders through
 * this, never a stored id, so an action id cannot reach a person as text.
 */
export const COUNTER_ACTION_LABEL: Readonly<Record<string, string>> = Object.freeze({
  "step-next": "Move the sale on to the next step",
  "step-back": "Go back one step in the sale",
  pay: "Take payment, complete the sale",
  park: "Park the ticket and start a new one",
  search: "Put the cursor back in the scan box",
});

/**
 * The counter's actions in the legend's own order (the original's HOTKEY_ORDER,
 * which is the order the strip draws).
 */
const COUNTER_ORDER: readonly string[] = ["step-next", "step-back", "pay", "park", "search"];

/**
 * One registered shell action: the overlay's words for it. The ids are what
 * stored rows point at, so they follow the stored id rule: ADD, NEVER RENAME.
 * The five are the original registry's seed set, carried with their labels.
 */
export interface ShellAction {
  readonly id: string;
  readonly label: string;
}

const SHELL_ACTIONS: readonly ShellAction[] = [
  { id: "sale.cash.new", label: "Start a new sale at the till" },
  { id: "sales.tab.new", label: "Open a new Sales tab" },
  { id: "order.quick.open", label: "Open the order write-up screen" },
  { id: "search.focus", label: "Search the product list" },
  { id: "assist.open", label: "Open the assistant" },
];

/** Every action one surface can bind, in the order the screen lists them. */
export function platformActions(surface: string): { id: string; label: string }[] {
  if (surface === PLATFORM_COUNTER_SURFACE) {
    return COUNTER_ORDER.map((id) => ({ id, label: COUNTER_ACTION_LABEL[id] ?? UNKNOWN_ACTION }));
  }
  return SHELL_ACTIONS.map((a) => ({ id: a.id, label: a.label }));
}

/**
 * The human label for one action on one surface, never the stored id.
 *
 * UX-106 round 3: an action the registry does not name reads as the house
 * placeholder word. The stored id must not reach a person as text, which is
 * what the counter roster's own note above says.
 */
export function platformActionLabel(surface: string, action: string): string {
  return platformActions(surface).find((a) => a.id === action)?.label ?? UNKNOWN_ACTION;
}

// -- the shell grammar (the original's bindingFromEvent, verbatim in meaning) ----------------------

/**
 * The canonical spelling of a pressed chord on the shell surface: modifiers
 * Ctrl, Alt, Shift in that order, then F1 to F12, a letter, or a digit;
 * letters and digits need Ctrl or Alt; F-keys stand alone. Meta returns null:
 * the OS and the browser own that key.
 */
export function bindingFromEvent(e: {
  key: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
}): string | null {
  if (e.metaKey) return null;
  const key = canonicalShellKey(e.key);
  if (key === null) return null;
  if (!key.startsWith("F") && !e.ctrlKey && !e.altKey) return null;
  const mods = [e.ctrlKey ? "Ctrl" : "", e.altKey ? "Alt" : "", e.shiftKey ? "Shift" : ""].filter(Boolean);
  return [...mods, key].join("+");
}

function canonicalShellKey(key: string): string | null {
  if (/^F([1-9]|1[0-2])$/.test(key)) return key;
  if (/^[a-zA-Z0-9]$/.test(key)) return key.toUpperCase();
  return null;
}

// -- the counter grammar (the original's chordFromEvent, verbatim in meaning) ----------------------

/**
 * The canonical spelling of a pressed chord on the counter surface:
 * lowercase, dash joined, modifiers shift, ctrl, alt in that order. A
 * function key stands alone; a letter or digit needs Ctrl or Alt (Shift alone
 * is typing a capital letter, and a counter that types must never claim that).
 */
export function chordFromEvent(e: {
  key: string;
  ctrlKey?: boolean;
  altKey?: boolean;
  shiftKey?: boolean;
  metaKey?: boolean;
}): string {
  if (e.metaKey) return "";
  const fn = /^F([1-9]|1[0-2])$/.test(e.key);
  const typed = /^[a-zA-Z0-9]$/.test(e.key);
  if (!fn && !typed) return "";
  if (typed && !e.ctrlKey && !e.altKey) return "";
  const parts: string[] = [];
  if (e.shiftKey) parts.push("shift");
  if (e.ctrlKey) parts.push("ctrl");
  if (e.altKey) parts.push("alt");
  parts.push(e.key.toLowerCase());
  return parts.join("-");
}

/**
 * Is this the canonical spelling of a bindable counter chord? The same rule
 * the server enforces, stated on this side too because the editor must
 * refuse a chord before it is sent.
 */
export function validChord(chord: string): boolean {
  const parts = (chord || "").toLowerCase().split("-");
  const base = parts[parts.length - 1] ?? "";
  const mods = parts.slice(0, -1);
  const allowed = ["shift", "ctrl", "alt"];
  let last = -1;
  for (const m of mods) {
    const i = allowed.indexOf(m);
    if (i < 0 || i <= last) return false; // unknown, duplicated, or out of canonical order
    last = i;
  }
  if (/^f([1-9]|1[0-2])$/.test(base)) return true;
  if (!/^[a-z0-9]$/.test(base)) return false;
  return mods.includes("ctrl") || mods.includes("alt");
}

/** A counter key's printed form: symbols, because a keycap has no room for words. */
export function keycap(key: string): string {
  const parts = (key || "").toLowerCase().split("-");
  const fn = parts[parts.length - 1] ?? "";
  let out = "";
  if (parts.includes("ctrl")) out += "\u2303";
  if (parts.includes("alt")) out += "\u23E5";
  if (parts.includes("shift")) out += "\u21E7";
  return out + fn.toUpperCase();
}

// -- the payload normaliser ------------------------------------------------------------------------

/**
 * Normalise the payload at the loader boundary: a body this build cannot read
 * yields EMPTY rosters and canEdit false. canEdit defaults FALSE for the same
 * reason every capability flag does: the safe answer to "may I" when nobody
 * said is no.
 */
export function asPlatformView(body: unknown): PlatformHotkeyView {
  const b = (body ?? {}) as Record<string, unknown>;
  const raw = Array.isArray(b.rosters) ? (b.rosters as unknown[]) : [];
  const rosters: PlatformRosterView[] = [];
  for (const surface of PLATFORM_SURFACES) {
    const hit = raw.find(
      (r) => r !== null && typeof r === "object" && (r as Record<string, unknown>).surface === surface,
    ) as Record<string, unknown> | undefined;
    rosters.push({
      surface,
      keys: asKeyRows(hit === undefined ? undefined : hit.keys),
      // A payload from a build older than the dealer column has no `dealer`
      // at all, and the honest reading of that is empty: the cross-tier
      // refusal is what it costs, never the screen.
      dealer: asKeyRows(hit === undefined ? undefined : hit.dealer),
    });
  }
  return {
    rosters,
    canEdit: b.can_edit === true,
    reserved: (Array.isArray(b.reserved) ? (b.reserved as unknown[]) : [])
      .filter((r): r is Record<string, unknown> => r !== null && typeof r === "object")
      .filter((r) => typeof r.binding === "string" && (r.binding as string) !== "")
      .map((r) => ({
        binding: r.binding as string,
        holds: typeof r.holds === "string" && r.holds !== "" ? (r.holds as string) : "belongs to the app itself",
      })),
  };
}

/** The shared row coercion for both tiers on one roster. */
function asKeyRows(raw: unknown): PlatformKey[] {
  return (Array.isArray(raw) ? (raw as unknown[]) : [])
    .filter((k): k is Record<string, unknown> => k !== null && typeof k === "object")
    .filter((k) => typeof k.action === "string" && typeof k.binding === "string")
    .map((k, i) => ({
      action: k.action as string,
      binding: k.binding as string,
      position: typeof k.position === "number" ? k.position : i,
    }));
}

/** One surface's rows out of a normalised view, or an empty roster. */
export function rosterFor(view: PlatformHotkeyView | null, surface: string): readonly PlatformKey[] {
  return view?.rosters.find((r) => r.surface === surface)?.keys ?? [];
}

/** The dealer override in force above one surface's platform roster, or nothing. */
export function dealerFor(view: PlatformHotkeyView | null, surface: string): readonly PlatformKey[] {
  return view?.rosters.find((r) => r.surface === surface)?.dealer ?? [];
}

/** The shell's own hardcoded keys, as the server sent them. */
export function reservedOf(view: PlatformHotkeyView | null): readonly ReservedKey[] {
  return view?.reserved ?? [];
}

/** The OTHER substrate's id: the one this surface's keys are allowed to share with. */
export function otherSurface(surface: string): string {
  return surface === PLATFORM_COUNTER_SURFACE ? PLATFORM_SHELL_SURFACE : PLATFORM_COUNTER_SURFACE;
}

/** The roster as the editor holds it while it is being changed: action to chord. */
export function draftFrom(keys: readonly PlatformKey[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of keys) out[k.action] = k.binding;
  return out;
}

/** A draft back on the wire, in the screen's own listed order: position IS that order. */
export function draftToKeys(surface: string, draft: Record<string, string>): PlatformKey[] {
  return platformActions(surface)
    .filter((a) => draft[a.id] !== undefined && draft[a.id] !== "")
    .map((a, i) => ({ action: a.id, binding: draft[a.id], position: i }));
}

/** The chord a keypress spells, in THIS surface's grammar, or "" when it is not bindable there. */
export function platformChordFrom(
  surface: string,
  e: { key: string; ctrlKey: boolean; altKey: boolean; shiftKey: boolean; metaKey: boolean },
): string {
  if (surface === PLATFORM_COUNTER_SURFACE) return chordFromEvent(e);
  return bindingFromEvent(e) ?? "";
}

/** Whether a chord is bindable at all on this surface: each surface's own rule. */
export function platformValidBinding(surface: string, binding: string): boolean {
  if (binding === "") return false;
  if (surface === PLATFORM_COUNTER_SURFACE) return validChord(binding);
  return bindingFromEvent(eventFromShellBinding(binding)) === binding;
}

/** The display form of a stored binding: the shell's spelling as it stands, the counter's through keycap. */
export function platformKeycap(surface: string, binding: string): string {
  if (binding === "") return "";
  return surface === PLATFORM_COUNTER_SURFACE ? keycap(binding) : binding;
}

/**
 * Which other action holds this key on this roster, or null. A roster where
 * two actions claim one key resolves by row order and drops the loser
 * silently, so the clash is named at the press, never after a save.
 */
export function platformCollisionFor(
  draft: Record<string, string>,
  action: string,
  binding: string,
): string | null {
  if (binding === "") return null;
  for (const [other, bound] of Object.entries(draft)) {
    if (other !== action && bound === binding) return other;
  }
  return null;
}

/** The refusal sentence for a clash: keycap plus a human label, never a stored id. */
export function platformClashSentence(surface: string, binding: string, clashAction: string): string {
  return `${platformKeycap(surface, binding)} already runs "${platformActionLabel(surface, clashAction)}". Free that one first, or pick another key.`;
}

// -- the effective verdict -------------------------------------------------------------------------

/**
 * WHICH OF THE THREE ANSWERS a candidate binding got. The order of the arms
 * is the rule: reserved first (a key no tier can hold is simply not
 * available), then this roster's draft, then the dealer's override above.
 * The cross substrate claim is a WARNING and never a refusal: the shipped
 * defaults bind F2, F4 and F9 on both surfaces on purpose, the till listens
 * in capture and the shell in bubble, so a rule that refused it would refuse
 * the roster this product ships with.
 */
export type KeyVerdictKind = "free" | "shared" | "taken";

export interface KeyVerdict {
  readonly kind: KeyVerdictKind;
  /** "" only when kind is "free". */
  readonly sentence: string;
}

export interface EffectiveContext {
  /** The roster being edited, action to chord. This tier, this surface. */
  readonly draft: Record<string, string>;
  readonly dealer?: readonly PlatformKey[];
  readonly reserved?: readonly ReservedKey[];
  /** The OTHER substrate's roster: for the warning, never for a refusal. */
  readonly other?: readonly PlatformKey[];
}

const FREE: KeyVerdict = { kind: "free", sentence: "" };

/** The whole verdict for one candidate binding, in one call. */
export function effectiveVerdict(surface: string, action: string, binding: string, ctx: EffectiveContext): KeyVerdict {
  if (binding === "") return FREE;
  const held = reservedHolderFor(binding, ctx.reserved);
  if (held !== "") {
    return {
      kind: "taken",
      sentence: `${platformKeycap(surface, binding)} ${held} on every screen. No roster can take it, so pick another key.`,
    };
  }
  const clash = platformCollisionFor(ctx.draft, action, binding);
  if (clash !== null) return { kind: "taken", sentence: platformClashSentence(surface, binding, clash) };
  const overridden = (ctx.dealer ?? []).find((d) => sameBinding(d.binding, binding) && d.action !== action);
  if (overridden !== undefined) {
    return {
      kind: "taken",
      sentence: `${platformKeycap(surface, binding)} is already this dealer's key for "${platformActionLabel(surface, overridden.action)}". Free it in Counter settings first, or pick another key.`,
    };
  }
  const elsewhere = (ctx.other ?? []).find((o) => sameBinding(o.binding, binding));
  if (elsewhere !== undefined) {
    const there = otherSurface(surface);
    return {
      kind: "shared",
      sentence: `${platformKeycap(surface, binding)} also runs "${platformActionLabel(there, elsewhere.action)}" ${PLATFORM_SURFACE_LABEL[there]?.toLowerCase() ?? UNKNOWN_SURFACE}. Both keep working: the till answers first while a sale is open.`,
    };
  }
  return FREE;
}

/** Is this the same physical key? The substrates spell one key "F2" and "f2". */
function sameBinding(a: string, b: string): boolean {
  return (a || "").toLowerCase() === (b || "").toLowerCase();
}

/** What the shell already does with this keystroke, in plain words, or "". */
export function reservedHolderFor(binding: string, reserved: readonly ReservedKey[] | undefined): string {
  const want = reservedForm(binding);
  if (want === "") return "";
  for (const r of reserved ?? []) {
    if (reservedForm(r.binding) === want) return r.holds;
  }
  return "";
}

/** The comparison folds the spellings the way the server's canonical form does. */
function reservedForm(binding: string): string {
  const s = (binding || "").trim().toLowerCase().replace(/\+/g, "-").replace(/\u2190/g, "arrowleft");
  if (s === "") return "";
  const parts = s.split("-");
  const base = parts[parts.length - 1] ?? "";
  return [...parts.slice(0, -1).sort(), base].join("-");
}

/** The refusal for a keystroke that is not bindable, in each surface's own terms. */
export function platformBadChordSentence(surface: string): string {
  return surface === PLATFORM_COUNTER_SURFACE
    ? "That key types into the sale. Use a function key, or hold Ctrl or Alt with a letter."
    : "That combination cannot be bound. Use a function key, or hold Ctrl or Alt with a letter.";
}

/**
 * The server's own sentence, fit to be painted. The core's modules prefix
 * their error strings with their own name for greppability; that prefix is
 * engineering vocabulary and no prefixed string reaches a screen. The strip
 * is conservative (one leading lowercase word, a colon, a space) and a no-op
 * on a message that never had one.
 */
export function serverSentence(msg: string): string {
  return (msg || "").replace(/^[a-z][a-z0-9_-]*:\s+/, "");
}

/**
 * A synthetic KeyboardEvent-shaped object for a stored shell binding, so a
 * stored spelling is checked against the one grammar rather than against a
 * second regex written here.
 */
function eventFromShellBinding(binding: string): {
  key: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
} {
  const parts = binding.split("+");
  const key = parts[parts.length - 1] ?? "";
  const mods = parts.slice(0, -1);
  return {
    key,
    ctrlKey: mods.includes("Ctrl"),
    altKey: mods.includes("Alt"),
    shiftKey: mods.includes("Shift"),
    metaKey: false,
  };
}

/**
 * The function keys mainstream browsers claim. Binding one is ALLOWED and
 * merely marked (dashed keycap): the escape rule (a binding matches only its
 * exact chord, so any unrequested modifier falls through to the host) keeps
 * the browser's own gesture reachable, and refusing the binding outright
 * would be this screen overruling the ergonomics of the person using it.
 */
const BROWSER_KEYS = new Set(["f1", "f3", "f5", "f6", "f7", "f10", "f11", "f12"]);

/** Whether a stored chord's base key is one the browser also claims. */
export function isBrowserKey(surface: string, chord: string): boolean {
  if (chord === "") return false;
  const parts = chord.split(surface === PLATFORM_COUNTER_SURFACE ? "-" : "+");
  return BROWSER_KEYS.has((parts[parts.length - 1] ?? "").toLowerCase());
}

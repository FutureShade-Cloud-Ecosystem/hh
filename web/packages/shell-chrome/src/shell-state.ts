// The shell chrome's pure state: no DOM, no Lit, no imports. The rail's pins
// per user over an injected storage seam, the rail's collapsed posture, the
// app switcher model by role over an injected manifest slice, the current
// surface's words, and the sync state chip's words. The components in this
// package render what this module answers; the shell wires the two together.
//
// Ported and polished from the original HardscapeOS at master 87d7941b, read
// only: the pin semantics are app/src/dashboard-logic.ts's (an unpin keeps its
// place so a re-pin restores it; the whole set is what persists; the ends of
// the rail refuse politely), and the rail's collapsed posture is
// app/src/_rail-state.ts's (silence means collapsed; the one stored literal an
// explicit expand writes keeps winning; anything else degrades to the default,
// never an error). What changes for HH: pins are kept per person on the
// device, keyed by their user id, so two users on one device never share
// pins, and persistence rides a narrow injected seam the shell fills
// (localStorage in the browser copy, the Tauri store in the shells) instead
// of a server override.

// ── the pin book ─────────────────────────────────────────────────────────────────────────────────

/** The two operations a keyed storage seam needs, so tests and shells inject
 * doubles. The pins seam keys by user id; the rail's posture seam keys by its
 * own key. */
export interface KeyValueStorage {
  read(key: string): string | null;
  write(key: string, value: string): void;
}

/** The pins seam: a KeyValueStorage whose keys are `pinStorageKey(userId)`. */
export interface PinStorage extends KeyValueStorage {}

/** One row of the rail: the person's own pinned surface. The glyph is a
 * short mark the shell renders beside the label until the icon registry
 * lands behind the design system's icon seam. */
export interface RailPin {
  key: string;
  label: string;
  glyph: string;
}

/** An in-memory seam, for tests and the demo. Keyed by the seam key. */
export function memoryPinStorage(): PinStorage & { entries(key: string): string[] } {
  const mem = new Map<string, string>();
  return {
    read: (key) => (mem.has(key) ? (mem.get(key) as string) : null),
    write: (key, value) => {
      mem.set(key, value);
    },
    entries: (key) => visibleKeysOf(mem.get(key) ?? null),
  };
}

interface StoredPin {
  key: string;
  visible: boolean;
}

interface StoredPayload {
  v: 1;
  pins: StoredPin[];
}

/** The storage key for one person's pins: the person is in the key, so two
 * users on one device never share pins. The user id is the FB Console user id
 * the session carries (or the core subject, for a persona). It is encoded, so
 * an odd id (a slash, dots, spaces) stays one key under the pin prefix and
 * never reaches another key on the origin. */
export function pinStorageKey(userId: string): string {
  return `hh-shell.pins.v1.${encodeURIComponent(userId)}`;
}

/** A blank id is no one: a shell that calls the book before sign in or after
 * sign out must not share one book under the empty key. */
function hasUser(userId: unknown): userId is string {
  return typeof userId === 'string' && userId.trim() !== '';
}

function parsePayload(raw: string | null): StoredPin[] {
  if (raw === null || raw === '') return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return [];
  const candidate = parsed as Record<string, unknown>;
  if (candidate['v'] !== 1 || !Array.isArray(candidate['pins'])) return [];
  const pins: StoredPin[] = [];
  for (const entry of candidate['pins']) {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) continue;
    const row = entry as Record<string, unknown>;
    if (typeof row['key'] !== 'string' || row['key'] === '' || typeof row['visible'] !== 'boolean') continue;
    pins.push({ key: row['key'], visible: row['visible'] });
  }
  return pins;
}

function visibleKeysOf(raw: string | null): string[] {
  return parsePayload(raw)
    .filter((p) => p.visible)
    .map((p) => p.key);
}

/** How many pins one person may hold. The rail is a shortlist, not a second
 * map; the selector is the whole map. */
export const SHELL_PIN_CAP = 8;

export type PinChange = { ok: true; changed: boolean } | { ok: false; reason: 'cap' | 'no-user' };

/**
 * The person's own pinned surfaces, across every micro app they hold. Every
 * operation reads the person's payload from the seam, answers, and writes the
 * whole set back, so two books over one seam always agree and a second person
 * signing in reads only their own rows. The book's own memory is the truth
 * while it lives: a seam that throws (blocked storage, quota) is not a fault
 * here, the answers stand on the rows the book already holds, and only
 * persistence is skipped, which is the honest behaviour for that posture (the
 * ERP's storage shim lesson).
 */
export class PinBook {
  readonly #storage: PinStorage;
  readonly #cap: number;
  readonly #cache = new Map<string, StoredPin[]>();

  constructor(options?: { storage?: PinStorage; cap?: number }) {
    this.#storage = options?.storage ?? memoryPinStorage();
    this.#cap = options?.cap ?? SHELL_PIN_CAP;
  }

  /** The pinned keys for one person, in rail order. */
  pins(userId: string): string[] {
    if (!hasUser(userId)) return [];
    return this.#read(userId)
      .filter((p) => p.visible)
      .map((p) => p.key);
  }

  isPinned(userId: string, key: string): boolean {
    return this.pins(userId).includes(key);
  }

  /**
   * Pin one surface. A key the person has unpinned before revives in its old
   * place (the row was kept); a brand new key lands on the right, where a new
   * tab does. At the cap a brand new key is refused, plainly; a revival is
   * not a new row, so the cap does not refuse it.
   */
  add(userId: string, key: string): PinChange {
    if (!hasUser(userId)) return { ok: false, reason: 'no-user' };
    const rows = this.#read(userId);
    const found = rows.find((p) => p.key === key);
    if (found && found.visible) return { ok: true, changed: false };
    const visibleCount = rows.filter((p) => p.visible).length;
    if (!found && visibleCount >= this.#cap) return { ok: false, reason: 'cap' };
    const next = found
      ? rows.map((p) => (p.key === key ? { ...p, visible: true } : { ...p }))
      : [...rows, { key, visible: true }];
    this.#write(userId, next);
    return { ok: true, changed: true };
  }

  /**
   * Unpin one surface. The row is KEPT, at visible false, for the reason the
   * original gives: dropping it would make a deliberately empty book read as
   * "no rows at all", and a re-pin would land on the end instead of back
   * where it was, which turns an undo into a rearrangement.
   */
  remove(userId: string, key: string): PinChange {
    if (!hasUser(userId)) return { ok: false, reason: 'no-user' };
    const rows = this.#read(userId);
    if (!rows.some((p) => p.key === key && p.visible)) return { ok: true, changed: false };
    this.#write(userId, rows.map((p) => (p.key === key ? { ...p, visible: false } : { ...p })));
    return { ok: true, changed: true };
  }

  /**
   * Move one pin one place along the rail. The ends answer changed false; an
   * unknown key moves nothing. The move swaps the two rows the rail shows,
   * and renumbers by rewrite: the array is the order.
   */
  move(userId: string, key: string, delta: -1 | 1): PinChange {
    if (!hasUser(userId)) return { ok: false, reason: 'no-user' };
    const rows = this.#read(userId);
    const visible = rows.filter((p) => p.visible).map((p) => p.key);
    const at = visible.indexOf(key);
    const to = at + delta;
    if (at < 0 || to < 0 || to >= visible.length) return { ok: true, changed: false };
    const neighbour = visible[to];
    const next = rows.map((p) => ({ ...p }));
    const ia = next.findIndex((p) => p.key === key);
    const ib = next.findIndex((p) => p.key === neighbour);
    [next[ia], next[ib]] = [next[ib], next[ia]];
    this.#write(userId, next);
    return { ok: true, changed: true };
  }

  /** Drop every row for one person: the fresh start is whole or absent. */
  reset(userId: string): PinChange {
    if (!hasUser(userId)) return { ok: false, reason: 'no-user' };
    const rows = this.#read(userId);
    if (rows.length === 0) return { ok: true, changed: false };
    this.#write(userId, []);
    return { ok: true, changed: true };
  }

  #read(userId: string): StoredPin[] {
    try {
      const rows = parsePayload(this.#storage.read(pinStorageKey(userId)));
      this.#cache.set(userId, rows);
      return rows;
    } catch {
      return this.#cache.get(userId) ?? [];
    }
  }

  #write(userId: string, rows: StoredPin[]): void {
    const payload: StoredPayload = { v: 1, pins: rows };
    this.#cache.set(userId, rows.map((p) => ({ ...p })));
    try {
      this.#storage.write(pinStorageKey(userId), JSON.stringify(payload));
    } catch {
      // blocked or full storage: the answer stands, persistence is skipped
    }
  }
}

// ── the rail's collapsed posture (ported from _rail-state.ts) ────────────────────────────────────

export const RAIL_COLLAPSED_KEY = 'hh-shell.rail-collapsed';

/**
 * Silence means collapsed: the rail of pinned surfaces arrives as a narrow
 * strip, and only a person's own explicit expand, written as '0', keeps it
 * open. Garbage and future values degrade to the default posture, never an
 * error. Storage being unavailable entirely is the caller's catch, which
 * lands on collapsed too.
 */
export function railCollapsedFromStored(stored: string | null): boolean {
  return stored !== '0';
}

/** The one literal an explicit expand writes, and its collapse twin. */
export function railCollapsedToStored(collapsed: boolean): string {
  return collapsed ? '1' : '0';
}

// ── the rail's pin mode (GAPS-r2 row G1) ─────────────────────────────────────────────────────────

export const RAIL_PIN_KEY = 'hh-shell.rail-pin';

/**
 * The rail's pin mode, the board's two pin choices and their off twin: the
 * icons column pinned open, the wider rail body pinned open, or nothing
 * pinned (the rail waits as the toggle and reveals on hover or focus).
 */
export type RailPinMode = 'icons' | 'wide' | 'off';

/**
 * Silence means off, and garbage and future values degrade to off, never an
 * error: the rail stays a toggle until a person pins a posture of their own.
 */
export function railPinFromStored(stored: string | null): RailPinMode {
  return stored === 'icons' || stored === 'wide' ? stored : 'off';
}

/** The stored literal for a pin mode: the mode word itself. */
export function railPinToStored(pin: RailPinMode): string {
  return pin;
}

// ── the app switcher model, by role, over an injected manifest slice ─────────────────────────────

/**
 * The slice of manifest.yaml the shell injects: the frontends the device's
 * profile names, verbatim from the manifest (id, name, roles). The chrome
 * never copies the manifest; the shell hands the slice over and the model
 * reads it.
 */
export interface ManifestFrontend {
  id: string;
  name: string;
  roles: readonly string[];
}

/** One surface an app offers: the shell's own list, keyed by app id. */
export interface SurfaceOption {
  id: string;
  label: string;
}

/** Where the person is standing right now. An empty surfaceId is the app's
 * front door, which pins under the bare app key. */
export interface CurrentSurface {
  appId: string;
  surfaceId: string;
}

/** The pin key for a surface: a bare app key for a front door, app/surface
 * for anything deeper. This is the spelling pins are stored under. */
export function surfaceKey(ref: CurrentSurface): string {
  return ref.surfaceId === '' ? ref.appId : `${ref.appId}/${ref.surfaceId}`;
}

/** The apps the person's roles hold, in manifest order. No roles, no apps. */
export function appsForRoles(
  frontends: readonly ManifestFrontend[],
  roles: readonly string[],
): ManifestFrontend[] {
  const held = new Set(roles);
  return frontends.filter((f) => f.roles.some((r) => held.has(r)));
}

export interface SwitcherGroup {
  app: ManifestFrontend;
  surfaces: readonly SurfaceOption[];
}

/** The selector panel's groups: each app the roles hold, with its surfaces. */
export function switcherGroups(input: {
  frontends: readonly ManifestFrontend[];
  roles: readonly string[];
  surfaces: ReadonlyMap<string, readonly SurfaceOption[]>;
}): SwitcherGroup[] {
  return appsForRoles(input.frontends, input.roles).map((app) => ({
    app,
    surfaces: input.surfaces.get(app.id) ?? [],
  }));
}

/** The band's breadcrumb words for where the person is: the app, then the
 * surface. An unknown app or no current answers '', and the band renders its
 * quiet default instead. */
export function currentBreadcrumb(input: {
  frontends: readonly ManifestFrontend[];
  surfaces: ReadonlyMap<string, readonly SurfaceOption[]>;
  current: CurrentSurface | null;
}): string {
  if (input.current === null) return '';
  const app = input.frontends.find((f) => f.id === input.current?.appId);
  if (app === undefined) return '';
  if (input.current.surfaceId === '') return app.name;
  const surface = (input.surfaces.get(app.id) ?? []).find((s) => s.id === input.current?.surfaceId);
  return `${app.name} › ${surface === undefined ? input.current.surfaceId : surface.label}`;
}

/** Initials for the avatar chip, from a role label: the first letters of the
 * first two words, uppercased. A band account string reads "name, role": only
 * the part before the comma counts, and a parenthetical ("(demo)") is not a
 * word. Committed text carries role labels only. */
export function initialsOf(name: string): string {
  const words = (name.split(',')[0] ?? '')
    .replace(/\([^)]*\)/g, ' ')
    .trim()
    .split(/\s+/)
    .filter((w) => /^[\p{L}\p{N}]/u.test(w));
  if (words.length === 0) return '';
  return words
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase() ?? '')
    .join('');
}

// ── the sync state chip ──────────────────────────────────────────────────────────────────────────

/**
 * Exactly the sync statechart's states (statecharts/sync/machine.ts; the pin
 * is held by the package's test suite, so a state the machine grows and the
 * chrome misses turns the chrome's own suite red). The chip shows the state
 * the runner settles in, plus what the outbox's health says about it.
 */
export const SHELL_SYNC_STATES = [
  'offline',
  'unreachable',
  'idle',
  'pushing',
  'pulling',
  'conflict',
  'auth_expired',
] as const;

export type ShellSyncState = (typeof SHELL_SYNC_STATES)[number];

/** Three colour kinds, bound to the sheet's state ink roles. Colour never
 * carries the state alone: the label word and the note carry it too. */
export type SyncChipKind = 'good' | 'busy' | 'stop';

/** What the shell hands the chip: the machine's state, the open conflict
 * cards, and the outbox health (waiting, the stuck write's attempts, the
 * stalls that wait for a person). */
export interface SyncChipInput {
  state: ShellSyncState;
  openCards: number;
  waiting: number;
  attempts: number;
  stalls: number;
}

export interface SyncChipView {
  /** The chip's word, which is its accessible name beside its dot. */
  label: string;
  kind: SyncChipKind;
  /** One small line behind the word: the outbox depth, a stall, a retry. */
  note: string;
  /** True only when the person should act or look now. One attempt on a
   * stuck write is never an alarm (the offline store's own rule), and a
   * single dropped response already counts as one attempt. */
  alarm: boolean;
}

const LABELS: Record<ShellSyncState, string> = {
  idle: 'Synced',
  pulling: 'Pulling',
  pushing: 'Pushing',
  offline: 'Offline',
  unreachable: 'Reconnecting',
  conflict: 'to review',
  auth_expired: 'Sign in expired',
};

const KINDS: Record<ShellSyncState, SyncChipKind> = {
  idle: 'good',
  pulling: 'busy',
  pushing: 'busy',
  offline: 'stop',
  unreachable: 'busy',
  conflict: 'stop',
  auth_expired: 'stop',
};

export function syncChipView(input: SyncChipInput): SyncChipView {
  const label =
    input.state === 'conflict' ? `${Math.max(0, input.openCards)} ${LABELS.conflict}` : LABELS[input.state];
  const kind = KINDS[input.state];
  const notes: string[] = [];
  if (input.state === 'pushing' && input.waiting > 0) notes.push(`${input.waiting} in the outbox`);
  if (input.stalls > 0) notes.push('a check needs you');
  const alarm = input.attempts >= 2 && input.state !== 'offline' && input.state !== 'auth_expired';
  if (alarm) notes.push('retrying');
  return { label, kind, note: notes.join(' · '), alarm };
}

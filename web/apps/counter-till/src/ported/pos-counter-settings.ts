// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-counter-settings.ts; adapted for HH.

// pos-counter-settings.ts: COUNTER SETTINGS: the dealer's key roster, the hot-list board, and the
// panel defaults, on one admin page (the commander's rulings R1 and R2).
//
// WHY THIS SCREEN EXISTS. The ruling was "Use bistrack patterns then make it admin user
// configurable", and until now only the first half was reachable: the roster and the tile set were
// server-curated with no surface to curate them FROM. A configurable thing with no editor is a
// hard-coded thing with extra steps.
//
// WHAT IT EDITS, AND WHERE EACH PIECE LIVES. Nothing new: it is three views of the ONE presentation
// substrate the counter already reads (surface `pos.counter`, the 0140/0149 idiom), through the
// settings kit's own admin-gated PUT.
//
// KEYS kind `action`, id `<action>.<key>`: the roster hotkeys.go resolves.
// HOT LIST kind `chip`, id `<sku>` : the tiles, in the order they are curated, which
// since R2 is also the CATEGORY order.
// PANELS kind `panel`, id `legend`/`hotlist`: the dealer's default for a job shape. A person
// still overrides their own in Counter view.
//
// THE COLLISION IS SHOWN BEFORE THE SAVE, WHICH IS THE POINT. A roster where two actions claim one
// key resolves by row order (hotkeys.go drops the loser silently), so an administrator who found
// out afterwards would find out by a counter person telling them a key stopped working. Here the
// clash is refused at the moment of pressing, named by the action it would break.
//
// THE SERVER IS THE AUTHORITY ON WHO MAY DO THIS. The kit refuses a non-administrator with a
// sentence and this surface shows that sentence rather than evaluating any authority of its own
// (the acceptance-settings posture).

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import { drawIcon, iconForId } from '@hardscapehouse/pos-kit/src/ported/_icon-consumer.js';
import { surfaceConfigUrl } from './surface-config-url.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import '@hardscapehouse/pos-kit/src/ported/product-thumb.js';
import { listProducts, putProductImage, deleteProductImage, type PosProduct } from '@hardscapehouse/pos-kit/src/ported/pos-api.js';
import {
  BISTRACK_ROSTER,
  HOTKEY_ORDER,
  HOTKEY_LABELS,
  chordFromEvent,
  collisionFor,
  keycap,
  validChord,
  type PosHotkey,
} from './_pos-hotkeys.js';
import {
  PLATFORM_COUNTER_SURFACE,
  asPlatformView,
  draftFrom,
  effectiveVerdict,
  otherSurface,
  reservedOf,
  rosterFor,
  type PlatformHotkeyView,
} from './_platform-hotkeys.js';
import {
  COUNTER_PANELS,
  PANEL_KIND,
  PANEL_LABELS,
  POS_COUNTER_SURFACE_ID,
  asSurfaceItems,
  panelView,
  type SurfaceItem,
} from './_pos-counter-view.js';
import './settings-history.js';
import type { PolicyChangeRow } from './settings-history-logic.js';
import { POS_HISTORY_WORDS, SUPPLIER_DEPOSIT_CANCEL_WORDS } from './pos-counter-settings-logic.js';

const PROFILES = ['counter', 'yard', 'driver', 'manager', 'admin'] as const;
type Profile = (typeof PROFILES)[number];

/**
 * The pos_policy row this screen reads and writes, in the contract's shape.
 * Every field the write replaces is held, so a one-knob save cannot clobber a
 * knob this screen does not show.
 */
type CounterPolicyRow = {
  branch_id: string;
  cod_enabled: boolean;
  restocking_fee_bps: number;
  notes: string;
  line_presentation: string;
  quick_chips_enabled: boolean;
  workspace_split_pct: number;
  workspace_arrangement: string;
  lookup_presentation: string;
  margin_visibility: string;
  supplier_deposit_cancel: string;
  quick_chip_set?: string[];
};

/** The kit item kind one hot-list tile is filed under (quickpicks.go's `chip`). */
const CHIP_KIND = 'chip';
/** The kit item kind one bound key is filed under (hotkeys.go's `action`). */
const ACTION_KIND = 'action';

@customElement('hos-pos-counter-settings')
export class HosPosCounterSettings extends LitElement {
  @property({ attribute: 'branch-id' }) branchId = '';

  /** The roster being edited, action -> chord. Seeded from the dealer's rows, else the defaults. */
  @state() private keys: Record<string, string> = {};
  /** Which action is waiting for a keystroke, or ''. */
  @state() private capturing = '';
  /** The curated tile set, in board order (lowercased SKUs). */
  @state() private tiles: string[] = [];
  /** The dealer's panel defaults for the chosen profile. */
  @state() private panels: Record<string, boolean> = {};
  /** Every active product, for the tile picker and the thumbnails. */
  @state() private catalogue: PosProduct[] = [];
  @state() private profile: Profile = 'counter';
  @state() private busy = false;
  @state() private error = '';
  @state() private notice = '';
  /** The collision or bad-chord refusal, shown at the moment of pressing. */
  @state() private keyRefusal = '';
  /**
   * ITEM 4c · the cross-substrate NOTE. A key this dealer is binding at the till that the app
 * shell also uses somewhere else: which works, on purpose (the till listens in the capture phase
   * and stops the event; the shell listens in bubble), and would otherwise look like a mistake an
   * administrator ought to go and undo.
   *
   * SEPARATE FROM keyRefusal AND NOT A FLAG ON IT: a refusal means the key was not taken, this means
   * it was. One slot would become one role attribute, and `role="alert"` on “this worked” is how a
   * screen teaches people to ignore alerts.
   */
  @state() private keyNote = '';
  /**
   * The platform view behind that note and behind the reserved-key refusal. Held whole rather than
 * reduced to a draft on read, because the two tiers this screen must not collide with: the shell's
 * roster and the keys the shell hardcodes: are on the same payload the platform roster arrives on.
   */
  @state() private platformView: PlatformHotkeyView | null = null;
 /** Which tiles have had a photo written this session: the thumb cache buster. */
  @state() private photoStamp: Record<string, number> = {};
  /** The dealer's counter-RULE change history (pos_policy), newest first. */
  @state() private history: PolicyChangeRow[] = [];
  /** The server's own sentence when the history read failed; '' means it did not. */
  @state() private historyFault = '';
  /**
   * The dealer's counter policy row (pos_policy), held WHOLE because the policy
   * write replaces the row: a one-knob save PUTs the row it read with the one
   * field moved, never a partial body that would clobber the knobs this screen
   * does not show. Null means the read has not answered or refused.
   */
  @state() private policyRow: CounterPolicyRow | null = null;
  /** The sentence when the policy read failed; '' means it did not. */
  @state() private policyFault = '';
  /** The deposit disposition draft: the select's value, saved on Save. */
  @state() private depositCancelDraft = 'CREDIT_BACK';

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
        /* ── THE SCALE, RETUNED TO LAND ON THE APP'S STEPS ────────────────────────────────────
           These were 0.95 / 0.83 / 0.72rem, which at a 16px root are 15.2 / 13.28 / 11.52px:
           three sizes that agree with nothing else this screen composes with. Measured on the live
           stack, the overlay rendered SIX distinct font sizes against a budget of four
           (SKILL.md:141): {11.52, 12, 13, 13.28, 14, 15.2}. The three strangers were not this
           file's own: 12 is the shared history's --sh-caption, and 13/14 are emptyStateStyles'
           .hos-blank and its h4, both px, both used by every surface in the app.

           So the fix is not "pick a fourth number", it is to stop being the one surface off the
           app's steps. The ratios are kept and the values are chosen to resolve to the integers
           every shared component already uses at the default root: 0.75rem = 12, 0.8125rem = 13,
           0.9375rem = 15: and rem is kept rather than traded for px, so a reader who has raised
           their browser's base size still gets a bigger till. The overlay then reads caption 12,
           body 13, section 15, with the shared empty state's h4 at 14 as its one title step: four,
           and every one of them a step some other screen already stands on. */
        --cs-section: 0.9375rem;
        --cs-body: 0.8125rem;
        --cs-caption: 0.75rem;
        font-size: var(--cs-body);
      }
      /* The shared history is the last piece of the scale above, and it is the only one this file
         can reach: --sh-caption/--sh-body are declared on ITS :host as 12/14px, and a normal
         declaration from the outer tree beats a :host one, so the panel adopts this screen's steps
         without settings-history.ts having to know which screen it is standing in. */
      hos-settings-history {
        --sh-caption: var(--cs-caption);
        --sh-body: var(--cs-body);
      }
      /* ── THE FORM'S COLUMNS (wave-1 layout contract) ──────────────────────────────────────────
         This screen is a FORM, and the contract's rule for a form is that it owes the width it is
         given a column count. It used to be handed a 760px column by pos-terminal.ts's .cspanel and
         stacked five boxes down it: a tile row measured 734px on the live stack at every width
         from 1024 to 1920, which is the whole story: a settings screen that answered a 1920px
         monitor and a 1024px till with the same 734px row. The panel is now the counter's own
         measure (1180px), and a form that answered THAT with one 1146px-wide column would have
         traded 844px of dead page for a row whose label sits at x=0 and whose control sits at
         x=1100. Same defect, wider.

         auto-fit + minmax and NO media query, which is the contract's own argument: the columns
         respond to the PANEL's real width, so the overlay works the same inside a popped-out
         window, a narrowed browser and a 1024 till without a breakpoint anyone has to keep in step.
         align-items: start because these boxes are five different heights and a stretched box
         with three rows in it is a box with a hole in the bottom.

         THE MINIMUM IS THE APP'S, RESTATED, NOT INVENTED. --hos-form-col (320px, tokens.css) is the
         widest column minimum this product already ships, and the two narrow boxes here: the key
         roster and the panel defaults, both .krow at 1fr auto auto: fit it with room. The tile
         board does not: .trow is SEVEN columns (thumb · name · up · down · photo · clear · remove)
         and measures ~470px before the name column has any width at all, so packing it into a
         320px track would put an ellipsis through every product name to buy space nothing else
         needed. It spans instead: see the grid-column rule below. --hos-pane-columns is used
         verbatim rather than re-typed, so if the app's form column ever moves this moves with it. */
      [data-testid='pos-counter-settings'] {
        display: grid;
        grid-template-columns: var(--hos-pane-columns);
        gap: var(--hos-space-5);
        align-items: start;
      }
      /* Four things take the whole width rather than a track. The tile board is the seven-column row
         argued above; the history is a table of old values, and a table squeezed into a third of the
         panel is a table that scrolls sideways inside a form: the one thing the contract's
         acceptance clause forbids outright. The save's outcome lines go with them because a
         role="alert" that lands in the second column, beside a box it is not about, is worse than
         no message: the sentence has to appear under the whole form, where the person's eyes are. */
      [data-testid='cs-tiles'],
      hos-settings-history,
      .err,
      .ok {
        grid-column: 1 / -1;
      }
      .box {
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-card);
        background: var(--hos-deep-earth);
        padding: 12px;
        /* The stack's 10px margin became the grid's gap. Leaving both would have put 30px between
           the rows of a grid whose columns are 20px apart, which reads as a layout bug rather than
           as rhythm. */
        margin-bottom: 0;
      }
      h3 {
        margin: 0 0 8px;
        font-size: var(--cs-section);
        display: flex;
        align-items: center;
        gap: 8px;
      }
      button.info {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        font: inherit;
        font-size: var(--cs-caption);
        width: 20px;
        height: 20px;
        padding: 0;
        line-height: 1;
        /* row 523: one circle. The button's border is gone; the drawn mark's circle is the
           ring, at the button's size, so the hit area is unchanged */
        border: none;
        background: transparent;
        color: var(--hos-text-dim);
        cursor: help;
      }
      button.info:hover {
        color: var(--hos-stone-amber-text);
      }
      label.f {
        display: block;
        font-size: var(--cs-caption);
        color: var(--hos-text-dim);
        margin-bottom: 3px;
      }
      select,
      input {
        font: inherit;
        font-size: var(--cs-body);
        padding: 5px 7px;
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        color: var(--hos-text);
      }
      /* One row per bound action: what it does, the cap, the control that changes it. */
      .krow {
        display: grid;
        grid-template-columns: 1fr auto auto;
        align-items: center;
        gap: 10px;
        padding: 6px 0;
        border-top: 1px solid var(--hos-line);
      }
      .krow:first-of-type {
        border-top: 0;
      }
      .cap {
        display: inline-grid;
        place-items: center;
        min-width: 44px;
        padding: 3px 8px;
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        font-family: var(--hos-font-mono);
        font-size: var(--cs-caption);
      }
      .cap.host {
        border-style: dashed;
      }
      .cap.none {
        color: var(--hos-text-dim);
      }
      button.q {
        font: inherit;
        font-size: var(--cs-caption);
        padding: 4px 10px;
        background: transparent;
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        color: var(--hos-text);
        cursor: pointer;
      }
      /* vertical-align: middle on the drawn glyph keeps the icon on the
      text middle and the line box the character left behind, so the button keeps its exact
      height (PR 55 review round; the quick-action-bar precedent centers, this keeps the box) */
      button.q svg {
        vertical-align: middle;
      }
      button.q:hover {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
      }
      button.q[aria-pressed='true'] {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
        background: color-mix(in srgb, var(--hos-stone-amber) 12%, transparent);
      }
      /* The one gold per panel: the save. */
      button.go {
        font: inherit;
        font-size: var(--cs-body);
        font-weight: 700;
        padding: 7px 14px;
        background: var(--hos-stone-amber);
        border: 1px solid var(--hos-stone-amber);
        border-radius: var(--hos-radius-control);
        color: var(--hos-stone-amber-ink);
        cursor: pointer;
      }
      button.go:hover {
        background: var(--hos-stone-amber-hover);
        border-color: var(--hos-stone-amber-hover);
        color: var(--hos-stone-amber-ink); /* E6: a theme-stable fill pins its theme-stable ink */
      }
      button.go[disabled] {
        background: var(--hh-disabled-fill);
        border-color: var(--hh-disabled-line);
        color: var(--hh-disabled-ink);
        opacity: 1;
        cursor: not-allowed;
      }
      .foot {
        display: flex;
        align-items: center;
        gap: 8px;
        margin-top: 10px;
      }
 /* thumb · name · up · down · photo · clear photo · remove: seven, so nothing wraps onto a
         second line and every tile row is the same height. */
      .trow {
        display: grid;
        grid-template-columns: auto 1fr auto auto auto auto auto;
        align-items: center;
        gap: 8px;
        padding: 5px 0;
        border-top: 1px solid var(--hos-line);
      }
      .trow:first-of-type {
        border-top: 0;
      }
      .trow .nm {
        min-width: 0;
      }
      .trow .nm b {
        display: block;
        font-weight: 600;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .trow .nm span {
        font-size: var(--cs-caption);
        color: var(--hos-text-dim);
      }
      .pickrow {
        display: flex;
        gap: 8px;
        align-items: flex-end;
        margin-top: 8px;
      }
      .pickrow select {
        flex: 1;
        min-width: 0;
      }
      .empty b {
        display: block;
      }
      .empty span {
        display: block;
        margin-top: 2px;
        font-size: var(--cs-caption);
        color: var(--hos-text-dim);
      }
      .err {
        color: var(--hos-safety-red);
        margin-top: 8px;
      }
      .ok {
        color: var(--hos-success);
        margin-top: 8px;
      }
      /* The cross-substrate note. Dim ink, not red: the key is bound and nothing failed. */
      .hint {
        color: var(--hos-text-dim);
        font-size: var(--cs-caption);
        margin-top: 8px;
      }
      input[type='file'] {
        display: none;
      }
    `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
    void this.load();
    void this.loadCatalogue();
    void this.loadHistory();
    void this.loadPolicy();
  }

  disconnectedCallback(): void {
    this.stopCapture();
    super.disconnectedCallback();
  }

  // ── reads ────────────────────────────────────────────────────────────────────────────────────

  private async load(): Promise<void> {
    this.error = '';
    try {
      const res = await api(
        surfaceConfigUrl(POS_COUNTER_SURFACE_ID, { branchId: this.branchId, profile: this.profile }),
      );
      if (!res.ok) {
        this.error = 'Could not read the counter settings.';
        return;
      }
      const body = (await res.json()) as unknown;
      const role = asSurfaceItems(body, 'role_items');
      this.keys = rosterFrom(role, await this.platformKeys());
      this.tiles = role.filter((r) => r.kind === CHIP_KIND && r.visible).map((r) => r.id);
      this.panels = panelView(role, []);
    } catch {
      this.error = 'Could not reach the server.';
    }
  }

  /**
 * The PLATFORM roster for this counter: what this dealer inherits when they have curated nothing.
   *
   * D-111 made this read necessary. Before it, an uncurated dealer's rows were shown from
   * BISTRACK_ROSTER, a constant in this bundle, which was true because the level beneath the dealer
   * WAS that constant. It is now a table a platform operator edits, so showing the constant would
   * show an administrator keys their counter is not actually using, and would make "Reset to
   * platform" a button whose destination is not what the screen displays afterwards.
   *
   * An unreadable platform roster answers EMPTY rather than throwing, and rosterFrom then falls to
 * the shipped BisTrack list: the same degrade the server takes, for the same reason: a settings
   * screen that will not open because a second read timed out is worse than one showing the floor.
   */
  private async platformKeys(): Promise<Record<string, string>> {
    try {
      const res = await api('/api/v1/platform/hotkeys');
      if (!res.ok) return {};
      const view = asPlatformView(await res.json());
      // Kept whole for the press-time checks. An unread view leaves them silent, never wrong: the
      // same-roster collision below has never needed it and still does not.
      this.platformView = view;
      return draftFrom(rosterFor(view, PLATFORM_COUNTER_SURFACE));
    } catch {
      return {};
    }
  }

  /**
   * The COUNTER RULES change history: who moved a pos_policy knob, and what it was before.
   *
   * ── WHY IT IS ON THIS SCREEN ─────────────────────────────────────────────────────────────────
   *
   * Counter settings is where an administrator already comes to configure the counter, so it is
   * where the record of counter configuration belongs. The deposit disposition box above is this
   * screen's first typed-policy editor; before it, pos_policy was dealer-configurable over the API
   * and by nobody else, which was precisely the accountability gap this panel closed: the knobs
   * that decide who may receive a dealer's margin and a customer's past-due balance could be moved
   * with no record of who moved them. The trail predates the editor, so it also covers every knob
   * moved over the API.
   *
   * A FAILURE NEVER BLOCKS THE SCREEN. It lands in the disclosure's own fault box with a retry; the
   * keys, tiles and panels above it stay editable.
   */
  private async loadHistory(): Promise<void> {
    this.historyFault = '';
    try {
      const res = await api(
        `/api/v1/pos/policy/history${this.branchId ? `?branch_id=${encodeURIComponent(this.branchId)}` : ''}`,
      );
      if (!res.ok) {
        this.historyFault = (await readErr(res)) || `request failed (${res.status})`;
        return;
      }
      const body = (await res.json()) as { items?: PolicyChangeRow[] };
      this.history = body.items ?? [];
    } catch {
      this.historyFault = 'The server could not be reached.';
    }
  }

  /**
   * The counter policy row behind the one money rule this screen edits: the
   * disposition a cancelled special order's supplier deposit takes. Held whole
   * (see CounterPolicyRow) because the write replaces the row. A failure never
   * blocks the screen: the rule's box shows the fault and the control stays
   * disabled, the keys, tiles and panels above it keep working.
   */
  private async loadPolicy(): Promise<void> {
    this.policyFault = '';
    try {
      const res = await api(
        `/api/v1/pos/policy${this.branchId ? `?branch_id=${encodeURIComponent(this.branchId)}` : ''}`,
      );
      if (!res.ok) {
        this.policyFault = (await readErr(res)) || `request failed (${res.status})`;
        return;
      }
      const row = (await res.json()) as CounterPolicyRow;
      this.policyRow = row;
      this.depositCancelDraft = row.supplier_deposit_cancel || 'CREDIT_BACK';
    } catch {
      this.policyFault = 'The server could not be reached.';
    }
  }

  /**
   * Save the deposit disposition: the read row with the one field moved. The
   * server walls the write to administrators (the surface's own posture: the
   * server is the authority on who may do this, and this screen shows its
   * sentence rather than evaluating any authority of its own).
   */
  private async saveDepositCancel(): Promise<void> {
    const row = this.policyRow;
    if (!row || this.busy) return;
    this.busy = true;
    this.error = '';
    this.notice = '';
    try {
      const res = await api('/api/v1/pos/policy', {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ ...row, supplier_deposit_cancel: this.depositCancelDraft }),
      });
      if (!res.ok) {
        this.error = (await readErr(res)) || `Could not save the deposit rule (${res.status}).`;
        return;
      }
      const saved = (await res.json()) as CounterPolicyRow;
      this.policyRow = saved;
      this.depositCancelDraft = saved.supplier_deposit_cancel || 'CREDIT_BACK';
      this.notice = 'Deposit rule saved. A cancelled special order now takes this disposition.';
      await this.loadHistory();
    } catch {
      this.error = 'Could not reach the server.';
    } finally {
      this.busy = false;
    }
  }

  /** The catalogue behind the tile picker and the tile rows. A failure leaves the picker empty. */
  private async loadCatalogue(): Promise<void> {
    try {
      this.catalogue = (await listProducts()).filter((p) => p.is_active);
    } catch {
      this.catalogue = [];
    }
  }

  private productBySku(sku: string): PosProduct | undefined {
    return this.catalogue.find((p) => p.sku.toLowerCase() === sku.toLowerCase());
  }

  // ── the keys ─────────────────────────────────────────────────────────────────────────────────

  /**
   * Arm the capture for one action. The listener goes on the WINDOW in the capture phase for the
   * same reason the counter's own does: an F-key pressed with focus anywhere must reach it, and
   * nothing downstream (the shell's dealer map included) may act on a keystroke that is being
   * recorded rather than obeyed.
   */
  private startCapture(action: string): void {
    this.stopCapture();
    this.capturing = action;
    this.keyRefusal = '';
    window.addEventListener('keydown', this.onCapture, true);
  }

  private stopCapture(): void {
    if (this.capturing) window.removeEventListener('keydown', this.onCapture, true);
    this.capturing = '';
  }

  private onCapture = (e: KeyboardEvent): void => {
    const action = this.capturing;
    if (!action) return;
    if (e.key === 'Escape' || e.key === 'Tab') {
 // Escape abandons and Tab walks on: a capture nobody can leave is a trap.
      this.stopCapture();
      return;
    }
    const chord = chordFromEvent(e);
    if (!chord) {
      if (e.key === 'Enter' || e.key === ' ') return; // the press that armed it, arriving late
      e.preventDefault();
      e.stopPropagation();
      this.keyRefusal = 'That key types into the sale. Use a function key, or hold Ctrl or Alt with a letter.';
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    if (!validChord(chord)) {
      this.keyRefusal = 'That combination cannot be bound. Use a function key, or Ctrl or Alt with a letter.';
      return;
    }
    // ITEM 4c · THE KEYS THE SHELL OWNS OUTRIGHT, first, because they are not a collision to be
 // negotiated: no tier holds them and no tier can free them, so naming another action would be
    // an offer this screen cannot honour. Silent until the platform read lands, which is the same
    // degrade the roster read takes: this screen must open whether or not a second read answered.
    const shellHeld = effectiveVerdict(PLATFORM_COUNTER_SURFACE, action, chord, {
      draft: {},
      reserved: reservedOf(this.platformView),
    });
    if (shellHeld.kind === 'taken') {
      this.keyRefusal = shellHeld.sentence;
      return;
    }
    const clash = collisionFor(this.rosterDraft(), action, chord);
    if (clash) {
      // THE COLLISION, BEFORE THE SAVE. Named by the action it would break, so the way forward is
      // obvious: free that key first, or pick another one.
      this.keyRefusal = `${keycap(chord)} already runs ${HOTKEY_LABELS[clash] ?? clash}. Change that one first, or pick another key.`;
      return;
    }
    this.keys = { ...this.keys, [action]: chord };
    this.keyRefusal = '';
    // ITEM 4c · THE OTHER SUBSTRATE, WARNED ABOUT AND NEVER REFUSED. F2, F4 and F9 are claimed on
    // both rosters on the shipped default and that is deliberate. An administrator who saw no word
    // about it would either not know, or would find out from a counter person; one who saw a REFUSAL
    // would be blocked from the roster this product ships with.
    const shared = effectiveVerdict(PLATFORM_COUNTER_SURFACE, action, chord, {
      draft: {},
      other: rosterFor(this.platformView, otherSurface(PLATFORM_COUNTER_SURFACE)),
    });
    this.keyNote = shared.kind === 'shared' ? shared.sentence : '';
    this.stopCapture();
    void this.updateComplete.then(() =>
      this.renderRoot.querySelector<HTMLElement>(`[data-testid="cs-key-${action}"]`)?.focus(),
    );
  };

  /** The draft roster as the collision check reads it. */
  private rosterDraft(): PosHotkey[] {
    return HOTKEY_ORDER.filter((a) => this.keys[a]).map((a) => ({ action: a, key: this.keys[a] }));
  }

  /** One action, reset to nothing. An action with no key simply draws no keycap on the legend. */
  private clearKey(action: string): void {
    const next = { ...this.keys };
    delete next[action];
    this.keys = next;
    this.keyRefusal = '';
    this.keyNote = '';
  }

  /**
 * Back to the PLATFORM roster, in one action: and it is a DELETE, not a redraft.
   *
   * WHY THIS CHANGED SHAPE UNDER D-111. It used to write BISTRACK_ROSTER into the local draft and
   * leave the administrator to press Save, which WROTE those values into the dealer's own level.
   * That looked like a reset and was the opposite of one: after it, this dealer was permanently
   * curated with what were really defaults, and no level beneath them could ever answer again.
   * Harmless while the level beneath was a constant in source; fatal now that a platform operator
   * can change it, because the whole promise of the platform tier is that a dealer who has not
   * overridden inherits the change live.
   *
 * So reset means what it means everywhere else in this substrate: DELETE the dealer's rows and
   * let the level below answer. The kit's role-items DELETE is scoped to the `action` kind, so the
   * hot list and the panel defaults on this same surface are untouched.
   *
   * The re-read afterwards is what puts the platform roster on the screen, so an administrator sees
   * what they have just inherited rather than being told about it.
   */
  private async resetKeys(): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    this.error = '';
    this.notice = '';
    this.keyRefusal = '';
    this.keyNote = '';
    try {
      const res = await api(
        `/api/v1/surfaces/${POS_COUNTER_SURFACE_ID}/items?profile=${encodeURIComponent(this.profile)}&kind=${ACTION_KIND}`,
        { method: 'DELETE' },
      );
      if (!res.ok) {
        this.error = (await readErr(res)) || `Could not reset the counter keys (${res.status}).`;
        return;
      }
      this.notice = 'Keys reset. This counter now uses the platform roster again.';
      await this.load();
    } catch {
      this.error = 'Could not reach the server.';
    } finally {
      this.busy = false;
    }
  }

  private async saveKeys(): Promise<void> {
    const items: SurfaceItem[] = HOTKEY_ORDER.filter((a) => this.keys[a]).map((a, i) => ({
      kind: ACTION_KIND,
      id: `${a}.${this.keys[a]}`,
      position: i,
      visible: true,
    }));
    if (items.length === 0) {
      this.error = 'Bind at least one key, or the counter has nothing to show on the legend.';
      return;
    }
    await this.put(items, `Keys saved for the ${this.profile} shape: live at every till on the next sale.`);
  }

  // ── the hot list ─────────────────────────────────────────────────────────────────────────────

  private moveTile(sku: string, dir: -1 | 1): void {
    const next = [...this.tiles];
    const i = next.indexOf(sku);
    const j = i + dir;
    if (i < 0 || j < 0 || j >= next.length) return;
    [next[i], next[j]] = [next[j], next[i]];
    this.tiles = next;
    void this.updateComplete.then(() =>
      this.renderRoot.querySelector<HTMLElement>(`[data-testid="cs-tile-${sku}"]`)?.focus(),
    );
  }

  private removeTile(sku: string): void {
    this.tiles = this.tiles.filter((s) => s !== sku);
  }

  private addTile(sku: string): void {
    const id = sku.trim().toLowerCase();
    if (!id || this.tiles.includes(id)) return;
    this.tiles = [...this.tiles, id];
  }

  private async saveTiles(): Promise<void> {
    const items: SurfaceItem[] = this.tiles.map((sku, i) => ({
      kind: CHIP_KIND,
      id: sku,
      position: i,
      visible: true,
    }));
    if (items.length === 0) {
 // An empty curated set is not a statement: the counter would fall straight back to the
      // derived board and the admin would think the save failed.
      this.error = 'Add at least one tile, or leave the board to fill itself from what the yard sells.';
      return;
    }
    await this.put(items, `Hot list saved for the ${this.profile} shape: the counter opens on it now.`);
  }

  /** The photo for one tile. Manager-gated server-side; the refusal arrives in words. */
  private async setPhoto(product: PosProduct, file: File): Promise<void> {
    this.busy = true;
    this.error = '';
    this.notice = '';
    try {
      const res = await putProductImage(product.id, file);
      if (!res.ok) {
        this.error = (await readErr(res)) || `Could not save that photo (${res.status}).`;
        return;
      }
      this.photoStamp = { ...this.photoStamp, [product.id]: Date.now() };
      this.notice = `Photo set for ${product.name}.`;
    } catch {
      this.error = 'Could not reach the server.';
    } finally {
      this.busy = false;
    }
  }

  private async clearPhoto(product: PosProduct): Promise<void> {
    this.busy = true;
    this.error = '';
    this.notice = '';
    try {
      const res = await deleteProductImage(product.id);
      if (!res.ok) {
        this.error = (await readErr(res)) || `Could not remove that photo (${res.status}).`;
        return;
      }
      this.photoStamp = { ...this.photoStamp, [product.id]: Date.now() };
      this.notice = `Photo removed from ${product.name}.`;
    } catch {
      this.error = 'Could not reach the server.';
    } finally {
      this.busy = false;
    }
  }

  // ── the panels ───────────────────────────────────────────────────────────────────────────────

  private async savePanels(): Promise<void> {
    const items: SurfaceItem[] = COUNTER_PANELS.map((id, i) => ({
      kind: PANEL_KIND,
      id,
      position: i,
      visible: this.panels[id] !== false,
    }));
    await this.put(items, `Starting view saved for the ${this.profile} shape.`);
  }

  // ── the one write ────────────────────────────────────────────────────────────────────────────

  /**
   * Every save is the same admin-gated PUT. The kit replaces only the KINDS present in the body, so
   * saving the keys cannot silently erase the board and saving the board cannot erase the keys.
   */
  private async put(items: SurfaceItem[], done: string): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    this.error = '';
    this.notice = '';
    try {
      const res = await api(`/api/v1/surfaces/${POS_COUNTER_SURFACE_ID}/items`, {
        method: 'PUT',
        body: JSON.stringify({ profile: this.profile, items }),
      });
      if (!res.ok) {
 // The server's sentence IS the message: a 422 arrives in words, a 403 names the admin gate.
        this.error = (await readErr(res)) || `Could not save the counter settings (${res.status}).`;
        return;
      }
      this.notice = done;
      await this.load();
    } catch {
      this.error = 'Could not reach the server.';
    } finally {
      this.busy = false;
    }
  }

  // ── render ───────────────────────────────────────────────────────────────────────────────────

  render() {
    return html`<div data-testid="pos-counter-settings">
      ${/* THE SWITCH NAMES WHAT IT RESCOPES, AND SO DOES EVERY SAVE UNDER IT.
            Measured at 1920: this box is 369×74 holding a 91px select, standing beside cs-keys at
            369×285. The @change below re-reads keys, tiles AND panels, so one control in one corner
            silently repoints three panels in the other columns: and not one of them said so. Their
            headings were "Counter keys", "Starts with", "Hot list": three absolute nouns over three
            per-shape rosters. An administrator who picked "yard" and then pressed Save keys was
            told "Keys saved", with nothing on screen naming the roster that had just been written.

            Dispatch already does this correctly: <h3>What each job shape sees on the board</h3>
            sits directly above its own selector (dispatch-settings.ts:451-457): so this is the
            product's own idiom, not a new one. The heading here says what the switch is for; each
            panel's own heading says which shape it is showing; and each confirmation names the
            shape it wrote (see saveKeys/saveTiles/savePanels).

            WHAT THIS DELIBERATELY DOES NOT DO: move the card. The 211px of dead track beside it is
            real and measured, but where the scope switch belongs in the grid is a design call and
            the critique itself files the remedy as opinion. Recorded, not applied. */ ''}
      <div class="box">
        <h3>What each job shape sees at the counter</h3>
        <label class="f" for="cs-profile">Job shape</label>
        <select
          id="cs-profile"
          data-testid="cs-profile"
          .value=${this.profile}
          @change=${(e: Event) => {
            this.profile = (e.target as HTMLSelectElement).value as Profile;
            this.notice = '';
            // AND RE-READ. Changing the job shape used to change only what the next SAVE was
            // addressed to; the rows on screen stayed the caller's own, so an admin picked "yard",
            // saw the counter's keys, and saved those onto the yard.
            void this.load();
          }}
        >
          ${PROFILES.map((p) => html`<option value=${p} ?selected=${p === this.profile}>${p}</option>`)}
        </select>
      </div>
      ${/* THE NARROW BOXES RUN TOGETHER, AND THE WIDE ONE FOLLOWS THEM. Tiles used to sit between
            keys and panels, which cost nothing in a single stack and costs a hole in a grid: the
            board spans every track, so it ends the row it lands in, and at three columns that left
            track 3 empty and stranded "Starts with" alone on a row of its own two rows further
            down. Reordered in the MARKUP and not with `order:`/`grid-auto-flow: dense` on purpose:
            either of those would have made the tab order disagree with the reading order, which is
            a worse defect than a gap and an invisible one to whoever fixes the gap next.
            The reading order this gives is also the better one: the two small rosters (which keys,
            which panels) together, then the board somebody scrolls, then the history. */ ''}
      ${this.renderKeys()} ${this.renderPanels()} ${this.renderTiles()} ${this.renderMoneyRule()}
      ${/* LAST, AND BEHIND A DISCLOSURE. A history is a look-something-up surface, not a next
            action; a table of old values above the controls somebody came here to change would be
            the screen arguing with itself about what it is for.

            WHAT THE TRAIL ACTUALLY COVERS, SAID EXACTLY. `subject` used to be "the counter rules",
            which the shared panel renders as "Every change to the counter rules is recorded here
            with who made it and what it was before." Measured on the live stack: a real save on this
            screen: rebinding a key: answers "Keys saved: live at every till on the next sale."
            and this panel still reads "Nothing has been changed yet", because the trail is
            pos_policy and the keys, the hot list and the panel defaults are surface items. The
            parked trail is deliberate and argued above; the SENTENCE was the defect, because "the
            counter rules" is precisely what the administrator has just changed.

            The nine diffed fields (core/internal/pos/policy_history.go) are cod_enabled,
            restocking_fee_bps and margin_visibility: money: and
            line_presentation, lookup_presentation, quick_chips_enabled, quick_chip_set,
            workspace_arrangement and workspace_split_pct: layout, and supplier_deposit_cancel:
            deposits. Nothing else. So that is what the
            noun phrase says. It is still one noun phrase, which is the whole contract with the
            shared panel; it is simply the true one. */ ''}
      <hos-settings-history
        data-testid="cs-history"
        .rows=${this.history}
        .vocabulary=${POS_HISTORY_WORDS}
        .retry=${() => void this.loadHistory()}
        .fault=${this.historyFault}
        .busy=${this.busy}
        subject="the counter's money and layout rules"
      ></hos-settings-history>
      ${this.error ? html`<p class="err" role="alert" data-testid="cs-error">${this.error}</p>` : nothing}
      ${this.notice ? html`<p class="ok" role="status" data-testid="cs-notice">${this.notice}</p>` : nothing}
    </div>`;
  }

  private renderKeys() {
    return html`<div class="box" data-testid="cs-keys">
      <h3>
        Counter keys · ${this.profile}
        <button
          class="info"
          type="button"
          aria-label="About counter keys"
          title="F4 advances and F12 takes payment, the way the counter system before this one did. Press Set, then press the key you want: function keys, or Ctrl or Alt with a letter."
        >
          ${drawIcon(iconForId('pencil:info'), 19)}
        </button>
      </h3>
      ${HOTKEY_ORDER.map((action) => {
        const chord = this.keys[action] ?? '';
        const owned = isBrowserKey(chord);
        return html`<div class="krow">
          <span>${HOTKEY_LABELS[action] ?? action}</span>
          <span
            class="cap ${owned ? 'host' : ''} ${chord ? '' : 'none'}"
            data-testid=${`cs-cap-${action}`}
            title=${owned ? 'The browser also uses this key. Any extra modifier gives it back.' : ''}
            >${chord ? keycap(chord) : '(not set)'}</span
          >
          <span>
            <button
              class="q"
              type="button"
              data-testid=${`cs-key-${action}`}
              aria-pressed=${this.capturing === action ? 'true' : 'false'}
              @click=${() => (this.capturing === action ? this.stopCapture() : this.startCapture(action))}
            >
              ${this.capturing === action ? 'Press a key' : 'Set'}
            </button>
            ${chord
              ? html`<button
                  class="q"
                  type="button"
                  data-testid=${`cs-clear-${action}`}
                  aria-label=${`Unbind ${HOTKEY_LABELS[action] ?? action}`}
                  @click=${() => this.clearKey(action)}
                >
                  Unbind
                </button>`
              : nothing}
          </span>
        </div>`;
      })}
      ${this.keyRefusal
        ? html`<p class="err" role="alert" data-testid="cs-key-refusal">${this.keyRefusal}</p>`
        : nothing}
      ${/* role="status", never role="alert": the key is bound and nothing failed. */ ''}
      ${this.keyNote
        ? html`<p class="hint" role="status" data-testid="cs-key-shared">${this.keyNote}</p>`
        : nothing}
      <div class="foot">
        <button class="go" data-testid="cs-keys-save" ?disabled=${this.busy} @click=${() => void this.saveKeys()}>
          Save keys
        </button>
        <button
          class="q"
          type="button"
          data-testid="cs-keys-reset"
          ?disabled=${this.busy}
          @click=${() => void this.resetKeys()}
        >
          Reset to platform
        </button>
      </div>
    </div>`;
  }

  private renderTiles() {
    const rows = this.tiles.map((sku) => ({ sku, product: this.productBySku(sku) }));
    return html`<div class="box" data-testid="cs-tiles">
      <h3>
        Hot list · ${this.profile}
        <button
          class="info"
          type="button"
          aria-label="About the hot list"
          title="The order here is the board's order: the first tile's category leads. Leave it empty and the board fills itself from the yard's own bulk lines."
        >
          ${drawIcon(iconForId('pencil:info'), 19)}
        </button>
      </h3>
      ${rows.length === 0
        ? html`<div class="empty" role="status" data-testid="cs-tiles-empty">
            <b>No tiles curated</b>
            <span>The counter is filling the board from the yard's own bulk lines: add a tile to take it over.</span>
          </div>`
        : rows.map(
            ({ sku, product }, i) => html`<div class="trow">
              <hos-product-thumb
                .productId=${product?.id ?? ''}
                .category=${product?.application ?? ''}
                alt=""
                .size=${34}
                data-stamp=${this.photoStamp[product?.id ?? ''] ?? 0}
              ></hos-product-thumb>
              <span class="nm">
                <b>${product?.name ?? sku.toUpperCase()}</b>
                <span>${product ? `${product.sku} · ${product.application || 'Unfiled'}` : 'Not in this catalogue'}</span>
              </span>
              <button
                class="q"
                type="button"
                data-testid=${`cs-tile-${sku}`}
                aria-label=${`Move ${product?.name ?? sku} up`}
                ?disabled=${i === 0}
                @click=${() => this.moveTile(sku, -1)}
              >
                ${drawIcon(iconForId('lucide:chevron-up'), 12)}
              </button>
              <button
                class="q"
                type="button"
                aria-label=${`Move ${product?.name ?? sku} down`}
                ?disabled=${i === rows.length - 1}
                @click=${() => this.moveTile(sku, 1)}
              >
                ${drawIcon(iconForId('pencil:bottom'), 12)}
              </button>
              ${product
                ? html`<button
                      class="q"
                      type="button"
                      data-testid=${`cs-photo-${sku}`}
                      aria-label=${`Set the photo for ${product.name}`}
                      @click=${() =>
                        this.renderRoot.querySelector<HTMLInputElement>(`#cs-file-${cssId(sku)}`)?.click()}
                    >
                      Photo
                    </button>
                    <input
                      id=${`cs-file-${cssId(sku)}`}
                      type="file"
                      accept="image/png,image/jpeg,image/webp"
                      @change=${(e: Event) => {
                        const f = (e.target as HTMLInputElement).files?.[0];
                        if (f) void this.setPhoto(product, f);
                        (e.target as HTMLInputElement).value = '';
                      }}
                    />
                    <button
                      class="q"
                      type="button"
                      data-testid=${`cs-photo-clear-${sku}`}
                      aria-label=${`Clear the photo on ${product.name}`}
                      @click=${() => void this.clearPhoto(product)}
                    >
                      Clear photo
                    </button>`
                : html`<span></span><span></span>`}
              <button
                class="q"
                type="button"
                aria-label=${`Remove ${product?.name ?? sku} from the hot list`}
                @click=${() => this.removeTile(sku)}
              >
                Remove
              </button>
            </div>`,
          )}
      <div class="pickrow">
        <span style="flex:1; min-width:0">
          <label class="f" for="cs-add">Add a tile</label>
          <select id="cs-add" data-testid="cs-tile-add" style="width:100%">
            <option value="">(not set)</option>
            ${this.pickerGroups().map(
              (g) => html`<optgroup label=${g.label}>
                ${g.items.map((p) => html`<option value=${p.sku.toLowerCase()}>${p.name} · ${p.sku}</option>`)}
              </optgroup>`,
            )}
          </select>
        </span>
        <button
          class="q"
          type="button"
          data-testid="cs-tile-add-go"
          @click=${() => {
            const sel = this.renderRoot.querySelector<HTMLSelectElement>('#cs-add');
            if (sel?.value) {
              this.addTile(sel.value);
              sel.value = '';
            }
          }}
        >
          Add
        </button>
      </div>
      <div class="foot">
        <button class="go" data-testid="cs-tiles-save" ?disabled=${this.busy} @click=${() => void this.saveTiles()}>
          Save hot list
        </button>
      </div>
    </div>`;
  }

  /** The picker, grouped by the same facet the board's first level groups by. */
  private pickerGroups(): { label: string; items: PosProduct[] }[] {
    const chosen = new Set(this.tiles);
    const groups = new Map<string, PosProduct[]>();
    for (const p of this.catalogue) {
      if (chosen.has(p.sku.toLowerCase())) continue;
      const k = p.application?.trim() || 'Other';
      const bucket = groups.get(k);
      if (bucket) bucket.push(p);
      else groups.set(k, [p]);
    }
    return [...groups.entries()]
      .sort((a, b) => Number(a[0] === 'Other') - Number(b[0] === 'Other') || a[0].localeCompare(b[0]))
      .map(([label, items]) => ({ label, items: items.sort((a, b) => a.name.localeCompare(b.name)) }));
  }

  private renderPanels() {
    return html`<div class="box" data-testid="cs-panels">
      <h3>
        Starts with · ${this.profile}
        <button
          class="info"
          type="button"
          aria-label="About the starting view"
          title="What this job shape sees before anyone changes it. Each person can still switch either one under Counter view."
        >
          ${drawIcon(iconForId('pencil:info'), 19)}
        </button>
      </h3>
      ${COUNTER_PANELS.map((id) => {
        const on = this.panels[id] !== false;
        return html`<div class="krow">
          <span>${PANEL_LABELS[id]}</span>
          <span></span>
          <button
            class="q"
            type="button"
            data-testid=${`cs-panel-${id}`}
            aria-pressed=${on ? 'true' : 'false'}
            @click=${() => (this.panels = { ...this.panels, [id]: !on })}
          >
            ${on ? 'On' : 'Off'}
          </button>
        </div>`;
      })}
      <div class="foot">
        <button class="go" data-testid="cs-panels-save" ?disabled=${this.busy} @click=${() => void this.savePanels()}>
          Save defaults
        </button>
      </div>
    </div>`;
  }

  /**
   * The counter's one money rule this screen edits: what a cancelled special
   * order's supplier deposit takes. The select carries the words the history
   * renders (one idea, one spelling), and the control only goes live once the
   * policy row has answered: a rule whose current value is unknown is shown
   * disabled rather than offered at a guessed default, the never-render-a-
   * confident-wrong-number rule. A manager may still keep a given order's
   * deposit at the cancel itself, with a reason; this is only the default.
   */
  private renderMoneyRule() {
    const loaded = this.policyRow !== null;
    return html`<div class="box" data-testid="cs-money-rule">
      <h3>
        Counter money rules
        <button
          class="info"
          type="button"
          aria-label="About the deposit rule"
          title="When an order whose only money is a supplier deposit is cancelled, the deposit takes this disposition. A manager can still keep it on a given order at the cancel, with a reason."
        >
          ${drawIcon(iconForId('pencil:info'), 19)}
        </button>
      </h3>
      ${this.policyFault
        ? html`<p class="err" role="alert" data-testid="cs-money-rule-fault">
            Could not read the deposit rule. ${this.policyFault}
            <button class="q" type="button" @click=${() => void this.loadPolicy()}>Retry</button>
          </p>`
        : nothing}
      <div class="krow">
        <label for="cs-deposit-cancel">${POS_HISTORY_WORDS.rule['supplier_deposit_cancel']}</label>
        <span></span>
        <select
          id="cs-deposit-cancel"
          data-testid="cs-deposit-cancel"
          ?disabled=${!loaded || this.busy}
          @change=${(e: Event) => {
            this.depositCancelDraft = (e.target as HTMLSelectElement).value;
          }}
        >
          ${Object.entries(SUPPLIER_DEPOSIT_CANCEL_WORDS).map(
            ([value, word]) =>
              html`<option value=${value} ?selected=${this.depositCancelDraft === value}>${word}</option>`,
          )}
        </select>
      </div>
      <div class="foot">
        <button
          class="go"
          data-testid="cs-deposit-cancel-save"
          ?disabled=${!loaded || this.busy}
          @click=${() => void this.saveDepositCancel()}
        >
          Save deposit rule
        </button>
      </div>
    </div>`;
  }
}

/**
 * The dealer's stored roster as action -> chord, else the level beneath them.
 *
 * D-111 added the second argument. No dealer rows used to mean the shipped BisTrack constant,
 * because that constant WAS the level beneath. It no longer is: the platform roster (0191) sits in
 * between, and a screen that showed the constant would show an administrator keys their counter is
 * not using. The constant stays as the LAST resort, for a build whose platform read did not land:
 * a counter with the wrong keys is bad; a settings screen showing none is worse.
 */
function rosterFrom(
  items: readonly SurfaceItem[],
  platform: Record<string, string>,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const it of items) {
    if (it.kind !== ACTION_KIND || !it.visible) continue;
    const i = it.id.lastIndexOf('.');
    if (i <= 0 || i === it.id.length - 1) continue;
    const action = it.id.slice(0, i);
    if (HOTKEY_LABELS[action] === undefined) continue;
    out[action] = it.id.slice(i + 1);
  }
  if (Object.keys(out).length > 0) return out;
  if (Object.keys(platform).length > 0) return platform;
  return Object.fromEntries(BISTRACK_ROSTER.map((r) => [r.action, r.key]));
}

/** The function keys mainstream browsers claim: mirrors browserOwnedKeys in hotkeys.go. */
const BROWSER_KEYS = new Set(['f1', 'f3', 'f5', 'f6', 'f7', 'f10', 'f11', 'f12']);

function isBrowserKey(chord: string): boolean {
  const parts = (chord || '').split('-');
  return BROWSER_KEYS.has(parts[parts.length - 1]);
}

/** A SKU is safe in an id attribute once its dots and slashes are gone. */
function cssId(sku: string): string {
  return sku.replace(/[^a-z0-9-]/gi, '_');
}

/** The server's own sentence, when it sent one. */
async function readErr(res: Response): Promise<string> {
  try {
    return ((await res.json()) as { error?: string }).error ?? '';
  } catch {
    return '';
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-counter-settings': HosPosCounterSettings;
  }
}

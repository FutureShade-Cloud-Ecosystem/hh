// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-panels.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { drawIcon, iconForId } from '@hardscapehouse/pos-kit/src/ported/_icon-consumer.js';
import type { FlowStep, FlowStepId } from '@hardscapehouse/pos-kit/src/ported/_pos-flow.js';
import {
  PANEL_ORDER,
  PANEL_STEP,
  PANEL_STEP_COARSE,
  bringToFront,
  clampBox,
  clearPanels,
  defaultLayout,
  moveBox,
  normalizeLayout,
  normalizeOrder,
  readPanels,
  resizeBox,
  writePanels,
  type PanelArea,
  type PanelBox,
  type PanelEdges,
  type PanelLayout,
} from './_pos-panels.js';

/**
 * hos-pos-panels: the counter's four steps, all open at once, each one sized and placed.
 *
 * THE OTHER COUNTER, NOT A REPLACEMENT FOR IT. The four-step single column remains the standard
 * counter and the shipped default; this is what a person gets when they ask for it in the view
 * menu, and the commander's ruling is what put it here. Several testers asked for the same
 * thing in different words: "can we just have everything opened and on the same page? being able
 * to see customer and products and payment and everything all at once is actually extremely
 * useful": and one of them asked for it in exactly this shape: "maybe if users still want this we
 * give the ability to turn it off?"
 *
 * WHAT IT OWNS: where each panel sits, the chrome around it, the pointer and keyboard gestures that
 * move and size it, and the way back (Reset layout). WHAT IT DOES NOT OWN: the step bodies. Those
 * arrive as slotted light DOM from the ONE switch in the terminal that the accordion also renders
 * through, so a step can never be rendered by two different paths: the accordion's own rule, kept.
 *
 * WHY THE BODIES ARE ALL RENDERED HERE and the accordion's are not: the accordion's whole argument
 * is that a closed card must not hold a live product grid open or strand controls in the tab order.
 * Nothing is closed here. Every panel is open, every body is live, and that IS the feature: the
 * cost is four live panels' worth of work, which is the cost the person asked to pay.
 *
 * ONE GOLD PER PANEL, and none of it is ours. The panel chrome is quiet on purpose: a header, a
 * number, a completion tick and two grips. The gold belongs to the work inside the panel (Complete
 * sale lives in Payment, and it is still the loudest thing on this screen).
 */
@customElement('hos-pos-panels')
export class HosPosPanels extends LitElement {
 /** The four steps, already derived by the terminal (deriveFlow): number, label, complete. */
  @property({ attribute: false }) steps: FlowStep[] = [];

  /** Which panel the operator is working in. Drives the focus ring and the hotkey target. */
  @property() active: FlowStepId | '' = '';

  @state() private boxes: PanelLayout | null = null;
  @state() private stack: FlowStepId[] = [...PANEL_ORDER];
  @state() private area: PanelArea = { w: 0, h: 0 };

  /** The gesture in flight. Null between gestures; never persisted. */
  private drag: {
    id: FlowStepId;
    pointerId: number;
    startX: number;
    startY: number;
    from: PanelBox;
    edges: PanelEdges | null;
  } | null = null;

  private ro: ResizeObserver | null = null;

  static styles = [
    focusRing,
    css`
    /* A column so the grid takes what the chrome above and below it does
       not: the mounting column (the terminal's panels view) hands this host
       a definite height, the reset bar keeps its natural row, and the
       surface flexes into the rest. The host carries min-height: 0 so a
       flex parent may shrink it below its content (the surface's own
       overflow takes the overflow from there). */
    :host { display: flex; flex-direction: column; min-height: 0; }
    /* The panel surface scrolls rather than squeezing: a person who drags a panel past the edge of
       a big layout meant to put it there, and a surface that silently re-tiled under them would be
       taking the arrangement back.
       The height is the flex chain's, not a viewport arithmetic: the old
       calc(100vh - 230px) guessed three chrome heights at once and was
       wrong at every width it did not guess (the fill work's finding). The
       measured area drives the tiling through the ResizeObserver below, so
       the default 2x2 tracks whatever the chain hands down. */
    .surface {
      position: relative;
      flex: 1 1 auto;
      min-height: 0;
      overflow: auto;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      background: var(--hos-deep-earth);
    }
    .bar {
      display: flex;
      align-items: center;
      justify-content: flex-end;
      margin-bottom: 6px;
    }
    .reset {
      background: transparent;
      color: var(--hos-text-dim);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 5px 11px;
      font: inherit;
      font-size: 12px;
      cursor: pointer;
    }
    .reset:hover { border-color: var(--hos-stone-amber); color: var(--hos-stone-amber-text); }
    .panel {
      position: absolute;
      display: flex;
      flex-direction: column;
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      /* Clipped so a panel's own content cannot paint over its neighbour; the BODY scrolls. */
      overflow: hidden;
    }
    .panel.active { border-color: var(--hos-stone-amber); }
    .head {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 0 8px 0 10px;
      height: 34px;
      flex: 0 0 auto;
      border-bottom: 1px solid var(--hos-line);
      cursor: move;
      touch-action: none;
      user-select: none;
    }
    .n {
      flex: 0 0 auto;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 18px;
      height: 18px;
      border-radius: 50%;
      border: 1px solid var(--hos-line);
      color: var(--hos-text-dim);
      font-size: 11px;
      text-align: center;
    }
    .panel.done .n { border-color: var(--hos-success); color: var(--hos-success); }
    .title {
      flex: 1 1 auto;
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .panel.active .title { color: var(--hos-stone-amber-text); }
    .grip {
      flex: 0 0 auto;
      width: 22px;
      height: 22px;
      padding: 0;
      background: transparent;
      border: 0;
      border-radius: var(--hos-radius-control);
      color: var(--hos-text-dim);
      font: inherit;
      font-size: 12px;
      line-height: 1;
      cursor: pointer;
    }
    .grip:hover { color: var(--hos-stone-amber-text); }
    .body {
      flex: 1 1 auto;
      overflow: auto;
      padding: 8px 10px;
      /* The zone's own floor: a panel the person dragged short still shows
         its head and a sliver of the work, and the zone takes the rest. */
      min-height: 0;
    }
    /* The pinned foot: rows that must never scroll away with the body (the
       Items panel's fulfilment area). No padding of its own: an empty foot
       slot renders nothing and costs no height, and the slotted wrapper
       (styled by the terminal, which owns its content) brings the spacing. */
    .foot {
      flex: 0 0 auto;
    }
    /* The edges are AT the border, not outside it: a handle at -3px sits outside a clipped box and
       cannot be hit at all: the floating assistant shipped exactly that bug once. */
    .e { position: absolute; touch-action: none; }
    .e-r { top: 0; right: 0; width: 6px; height: 100%; cursor: ew-resize; }
    .e-b { left: 0; bottom: 0; height: 6px; width: 100%; cursor: ns-resize; }
    .e-br { right: 0; bottom: 0; width: 14px; height: 14px; cursor: nwse-resize; }
    @media (prefers-reduced-motion: no-preference) {
      .panel { transition: border-color 120ms ease; }
    }
  `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
    const stored = readPanels();
    this.stack = normalizeOrder(stored?.order);
    // The boxes wait for a real area: normalising against {0,0} would clamp every panel to one
    // corner and then STORE that on the first gesture. First measure, then place.
    this.pendingBoxes = stored?.boxes ?? null;
  }

  private pendingBoxes: Partial<PanelLayout> | null = null;

  firstUpdated(): void {
    const host = this.renderRoot.querySelector<HTMLElement>('.surface');
    if (!host) return;
    const measure = () => {
      const r = host.getBoundingClientRect();
      const area = { w: Math.round(r.width), h: Math.round(r.height) };
      if (area.w <= 0 || area.h <= 0) return;
      const first = !this.boxes;
      this.area = area;
      // A resize RE-CLAMPS but never re-tiles: the arrangement is the person's, and a window
      // dragged narrower should bring panels back into reach, not throw the layout away.
      this.boxes = first ? normalizeLayout(this.pendingBoxes, area) : normalizeLayout(this.boxes, area);
      if (first) this.pendingBoxes = null;
    };
    measure();
    this.ro = new ResizeObserver(measure);
    this.ro.observe(host);
  }

  disconnectedCallback(): void {
    this.ro?.disconnect();
    this.ro = null;
    super.disconnectedCallback();
  }

  private save(): void {
    if (this.boxes) writePanels({ boxes: this.boxes, order: this.stack });
  }

  private focusPanel(id: FlowStepId): void {
    if (this.stack[this.stack.length - 1] !== id) this.stack = bringToFront(this.stack, id);
    this.dispatchEvent(new CustomEvent('pos-panel-focus', { detail: { step: id }, bubbles: true, composed: true }));
  }

  // ── pointer ──────────────────────────────────────────────────────────────────────────────────
  private onDown(e: PointerEvent, id: FlowStepId, edges: PanelEdges | null): void {
    if (e.button !== 0 || !this.boxes) return;
    const target = e.currentTarget as HTMLElement;
    target.setPointerCapture(e.pointerId);
    this.focusPanel(id);
    this.drag = { id, pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, from: { ...this.boxes[id] }, edges };
    e.preventDefault();
  }

  private onMove = (e: PointerEvent): void => {
    const d = this.drag;
    if (!d || !this.boxes || e.pointerId !== d.pointerId) return;
    const dx = e.clientX - d.startX;
    const dy = e.clientY - d.startY;
    const next = d.edges ? resizeBox(d.from, dx, dy, d.edges, this.area) : moveBox(d.from, dx, dy, this.area);
    this.boxes = { ...this.boxes, [d.id]: next };
  };

  private onUp = (e: PointerEvent): void => {
    if (!this.drag || e.pointerId !== this.drag.pointerId) return;
    this.drag = null;
    this.save();
  };

  /**
   * The keyboard path, which is not optional: a counter runs on muscle memory and a panel that can
   * only be placed with a pointer is a panel a keyboard user cannot place at all. Arrows move the
   * focused panel, Alt+Arrows size it, Shift takes the coarse step.
   */
  private onHeadKey(e: KeyboardEvent, id: FlowStepId): void {
    if (!this.boxes) return;
    const dir: Record<string, [number, number]> = {
      ArrowLeft: [-1, 0],
      ArrowRight: [1, 0],
      ArrowUp: [0, -1],
      ArrowDown: [0, 1],
    };
    const d = dir[e.key];
    if (!d) return;
    e.preventDefault();
    const step = e.shiftKey ? PANEL_STEP_COARSE : PANEL_STEP;
    const from = this.boxes[id];
    const next = e.altKey
      ? resizeBox(from, d[0] * step, d[1] * step, { right: true, bottom: true }, this.area)
      : moveBox(from, d[0] * step, d[1] * step, this.area);
    this.boxes = { ...this.boxes, [id]: next };
    this.focusPanel(id);
    this.save();
  }

  private reset(): void {
    clearPanels();
    this.stack = [...PANEL_ORDER];
    this.boxes = defaultLayout(this.area);
    this.save();
  }

  render() {
    return html`
      ${/* NO HELPER LINE. This bar carried one: "Drag a panel by its header; drag an edge to size
           it": and it was a helper paragraph at the top of a screen, which the doctrine bans
           outright and which the shell spent a whole train removing from every other pane. The
           affordances say it better anyway: the header takes a move cursor, the edges take resize
           cursors, and the header's own accessible name carries the keyboard path for the people a
           sentence in 12px grey was never going to reach. */ ''}
      <div class="bar">
        <button class="reset" type="button" data-testid="pos-panels-reset" @click=${this.reset}>Reset layout</button>
      </div>
      <div
        class="surface"
        data-testid="pos-panels"
        @pointermove=${this.onMove}
        @pointerup=${this.onUp}
        @pointercancel=${this.onUp}
      >
        ${this.boxes ? this.steps.map((s) => this.renderPanel(s)) : nothing}
      </div>
    `;
  }

  private renderPanel(s: FlowStep) {
    const boxes = this.boxes;
    if (!boxes) return nothing;
    const b = clampBox(boxes[s.id], this.area);
    const z = this.stack.indexOf(s.id) + 1;
    const cls = `panel${this.active === s.id ? ' active' : ''}${s.complete ? ' done' : ''}`;
    return html`<section
      class="${cls}"
      data-panel=${s.id}
      aria-label=${s.label}
      style=${'left:' + b.x + 'px;top:' + b.y + 'px;width:' + b.w + 'px;height:' + b.h + 'px;z-index:' + z}
      @pointerdown=${() => this.focusPanel(s.id)}
    >
      ${/* The header is the handle, so it is a real focusable control with its own accessible name
          : a div with a pointer handler would be a drag nobody can start from the keyboard. */ ''}
      <div
        class="head"
        tabindex="0"
        role="button"
        aria-label=${`${s.label} panel: arrows move, Alt and arrows resize`}
        data-head=${s.id}
        @pointerdown=${(e: PointerEvent) => this.onDown(e, s.id, null)}
        @keydown=${(e: KeyboardEvent) => this.onHeadKey(e, s.id)}
      >
        <span class="n" aria-hidden="true">${s.complete ? drawIcon(iconForId('pencil:tick-m'), 12) : s.n}</span>
        <span class="title">${s.label}</span>
      </div>
      <div class="body"><slot name=${s.id}></slot></div>
      <div class="foot"><slot name=${s.id + '-foot'}></slot></div>
      <div class="e e-r" @pointerdown=${(e: PointerEvent) => this.onDown(e, s.id, { right: true })}></div>
      <div class="e e-b" @pointerdown=${(e: PointerEvent) => this.onDown(e, s.id, { bottom: true })}></div>
      <div class="e e-br" @pointerdown=${(e: PointerEvent) => this.onDown(e, s.id, { right: true, bottom: true })}></div>
    </section>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-panels': HosPosPanels;
  }
}

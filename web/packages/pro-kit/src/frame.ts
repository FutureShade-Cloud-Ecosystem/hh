// hh-pro-frame: the Pro frame. One header band with the app and surface
// selector, the dealer's name on the chrome, and the person's role and sign out
// on the right; a default slot for the surface; a "banner" slot under the band
// for offline and sync notices; an "actions" slot in the band for a surface's
// own controls. Visual references: HH: Pro, shell-frame-phone-light-r2 and
// shell-selector-phone-light (phone first, then tablet and desktop).
//
// THIS IS A THIN FRAME ELEMENT THE SHELLS CYCLE CAN REPLACE. The real shell
// (one header band, the collapsed rail of pinned surfaces, row 1520) belongs to
// that cycle; this element keeps the one contract an app depends on, so
// swapping it touches nothing else: it takes its inputs as data- attributes
// (data-app, data-surface, data-dealer, data-role, data-capabilities), shows a
// selector of the surfaces those capabilities open across the three apps, and
// speaks in two events only: "pro-surface-select" ({app, surface}) and
// "pro-sign-out". It owns no session, no routing and no data.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { appOf, openSurfaces, surfaceOf, type ProAppKey } from "./surfaces.js";

export interface SurfaceSelect {
  app: string;
  surface: string;
}

export class HHProFrame extends HHElement {
  static properties = {
    ...HHElement.properties,
    app: { attribute: "data-app" },
    surface: { attribute: "data-surface" },
    dealer: { attribute: "data-dealer" },
    roleLabel: { attribute: "data-role" },
    capabilities: { attribute: "data-capabilities" },
    open: { state: true },
  };

  static styles = [
    baseStyles,
    css`
      :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
      .strip {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--hh-s-8);
        padding: var(--hh-s-4) var(--hh-page-gutter, var(--hh-s-16));
        background: var(--hh-ground);
        border-bottom: 1px solid var(--hh-line);
        font-size: 0.75rem;
        color: var(--hh-text-dim);
      }
      .dealer { font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; color: var(--hh-text); }
      .band {
        position: relative;
        display: flex;
        align-items: center;
        gap: var(--hh-s-12);
        padding: var(--hh-s-8) var(--hh-page-gutter, var(--hh-s-16));
        border-bottom: 1px solid var(--hh-line);
        background: var(--hh-card);
      }
      .mark {
        flex: none;
        display: grid;
        place-items: center;
        width: 34px;
        height: 34px;
        border-radius: var(--hh-radius-control);
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
        font-weight: 800;
      }
      .selector {
        display: inline-flex;
        align-items: center;
        gap: var(--hh-s-8);
        min-height: 44px;
        padding: var(--hh-s-4) var(--hh-s-12);
        border: 1px solid var(--hh-control-line);
        border-radius: var(--hh-radius-control);
        background: var(--hh-field);
        color: var(--hh-text);
        font: inherit;
        font-weight: 600;
        cursor: pointer;
      }
      .selector .app { display: none; font-weight: 400; color: var(--hh-text-dim); white-space: nowrap; }
      .selector span { white-space: nowrap; }
      .grow { flex: 1; }
      .who { display: none; color: var(--hh-text-dim); font-size: 0.875rem; }
      .out {
        min-height: 44px;
        padding: var(--hh-s-4) var(--hh-s-12);
        border: 1px solid var(--hh-control-line);
        border-radius: var(--hh-radius-control);
        background: transparent;
        color: var(--hh-text);
        font: inherit;
        cursor: pointer;
      }
      .panel {
        position: absolute;
        z-index: var(--hh-z-dropdown, 10);
        top: 100%;
        left: var(--hh-s-8);
        right: var(--hh-s-8);
        max-width: 26rem;
        padding: var(--hh-s-8);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        background: var(--hh-card);
        box-shadow: var(--hh-widget-shadow);
      }
      .group { margin: 0; padding: var(--hh-s-8) var(--hh-s-8) var(--hh-s-4); font-size: 0.6875rem; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; color: var(--hh-text-dim); }
      .item {
        display: flex;
        width: 100%;
        align-items: center;
        min-height: 44px;
        padding: var(--hh-s-8) var(--hh-s-12);
        border: 0;
        border-radius: var(--hh-radius-control);
        background: transparent;
        color: var(--hh-text);
        font: inherit;
        text-align: left;
        cursor: pointer;
      }
      .item[aria-current="page"] { background: var(--hh-widget); font-weight: 700; border-inline-start: 3px solid var(--hh-focus-ring); }
      main { display: block; padding: var(--hh-s-16) var(--hh-page-gutter, var(--hh-s-16)); max-width: var(--hh-content-max, 72rem); margin: 0 auto; }
      .out { white-space: nowrap; }
      @media (min-width: 30rem) { .selector .app { display: inline; } }
      @media (min-width: 40rem) { .who { display: inline; } }
    `,
  ];

  declare app: string;
  declare surface: string;
  declare dealer: string;
  declare roleLabel: string;
  declare capabilities: string;
  declare open: boolean;

  constructor() {
    super();
    this.app = "";
    this.surface = "";
    this.dealer = "";
    this.roleLabel = "";
    this.capabilities = "";
    this.open = false;
  }

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    const action = control?.getAttribute("data-action");
    if (action === "toggle-selector") {
      this.open = !this.open;
    } else if (action === "select-surface" && control !== undefined) {
      this.open = false;
      const detail: SurfaceSelect = { app: control.getAttribute("data-app") ?? "", surface: control.getAttribute("data-surface") ?? "" };
      this.dispatchEvent(new CustomEvent<SurfaceSelect>("pro-surface-select", { detail, bubbles: true, composed: true }));
    } else if (action === "sign-out") {
      this.open = false;
      this.dispatchEvent(new CustomEvent("pro-sign-out", { bubbles: true, composed: true }));
    }
  };

  readonly #onKey = (event: KeyboardEvent): void => {
    if (event.key === "Escape" && this.open) this.open = false;
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onClick);
    this.addEventListener("keydown", this.#onKey);
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.removeEventListener("keydown", this.#onKey);
    super.disconnectedCallback();
  }

  #panel(): TemplateResult | typeof nothing {
    if (!this.open) return nothing;
    const held = new Set(this.capabilities.split(" ").filter((c) => c !== ""));
    const groups = openSurfaces(held);
    return html`<nav class="panel" aria-label="Apps and surfaces">
      ${groups.map(
        ({ app, surfaces }) => html`<p class="group">${app.label}</p>
          ${surfaces.map(
            (s) => html`<button type="button" class="item" data-action="select-surface" data-app="${app.key}" data-surface="${s.id}" aria-current="${app.key === this.app && s.id === this.surface ? "page" : "false"}">${s.label}</button>`,
          )}`,
      )}
    </nav>`;
  }

  render(): TemplateResult {
    const app = appOf(this.app);
    const surface = app === undefined ? undefined : surfaceOf(app.key as ProAppKey, this.surface);
    const dealer = this.dealer === "" ? "Pro" : this.dealer;
    return html`<div class="strip">
        <span class="dealer" data-testid="frame-dealer">${dealer}</span>
        <span>${app === undefined ? "" : app.label}</span>
      </div>
      <header class="band">
        <span class="mark" aria-hidden="true">H</span>
        <button type="button" class="selector" data-action="toggle-selector" data-testid="frame-selector" aria-expanded="${this.open ? "true" : "false"}" aria-label="Choose an app and surface">
          <span>${surface === undefined ? "Choose" : surface.label}</span>
          <span class="app">${app === undefined ? "" : app.label}</span>
        </button>
        <span class="grow"></span>
        <slot name="actions"></slot>
        <span class="who" data-testid="frame-role">${this.roleLabel}</span>
        <button type="button" class="out" data-action="sign-out" data-testid="frame-sign-out">Sign out</button>
        ${this.#panel()}
      </header>
      <slot name="banner"></slot>
      <main><slot></slot></main>`;
  }
}

defineElement("hh-pro-frame", HHProFrame);

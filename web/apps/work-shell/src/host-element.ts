// The Work shell host: the one page the HH Work profile opens (plan B.4, the
// shell UX of decision log row 1520).
//
//   door   no session: the front door, mounted as a page under the host at
//          /door/, is the sign in and the role picker (its screen is not
//          rewritten here). It mints a demo session into the tab's session
//          storage; the host picks it up from there.
//   desk   a session: ONE header band (the app and surface selector built from
//          the profile by the signed in role, the pin control, the org, the
//          sync chip, notifications, the account chip with Switch role), the
//          offline banner, the collapsed rail of the person's own pins, and
//          the selected desk app in the content area.
//
// WHY AN IFRAME PER APP, not the app's own route in place. Each desk app is a
// complete Vite bundle built at its own base (/till/, /quote/, /home/ ...)
// with its own index.html, and every bundle registers the same custom element
// names from the design system: two of them in one document would collide on
// customElements.define. A frame keeps each bundle whole, at the path the
// assembly script and the demo box already serve it from, and the session
// handoff the apps already speak (the front door's key in this tab's session
// storage, read once and dropped) works between same-origin frames without any
// app change. The cost, named: the hosted app keeps its own header inside the
// frame until it grows a framed mode (a fidelity pass item).
//
// The host holds the session in memory only. Each desk app drops the handoff
// key when it adopts it, so the host writes the key again right before it
// loads each app. Switch role clears the key, drops the session and puts the
// door back (a fresh frame, so the picker comes up clean); pins and every per
// user store are untouched, they belong to the person.
import { css, html, nothing, type PropertyValues, type TemplateResult } from "lit";
import { keyed } from "lit/directives/keyed.js";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import {
  PinBook,
  currentBreadcrumb,
  initialsOf,
  switcherGroups,
  type PinStorage,
  type RailPin,
} from "@hardscapehouse/shell-chrome";
import type { ShellHeader } from "@hardscapehouse/shell-chrome/src/shell-header.js";
import type { ShellRail } from "@hardscapehouse/shell-chrome/src/shell-rail.js";
import type { ShellBanner } from "@hardscapehouse/shell-chrome/src/shell-banner.js";
import "@hardscapehouse/shell-chrome/src/shell-header.js";
import "@hardscapehouse/shell-chrome/src/shell-rail.js";
import "@hardscapehouse/shell-chrome/src/shell-banner.js";
import { bannerModeFor, chipInputFor, pinStorageFrom } from "./chrome-feed.js";
import { createConsoleSession, type ConsoleSession } from "./console-session.js";
import { DEMO_ORG_NAME, DEMO_ORG_SLUG, DOOR_PATH, LAUNCH_FILE, PROFILE_FILE } from "./config.js";
import { acceptSwitchRole } from "./frames.js";
import { clearHandoff, readDoorSession, writeHandoff } from "./handoff.js";
import {
  initialState,
  openApp,
  signedIn,
  switchRole,
  type HostSession,
  type HostState,
} from "./host-state.js";
import { parseProfile, type WorkApp } from "./switcher.js";
import { applyTheme } from "./theme.js";

/** The slice of the Tauri webview this host uses: the shell's invoke, when there is one. */
interface TauriWindow {
  __TAURI_INTERNALS__?: { invoke?: (command: string, args?: Record<string, unknown>) => Promise<unknown> };
}

/** How often the door phase looks for the session the front door mints (a storage event is the quick path). */
const DOOR_POLL_MS = 250;

const NO_SURFACES = new Map<string, readonly { id: string; label: string }[]>();

export class WorkShellHost extends HHElement {
  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
        height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
        font-family: var(--hh-font-ui);
      }
      .shell {
        display: flex;
        flex-direction: column;
        height: 100%;
        min-height: 0;
      }
      .top {
        position: relative;
        flex: none;
      }
      .body {
        display: flex;
        flex: 1 1 auto;
        min-height: 0;
      }
      hh-shell-rail {
        flex: none;
        border-right: 1px solid var(--hh-line);
      }
      main {
        position: relative;
        flex: 1 1 auto;
        min-width: 0;
        min-height: 0;
      }
      iframe {
        display: block;
        width: 100%;
        height: 100%;
        border: 0;
        background: var(--hh-page);
      }
      .menu {
        position: absolute;
        top: 100%;
        right: var(--hh-s-12);
        z-index: 30;
        display: grid;
        gap: var(--hh-s-12);
        min-width: 220px;
        padding: var(--hh-s-16);
        background: var(--hh-card);
        color: var(--hh-text);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        box-shadow: var(--hh-elev-2);
      }
      .menu p {
        margin: 0;
        font-size: 13px;
        color: var(--hh-text-dim);
      }
      .menu strong {
        color: var(--hh-text);
        font-size: 15px;
      }
      .menu button {
        min-height: 44px;
        justify-self: stretch;
      }
      .notice {
        margin: 0;
        padding: var(--hh-s-4) var(--hh-s-16);
        font-size: 13px;
        color: var(--hh-text-dim);
        border-bottom: 1px solid var(--hh-line);
      }
      .state {
        display: grid;
        justify-items: start;
        gap: var(--hh-s-12);
        max-width: 520px;
        margin: 0 auto;
        padding: var(--hh-s-48) var(--hh-s-24);
      }
      .state h2 {
        margin: 0;
        font-size: clamp(22px, 2.6vw, 28px);
        font-weight: 600;
        letter-spacing: -0.02em;
      }
      .state p {
        margin: 0;
        color: var(--hh-text-dim);
        line-height: 1.5;
      }
    `,
  ];

  /** Per user pins, on the platform's persistent storage when it works and memory when it does not. */
  readonly #pins: PinBook;
  readonly #pinStorage: PinStorage;
  #state: HostState = initialState();
  #apps: readonly WorkApp[] | null = null;
  #fault = "";
  #online = typeof navigator === "undefined" ? true : navigator.onLine;
  #menuOpen = false;
  #notice = "";
  #consoleSession: ConsoleSession | null = null;
  #doorPoll: ReturnType<typeof setInterval> | null = null;

  constructor() {
    super();
    this.#pinStorage = pinStorageFrom(safeLocalStorage());
    this.#pins = new PinBook({ storage: this.#pinStorage });
  }

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onClick);
    this.addEventListener("hh-shell-surface-open", this.#onSurfaceOpen as EventListener);
    this.addEventListener("hh-shell-rail-select", this.#onRailSelect as EventListener);
    this.addEventListener("hh-shell-pin", this.#onPin as EventListener);
    this.addEventListener("hh-shell-account", this.#onAccount);
    this.addEventListener("keydown", this.#onKeyDown);
    window.addEventListener("message", this.#onMessage);
    window.addEventListener("online", this.#onConnection);
    window.addEventListener("offline", this.#onConnection);
    window.addEventListener("storage", this.#onStorage);
    document.addEventListener("pointerdown", this.#onPointerDown, true);
    this.#applyTheme();
    void this.#boot();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.removeEventListener("hh-shell-surface-open", this.#onSurfaceOpen as EventListener);
    this.removeEventListener("hh-shell-rail-select", this.#onRailSelect as EventListener);
    this.removeEventListener("hh-shell-pin", this.#onPin as EventListener);
    this.removeEventListener("hh-shell-account", this.#onAccount);
    this.removeEventListener("keydown", this.#onKeyDown);
    window.removeEventListener("message", this.#onMessage);
    window.removeEventListener("online", this.#onConnection);
    window.removeEventListener("offline", this.#onConnection);
    window.removeEventListener("storage", this.#onStorage);
    document.removeEventListener("pointerdown", this.#onPointerDown, true);
    this.#stopDoorPoll();
    super.disconnectedCallback();
  }

  // ── boot ──────────────────────────────────────────────────────────────

  async #boot(): Promise<void> {
    try {
      const [profile, launch] = await Promise.all([fetchJson(PROFILE_FILE), fetchJson(LAUNCH_FILE)]);
      const read = parseProfile(profile, launch);
      if (read === null) throw new Error("the profile files are not usable");
      this.#apps = read.apps;
    } catch {
      this.#fault = "HH Work could not read its profile, so it has no apps to offer. Close it and open it again.";
      this.requestUpdate();
      return;
    }
    // In the shell the Console session (the keychain's, on the Rust side) is
    // tried first; a shell with no Console configuration answers unavailable
    // and the front door's picker stands.
    const invoke = (window as unknown as TauriWindow).__TAURI_INTERNALS__?.invoke;
    if (typeof invoke === "function") {
      this.#consoleSession = createConsoleSession({ invoke, org: DEMO_ORG_SLUG });
      const resumed = await this.#consoleSession.resume();
      if (resumed !== null) {
        this.#signIn(resumed);
        return;
      }
    }
    // A reload that finds the front door's key still in place restores that role.
    const waiting = readDoorSession();
    if (waiting !== null) {
      this.#signIn(waiting);
      return;
    }
    this.#startDoorPoll();
    this.requestUpdate();
  }

  // ── the door: the session the front door mints ───────────────────────

  #startDoorPoll(): void {
    if (this.#doorPoll !== null) return;
    this.#doorPoll = setInterval(() => this.#checkDoor(), DOOR_POLL_MS);
  }

  #stopDoorPoll(): void {
    if (this.#doorPoll !== null) clearInterval(this.#doorPoll);
    this.#doorPoll = null;
  }

  #checkDoor(): void {
    if (this.#state.phase !== "door" || this.#apps === null) return;
    const minted = readDoorSession();
    if (minted !== null) this.#signIn(minted);
  }

  readonly #onStorage = (): void => {
    this.#applyTheme();
    this.#checkDoor();
  };

  // ── state changes ──────────────────────────────────────────────────────

  #signIn(session: HostSession): void {
    if (this.#apps === null) return;
    this.#stopDoorPoll();
    const next = signedIn(this.#state, session, this.#apps);
    this.#state = next;
    this.#menuOpen = false;
    this.#notice = "";
    this.#handOff();
    this.requestUpdate();
  }

  /** Write the session where the app about to load will read it (each app drops the key it reads). */
  #handOff(): void {
    const state = this.#state;
    if (state.phase !== "desk" || state.currentAppId === null) return;
    const fresh = this.#consoleSession?.currentToken() ?? null;
    writeHandoff(fresh !== null && state.session.source === "console" ? { ...state.session, token: fresh } : state.session);
  }

  #open(appId: string): void {
    const next = openApp(this.#state, appId);
    if (next === this.#state) return;
    this.#state = next;
    this.#menuOpen = false;
    this.#notice = "";
    this.#handOff();
    this.requestUpdate();
  }

  /** Switch role: the session is dropped everywhere, the door is back. Pins and stores stay for their person. */
  #switchRole(): void {
    const wasConsole = this.#state.phase === "desk" && this.#state.session.source === "console";
    this.#state = switchRole(this.#state);
    this.#menuOpen = false;
    this.#notice = "";
    clearHandoff();
    if (wasConsole && this.#consoleSession !== null) void this.#consoleSession.signOut();
    this.#startDoorPoll();
    this.requestUpdate();
  }

  // ── events ─────────────────────────────────────────────────────────────

  readonly #onClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    if (control.getAttribute("data-action") === "switch-role") this.#switchRole();
  };

  readonly #onSurfaceOpen = (event: CustomEvent<{ appId: string }>): void => this.#open(event.detail.appId);
  readonly #onRailSelect = (event: CustomEvent<{ key: string }>): void => this.#open(event.detail.key);

  readonly #onPin = (event: CustomEvent<{ key: string; pinned: boolean }>): void => {
    const state = this.#state;
    if (state.phase !== "desk") return;
    const { key, pinned } = event.detail;
    const userId = state.session.userId;
    const change = pinned ? this.#pins.add(userId, key) : this.#pins.remove(userId, key);
    this.#notice = !change.ok && change.reason === "cap" ? "You can pin up to eight surfaces. Unpin one to pin another." : "";
    this.requestUpdate();
  };

  readonly #onAccount = (): void => {
    this.#menuOpen = !this.#menuOpen;
    this.requestUpdate();
    if (this.#menuOpen) {
      void this.updateComplete.then(() => this.renderRoot.querySelector<HTMLElement>('[data-testid="account-menu"] button')?.focus());
    }
  };

  readonly #onKeyDown = (event: KeyboardEvent): void => {
    if (event.key === "Escape" && this.#menuOpen) {
      this.#menuOpen = false;
      this.requestUpdate();
      void this.updateComplete.then(() => this.#header()?.shadowRoot?.querySelector<HTMLElement>("button.account")?.focus());
    }
  };

  readonly #onPointerDown = (event: Event): void => {
    if (!this.#menuOpen) return;
    const inside = event
      .composedPath()
      .some((node) => node instanceof Element && (node.getAttribute("data-testid") === "account-menu" || node.getAttribute("data-action") === "account" || node.classList?.contains("account")));
    if (inside) return;
    this.#menuOpen = false;
    this.requestUpdate();
  };

  /** A hosted app's own Switch role (it assigns the origin root; the framed host forwards it here). */
  readonly #onMessage = (event: MessageEvent): void => {
    const frame = this.renderRoot.querySelector<HTMLIFrameElement>('iframe[data-testid="app-frame"]');
    if (frame === null) return;
    if (acceptSwitchRole(event, { origin: window.location.origin, contentWindow: frame.contentWindow })) this.#switchRole();
  };

  readonly #onConnection = (): void => {
    this.#online = navigator.onLine;
    this.requestUpdate();
  };

  #applyTheme(): void {
    const systemLight = typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: light)").matches;
    applyTheme(document.documentElement, systemLight);
  }

  #header(): ShellHeader | null {
    return this.renderRoot.querySelector<ShellHeader>("hh-shell-header");
  }

  // ── render ─────────────────────────────────────────────────────────────

  render(): TemplateResult {
    const state = this.#state;
    const roleCode = state.phase === "desk" ? state.session.roleCode : "";
    const appId = state.phase === "desk" ? (state.currentAppId ?? "") : "";
    return html`
      <div class="shell" data-testid="ws-shell" data-phase=${state.phase} data-role=${roleCode} data-current-app=${appId}>
        ${this.#fault !== "" ? this.#faultView() : state.phase === "door" ? this.#door() : this.#desk(state)}
      </div>
    `;
  }

  #faultView(): TemplateResult {
    return html`
      <div class="state" data-testid="profile-fault" role="alert">
        <h2>HH Work cannot start</h2>
        <p>${this.#fault}</p>
      </div>
    `;
  }

  #door(): TemplateResult {
    // The front door is the sign in and the role picker; a frame per visit, so
    // the picker always comes up clean after a Switch role.
    return html`<main>${this.#apps === null ? nothing : html`<iframe data-testid="door-frame" title="Sign in and pick a role" src=${hhAddress(DOOR_PATH)}></iframe>`}</main>`;
  }

  #desk(state: Extract<HostState, { phase: "desk" }>): TemplateResult {
    const current = state.apps.find((a) => a.id === state.currentAppId);
    return html`
      <div class="top">
        <hh-shell-header></hh-shell-header>
        ${this.#menuOpen
          ? html`
              <div class="menu" data-testid="account-menu" role="group" aria-label="Account">
                <p>Signed in as <strong>${state.session.roleLabel}</strong></p>
                <button type="button" class="btn-secondary" data-action="switch-role">Switch role</button>
              </div>
            `
          : nothing}
      </div>
      <hh-shell-offline-banner></hh-shell-offline-banner>
      ${this.#notice === "" ? nothing : html`<p class="notice" role="status">${this.#notice}</p>`}
      <div class="body">
        <hh-shell-rail></hh-shell-rail>
        <main>
          ${current === undefined
            ? html`
                <div class="state" data-testid="no-apps">
                  <h2>No apps for this role</h2>
                  <p>${state.session.roleLabel} has no desk apps in HH Work. Use Switch role to pick another role.</p>
                  <button type="button" class="btn-secondary" data-action="switch-role">Switch role</button>
                </div>
              `
            : keyed(current.id, html`<iframe data-testid="app-frame" data-title=${current.name} src=${hhAddress(current.path)}></iframe>`)}
        </main>
      </div>
    `;
  }

  /** The chrome's model objects ride properties, set here: the design system's position gate refuses bound properties in templates. */
  protected override updated(_changed: PropertyValues): void {
    // The frame's accessible name rides data-title (the gate refuses a bound title attribute).
    for (const frame of this.renderRoot.querySelectorAll<HTMLIFrameElement>("iframe[data-title]")) {
      const name = frame.getAttribute("data-title") ?? "";
      if (frame.title !== name) frame.title = name;
    }
    const state = this.#state;
    if (state.phase !== "desk") return;
    const header = this.#header();
    const rail = this.renderRoot.querySelector<ShellRail>("hh-shell-rail");
    const banner = this.renderRoot.querySelector<ShellBanner>("hh-shell-offline-banner");
    const userId = state.session.userId;
    const apps = state.apps;
    const currentId = state.currentAppId ?? "";
    const chip = chipInputFor({ online: this.#online });
    const pinned = this.#pins.pins(userId);
    const held = new Set(apps.map((a) => a.id));
    if (header !== null) {
      header.org = DEMO_ORG_NAME;
      header.user = state.session.roleLabel;
      header.groups = switcherGroups({ frontends: apps, roles: [state.session.roleCode], surfaces: NO_SURFACES });
      header.breadcrumb = currentBreadcrumb({
        frontends: apps,
        surfaces: NO_SURFACES,
        current: currentId === "" ? null : { appId: currentId, surfaceId: "" },
      });
      header.currentKey = currentId;
      header.pinned = currentId !== "" && pinned.includes(currentId);
      header.syncState = chip.state;
      header.cards = chip.openCards;
      header.waiting = chip.waiting;
      header.attempts = chip.attempts;
      header.stalls = chip.stalls;
    }
    if (rail !== null) {
      const rows: RailPin[] = pinned
        .filter((key) => held.has(key))
        .map((key) => {
          const app = apps.find((a) => a.id === key) as WorkApp;
          return { key, label: app.name, glyph: initialsOf(app.name) };
        });
      rail.pins = rows;
      rail.active = rows.some((r) => r.key === currentId) ? currentId : "";
      rail.heading = "Pinned";
      rail.storage = this.#pinStorage;
    }
    if (banner !== null) banner.mode = bannerModeFor({ online: this.#online });
  }
}

async function fetchJson(path: string): Promise<unknown> {
  const response = await fetch(path, { credentials: "same-origin" });
  if (!response.ok) throw new Error(`${path} answered ${response.status}`);
  return (await response.json()) as unknown;
}

function safeLocalStorage(): Storage | undefined {
  try {
    return window.localStorage;
  } catch {
    return undefined;
  }
}

defineElement("ws-host", WorkShellHost);

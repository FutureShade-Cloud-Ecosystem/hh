// The front door: the demo box's first screen (HH-DEMO-1 D2). One element,
// three states, one header.
//
//   picker      pre-auth: "Pick a role to walk the demo.", the two live role
//               cards, and the decided matrix's eleven other roles as coming
//               rows that show their modules and never tap.
//   home        post-auth: the role's modules as tiles. The box's two live
//               modules navigate (D1 wires the routes); the manager's three
//               not-yet-on-the-box modules open the polite coming-soon page.
//   coming-soon a module a tile pointed at that the box does not serve yet.
//
// The header carries every state: the brand lockup and the demo badge on
// the left (the ERP shell's HH Yard lockup; the badge is the till's gold
// chip, this app's own styles over the same token names), the role's name
// centred when signed in and "Pick a role" when not, and Switch role and
// Sign out on the right, both signed-in only, both clearing the session
// and returning to the picker.
//
// The sign-in path is the original demo's pattern (the reference this app
// ports, /tmp/demo-ref read only, app/src/demo.ts): one mint at a time, a
// refusal shown verbatim inside the card that earned it while the other card
// keeps working, a polite region rendered unconditionally so assistive tech
// hears the pointer even when the card is off screen, and every await between
// the tap and the state change bounded by the transport's own deadline, so
// nobody is stranded on a spinner. What it does NOT port is the old demo's
// job of adopting a session into an app shell: the front door holds the
// minted session for the box (src/session.ts) and the tiles navigate.
//
// The page never fences anything. Which role a mint yields is the server's
// closed table's answer alone; the coming rows are honesty about scope, not
// a client-side gate, and their buttons are disabled because their screens
// do not exist on this box.
//
// Rendering is Lit over the design system's tokens and base styles, the
// icons come from the ported registry (the one registry, the till's own
// files, imported across the app boundary), and the clicks ride the host
// delegation rule: listen on the HOST, read the control off composedPath(),
// never @click in a template and never event.target.closest, because the
// shadow boundary retargets the target to this element.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient, staffDemo, staffFeedback } from "@hardscapehouse/api-client";
import {
  HHFeedback,
  type FeedbackSender,
} from "@hardscapehouse/design-system/src/feedback.js";
// The ported icon registry: the one registry, the shared POS kit's own files
// (moved once out of the Counter Till's app so the box's apps import the same
// pieces). Only type imports reach the kit's ports from here, so the front
// door's bundle carries the registry and the drawing rules, not the till.
import { drawIcon, iconForId } from "@hardscapehouse/pos-kit/src/ported/_icon-consumer.js";
import {
  COMING_ROLES,
  LIVE_ROLES,
  type ComingRole,
  type LiveRole,
  type ModuleTile,
} from "./roles.js";
import {
  FrontDoorSessionStore,
  validateMint,
  type FrontDoorSession,
} from "./session.js";
import { applyTheme } from "./theme.js";

const DEMO_ORG = "hh-demo";

type Screen = { kind: "picker" } | { kind: "home" } | { kind: "coming-soon"; module: string };

/** The tile's one line of what the module is, keyed by the module's name. */
const TILE_BLURBS: Record<string, string> = {
  "Counter Till": "The counter sale flow.",
  "Quote Desk": "Size the job, build and send the quote.",
  "Manager Home": "What needs you, the approvals, the reports.",
  "Dealer Admin": "Staff, roles, pricing rules, the audit log.",
  "House Messages": "Staff channels and acknowledgements.",
};

function tileBlurb(tile: ModuleTile): string {
  return Object.hasOwn(TILE_BLURBS, tile.name) ? (TILE_BLURBS[tile.name] as string) : "";
}

export class FrontDoorApp extends HHElement {
  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
      }

      /* ── the header, every state ──────────────────────────────────────── */
      /* One bar, three zones: the brand lockup and the demo badge left, the
         word of the moment centred (truly centred: the side columns are
         equal fr tracks, so an empty right side cannot pull the word off
         centre), the signed-in controls right. Under the sheet's small
         breakpoint the zones stack and centre, so nothing overlaps and
         nothing jumps. */
      header {
        display: grid;
        grid-template-columns: 1fr auto 1fr;
        align-items: center;
        gap: var(--hh-s-12);
        box-sizing: border-box;
        min-height: 56px;
        padding: var(--hh-s-8) var(--hh-s-20);
        border-bottom: 1px solid var(--hh-line);
      }
      .demo-banner-cell {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: var(--hh-s-12);
      }
      /* The brand lockup, the ERP shell's own (hh-rd2's builder list, the
         reference): the HH mark at the left, the product name with the house
         line under it. Gold is a fill, never an ink, so the mark's "HH" is
         the near-black ink the gold fill carries; the accent word takes the
         gold-as-ink token, which is dark gold in the light theme. */
      .brand {
        display: flex;
        align-items: center;
        gap: 10px;
        min-width: 0;
      }
      .brand-mark {
        width: 30px;
        height: 30px;
        flex: none;
        display: grid;
        place-items: center;
        border-radius: var(--hh-radius-control);
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
        font-size: 12px;
        font-weight: 800;
        letter-spacing: -0.02em;
      }
      .brand-names {
        display: flex;
        flex-direction: column;
        gap: 1px;
        min-width: 0;
      }
      .brand-product {
        font-size: 16px;
        font-weight: 700;
        letter-spacing: -0.02em;
        line-height: 1.05;
      }
      .brand-product .accent {
        color: var(--hh-accent-text);
      }
      .brand-sub {
        font-size: 10px;
        font-weight: 600;
        letter-spacing: 0.055em;
        line-height: 1.2;
        color: var(--hh-text-dim);
        white-space: nowrap;
      }
      .who {
        margin: 0;
        justify-self: center;
        text-align: center;
        font-weight: 600;
        font-size: 15px;
      }
      .controls {
        justify-self: end;
        display: flex;
        align-items: center;
        gap: var(--hh-s-8);
      }
      @media (max-width: 480px) {
        header {
          grid-template-columns: 1fr;
          justify-items: center;
        }
        .demo-banner-cell {
          justify-self: center;
        }
        /* The band needs the room more than the house line does; the mark
           and the product name stay, the part that identifies the app. */
        .brand-sub {
          display: none;
        }
        .who {
          justify-self: center;
        }
        .controls {
          justify-self: center;
        }
      }
      .controls button {
        min-height: 0;
        padding: 7px 12px;
        font-size: 11px;
        line-height: normal;
        color: var(--hh-text-dim);
      }
      .controls button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      /* The demo badge: the till's gold chip, this app's own styles over the
         same token names the till's demo-banner.ts carries (the gold is a
         fill and carries the accent ink). */
      .demo-banner {
        display: inline-flex;
        flex-wrap: wrap;
        align-items: baseline;
        gap: var(--hh-s-8);
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
        border-radius: var(--hh-radius-control);
        padding: var(--hh-s-4) var(--hh-s-12);
      }
      .demo-banner .word {
        font-size: 12px;
        line-height: 1.3;
        font-weight: 700;
      }
      .demo-banner .copy {
        font-size: 12px;
        line-height: 1.3;
      }

      /* ── the stage: every state lives on one centred column ───────────── */
      .stage {
        max-width: 960px;
        margin: 0 auto;
        padding: var(--hh-s-40) var(--hh-s-8) var(--hh-s-64);
      }
      .headline {
        font-size: clamp(26px, 3.4vw, 34px);
        line-height: 1.15;
        font-weight: 600;
        letter-spacing: -0.02em;
        margin: 0 0 var(--hh-s-8);
      }
      .intro {
        color: var(--hh-text-dim);
        font-size: var(--hh-s-16);
        margin: 0 0 var(--hh-s-32);
      }

      /* ── the role cards: the page's lead. Larger than a list row, each with
         one line of what the role does and the call to act pinned to the
         card's foot, so both cards read whole at the same height and no
         empty band is left under the shorter one. ──────────────────────── */
      .cards {
        display: grid;
        grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
        gap: var(--hh-s-16);
        margin: 0 0 var(--hh-s-24);
      }
      .card {
        display: flex;
        align-items: flex-start;
        gap: var(--hh-s-16);
        min-height: 168px;
        text-align: start;
        padding: var(--hh-s-24);
        background: var(--hh-card);
        color: var(--hh-text);
        border: 1px solid var(--hh-control-line);
        border-radius: var(--hh-radius-card);
        box-shadow: var(--hh-widget-shadow);
        transition: border-color 0.15s ease, box-shadow 0.15s ease;
      }
      .card:hover:not(:disabled),
      .card:focus-visible {
        border-color: var(--hh-accent);
        box-shadow: var(--hh-elev-2);
      }
      /* The one card that is starting keeps its fill and takes the gold edge:
         a page with one thing happening on it looks like one thing happening.
         Its specificity outranks the base disabled rule, so the mint's own
         card never reads as refused while the rest go grey. */
      .card.starting {
        border-color: var(--hh-accent);
        background: var(--hh-card);
        color: var(--hh-text);
      }
      .card .mark {
        color: var(--hh-text-dim);
        flex: none;
      }
      .card:hover:not(:disabled) .mark,
      .card:focus-visible .mark,
      .card.starting .mark {
        color: var(--hh-accent-text);
      }
      .card .words {
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-8);
        flex: 1 1 auto;
        min-width: 0;
      }
      .card .role {
        font-size: var(--hh-s-20);
        font-weight: 600;
      }
      .card .line {
        font-size: 14px;
        line-height: 1.45;
        color: var(--hh-text-dim);
      }
      /* The call to act sits at the card's foot whatever the chips above do:
         the flex column stretches to the grid row's height, so the two
         cards' asks align on one line and the shorter card carries no void. */
      .card .cta {
        margin-top: auto;
        padding-top: var(--hh-s-4);
        font-size: 14px;
        font-weight: 600;
        color: var(--hh-accent-text);
      }
      .card:hover:not(:disabled) .cta,
      .card:focus-visible .cta,
      .card.starting .cta {
        text-decoration: underline;
      }
      .chips {
        display: flex;
        flex-wrap: wrap;
        gap: var(--hh-s-4);
      }
      .chip {
        font-size: var(--hh-s-12);
        line-height: 1.4;
        color: var(--hh-text-dim);
        background: var(--hh-row);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-pill);
        padding: 2px var(--hh-s-8);
        white-space: nowrap;
      }
      /* A refusal in the card that earned it: red as ink, never a fill, the
         original demo's rule on this token sheet's word for it. */
      .card .refusal {
        margin: 0;
        font-size: 13px;
        line-height: 1.45;
        color: var(--hh-danger);
      }

      /* ── the coming list: one collapsed disclosure. The eleven coming
         roles are honesty about scope, not a page of their own: closed they
         take one row, open they show today's list unchanged, chips muted
         as they ever were. ────────────────────────────────────────────── */
      .coming-card {
        background: var(--hh-card);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        padding: var(--hh-s-8) var(--hh-s-20);
        margin: 0;
      }
      .coming-card summary {
        display: flex;
        align-items: center;
        gap: var(--hh-s-12);
        min-height: 44px;
        cursor: pointer;
        list-style: none;
        color: var(--hh-text);
      }
      .coming-card summary::-webkit-details-marker {
        display: none;
      }
      .coming-card summary:focus-visible {
        outline: 2px solid var(--hh-focus-ring);
        outline-offset: 2px;
        border-radius: var(--hh-radius-control);
      }
      .coming-heading {
        font-size: var(--hh-s-16);
        font-weight: 600;
        margin: 0;
      }
      .chev {
        display: inline-flex;
        color: var(--hh-text-dim);
        /* Closed the chevron points down into the disclosure; open it
           returns to the registry's own up-pointing draw. */
        transform: rotate(180deg);
        transition: transform 0.15s ease;
      }
      .coming-card[open] .chev {
        transform: rotate(0deg);
      }
      .coming {
        list-style: none;
        margin: 0;
        padding: var(--hh-s-8) 0 var(--hh-s-12);
      }
      .coming-role {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: var(--hh-s-8) var(--hh-s-12);
        border-bottom: 1px solid var(--hh-line);
        padding: var(--hh-s-8) 0;
      }
      .coming li:last-child .coming-role {
        border-bottom: none;
      }
      .coming-role button {
        flex: none;
        min-height: 0;
        padding: var(--hh-s-4) 0;
        text-align: start;
        background: transparent;
        color: var(--hh-disabled-ink);
        border: none;
        font-weight: 600;
        cursor: default;
      }
      .coming-role .chips {
        flex: 1 1 240px;
      }
      /* The pill reads at the row's right edge whatever the chips do: beside
         them on one line, and pushed to the same edge on its own wrapped
         line at phone width, so a narrow window never leaves it stranded
         left. */
      .coming-role .coming-tag {
        margin-left: auto;
      }
      .coming-tag {
        flex: none;
        color: var(--hh-text-dim);
        font-size: var(--hh-s-12);
        background: var(--hh-row);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-pill);
        padding: 2px var(--hh-s-8);
        white-space: nowrap;
      }

      /* ── the role home: the live modules lead, the not-yet ones recede ── */
      .tiles {
        display: grid;
        grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
        gap: var(--hh-s-16);
        margin: 0;
      }
      .tile {
        position: relative;
        display: flex;
        align-items: flex-start;
        gap: var(--hh-s-16);
        min-height: 132px;
        text-align: start;
        padding: var(--hh-s-24);
        background: var(--hh-card);
        color: var(--hh-text);
        border: 1px solid var(--hh-control-line);
        border-radius: var(--hh-radius-card);
        box-shadow: var(--hh-widget-shadow);
        transition: border-color 0.15s ease, box-shadow 0.15s ease;
      }
      /* A live module is the page's lead: the gold edge says openable, the
         ask sits at the tile's foot the way the role card's does. */
      .tile.live {
        border-color: var(--hh-accent);
      }
      .tile:hover:not(:disabled) {
        border-color: var(--hh-accent);
        box-shadow: var(--hh-elev-2);
      }
      .tile .mark {
        color: var(--hh-text-dim);
        flex: none;
      }
      .tile:hover:not(:disabled) .mark,
      .tile:focus-visible .mark {
        color: var(--hh-accent-text);
      }
      .tile .words {
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-4);
        flex: 1 1 auto;
        min-width: 0;
      }
      .tile .module {
        font-size: var(--hh-s-16);
        font-weight: 600;
      }
      .tile .what {
        font-size: 13px;
        line-height: 1.4;
        color: var(--hh-text-dim);
        font-weight: 400;
      }
      .tile .cta {
        margin-top: auto;
        padding-top: var(--hh-s-4);
        font-size: 14px;
        font-weight: 600;
        color: var(--hh-accent-text);
      }
      .tile:hover:not(:disabled) .cta,
      .tile:focus-visible .cta {
        text-decoration: underline;
      }
      /* The modules the box does not serve yet, grouped under their own word
         and drawn quieter and smaller than the live tiles: present, honest,
         clearly second. */
      .coming-next {
        margin: var(--hh-s-32) 0 0;
      }
      .coming-next-heading {
        font-size: var(--hh-s-16);
        font-weight: 600;
        margin: 0 0 var(--hh-s-12);
        color: var(--hh-text-dim);
      }
      .coming-tiles {
        grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
      }
      .tile.muted {
        min-height: 64px;
        align-items: center;
        padding: var(--hh-s-16);
        border-color: var(--hh-line);
        box-shadow: none;
      }
      .tile.muted .mark {
        color: var(--hh-disabled-ink);
      }
      .tile.muted .module {
        font-size: 14px;
        color: var(--hh-disabled-ink);
      }

      /* ── the coming-soon page ─────────────────────────────────────────── */
      .soon {
        display: grid;
        justify-items: start;
        gap: var(--hh-s-16);
        max-width: 480px;
        margin: 0 auto;
        padding: var(--hh-s-32) 0;
      }
      .soon .mark {
        color: var(--hh-text-dim);
      }
      .soon h2 {
        display: flex;
        align-items: center;
        gap: var(--hh-s-12);
        margin: 0;
        font-size: clamp(22px, 2.6vw, 28px);
        font-weight: 600;
        letter-spacing: -0.02em;
      }
      .soon p {
        margin: 0;
        color: var(--hh-text-dim);
        line-height: 1.5;
      }
      .soon button {
        justify-self: start;
      }

      /* The polite region: rendered unconditionally, empty at rest, the
         original demo's rule (a region that arrives with its first sentence
         is frequently announced by nobody). */
      .notice {
        margin: var(--hh-s-16) 0 0;
        min-height: 1.5em;
        font-size: 13px;
        line-height: 1.5;
        color: var(--hh-text-dim);
      }
    `,
  ];

  readonly #store = new FrontDoorSessionStore();
  readonly #demoClient: ApiClient;
  #session: FrontDoorSession | null = null;
  #screen: Screen = { kind: "picker" };
  /** The flow whose mint is in flight, or null: one at a time. */
  #busyFlow: string | null = null;
  /** Per-card refusals, keyed by flow, cleared on that card's retry. */
  #refusals = new Map<string, string>();
  /** The polite region's sentence. Empty is the resting value. */
  #notice = "";

  constructor() {
    super();
    // The demo client carries no staff bearer: the demo routes are pre-auth
    // by design, and the served gate holds the pages and the mint behind
    // basic authentication, where the browser answers the challenge the way
    // it answered the page. The org header rides every call, the till's own
    // demo client's constant.
    this.#demoClient = new ApiClient({
      baseUrl: "",
      headers: async () => ({ "X-Org": DEMO_ORG }),
    });
    // The feedback widget's client: the mint's own bearer when a role is
    // signed in (the feedback route is staff guarded), and no credential
    // before that, so the widget's panel says what happened rather than the
    // gate. The token is read per request from the store, never held here.
    this.#feedbackClient = new ApiClient({
      baseUrl: "",
      headers: async () => {
        const headers: Record<string, string> = { "X-Org": DEMO_ORG };
        const session = this.#store.current();
        if (session !== null) headers["Authorization"] = `Bearer ${session.token}`;
        return headers;
      },
    });
  }

  /** The feedback bridge's client (the widget's sender rides it). */
  readonly #feedbackClient: ApiClient;

  /** The sender the widget calls on Send; the wire and its errors stay here. */
  #feedbackSender: FeedbackSender = async (request) => {
    const answer = await staffFeedback.feedbackCreateMultipart(this.#feedbackClient, {
      type: request.type,
      message: request.message,
      screen: request.screen,
      url: request.url,
      user_agent: request.userAgent,
      images: request.images.map((image) => image.blob),
    });
    if (answer.ok) {
      return { ok: true, issueNumber: answer.value.issue_number ?? "" };
    }
    return { ok: false, error: answer.error };
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    // Light is the box's default, the sheet's own attribute contract.
    applyTheme("light");
    // A reload or a back navigation in this tab restores the role the mint
    // was for, until the token's own expiry says otherwise. A new tab starts
    // at the picker: sessionStorage is per tab.
    const restored = this.#store.current();
    if (restored !== null) {
      this.#session = restored;
      this.#screen = { kind: "home" };
    }
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    super.disconnectedCallback();
  }

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
    if (action === "choose") {
      const flow = control.getAttribute("data-flow");
      if (typeof flow === "string" && flow !== "") this.#choose(flow);
    }
    if (action === "open-module") {
      const href = control.getAttribute("data-href");
      if (typeof href === "string" && href !== "") window.location.assign(href);
    }
    if (action === "coming-soon") {
      const module = control.getAttribute("data-module");
      if (typeof module === "string" && module !== "") this.#screen = { kind: "coming-soon", module };
      this.requestUpdate();
    }
    if (action === "back-home") {
      this.#screen = { kind: "home" };
      this.requestUpdate();
    }
    if (action === "switch-role") {
      this.#endSession();
      this.#notice = "Role cleared: pick a role.";
      this.requestUpdate();
    }
    if (action === "sign-out") {
      this.#endSession();
      this.#notice = "Signed out. The demo is ready for the next person.";
      this.requestUpdate();
    }
  };

  /** The one tap's work: mint, validate, hold, land on the role's home. */
  #choose(flow: string): void {
    if (this.#busyFlow !== null) return;
    const role = LIVE_ROLES.find((candidate) => candidate.flow === flow);
    if (role === undefined) return;
    this.#busyFlow = flow;
    // A previous refusal on this card is cleared on the retry rather than
    // left standing beside a fresh attempt.
    if (this.#refusals.has(flow)) {
      const next = new Map(this.#refusals);
      next.delete(flow);
      this.#refusals = next;
    }
    this.#notice = `Signing in as ${role.label}.`;
    this.requestUpdate();
    void this.#mint(flow, role);
  }

  async #mint(flow: string, role: LiveRole): Promise<void> {
    const answered = await staffDemo.demoCreateSession(this.#demoClient, { flow });
    this.#busyFlow = null;
    if (!answered.ok) {
      // The gate's or the mint's own words, verbatim, in the card that
      // earned them; the other card keeps working. The polite region
      // carries the pointer, not the sentence.
      this.#refusals = new Map(this.#refusals).set(flow, `Demo sign-in refused: ${answered.error}`);
      this.#notice = "The demo refused that sign in; the card says why.";
      this.requestUpdate();
      return;
    }
    const minted = validateMint(answered.value, flow, role.label, Date.now());
    if (minted === null) {
      this.#refusals = new Map(this.#refusals).set(
        flow,
        "Demo sign-in refused: the mint's answer is not a usable session.",
      );
      this.#notice = "The demo refused that sign in; the card says why.";
      this.requestUpdate();
      return;
    }
    this.#store.adopt(minted);
    this.#session = minted;
    this.#screen = { kind: "home" };
    this.#notice = "";
    this.requestUpdate();
  }

  /** Switch role and Sign out end the same way: custody wiped, picker back. */
  #endSession(): void {
    this.#store.clear();
    this.#session = null;
    this.#screen = { kind: "picker" };
    this.#refusals = new Map();
  }

  render(): TemplateResult {
    return html`
      ${this.#header()}
      <main>
        ${this.#screen.kind === "picker" ? this.#picker() : nothing}
        ${this.#screen.kind === "home" ? this.#home() : nothing}
        ${this.#screen.kind === "coming-soon" ? this.#comingSoon() : nothing}
      </main>
      <div class="stage">
        <p class="notice" role="status" aria-live="polite" aria-atomic="true">${this.#notice}</p>
      </div>
      <hh-feedback screen="Front door"></hh-feedback>
    `;
  }

  /** The widget's sender rides the property, never a template binding (the
   * design system's position gate refuses . bindings in dependents too). */
  protected override updated(): void {
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) {
      widget.send = this.#feedbackSender;
    }
  }

  #header(): TemplateResult {
    const session = this.#session;
    const signedIn = session !== null;
    return html`
      <header>
        <div class="demo-banner-cell">
          <div class="brand" role="img" aria-label="HH Yard, a Hardscape House product">
            <span class="brand-mark" aria-hidden="true">HH</span>
            <span class="brand-names">
              <span class="brand-product">HH <span class="accent">Yard</span></span>
              <span class="brand-sub">a Hardscape House product</span>
            </span>
          </div>
          <div class="demo-banner" role="note">
            <span class="word">Demo</span>
            <span class="copy">Everything on this box is invented demo data: nothing here is real.</span>
          </div>
        </div>
        <p class="who" data-testid="header-word">${session === null ? "Pick a role" : session.roleLabel}</p>
        <div class="controls">
          ${signedIn
            ? html`
                <button type="button" class="btn-secondary" data-action="switch-role">Switch role</button>
                <button type="button" class="btn-secondary" data-action="sign-out">Sign out</button>
              `
            : nothing}
        </div>
      </header>
    `;
  }

  #picker(): TemplateResult {
    return html`
      <div class="stage">
        <h2 class="headline">Pick a role to walk the demo.</h2>
        <p class="intro">Tap a role to sign in as it and open what it works:</p>
        <div class="cards">
          ${LIVE_ROLES.map((role) => this.#roleCard(role))}
        </div>
        <details class="coming-card" data-testid="coming-disclosure">
          <summary>
            <span class="coming-heading">Eleven more roles are coming</span>
            <span class="chev" aria-hidden="true">${drawIcon(iconForId("lucide:chevron-up"), 16)}</span>
          </summary>
          <ul class="coming" aria-label="Coming roles">
            ${COMING_ROLES.map((role) => html`<li>${this.#comingRow(role)}</li>`)}
          </ul>
        </details>
      </div>
    `;
  }

  #roleCard(role: LiveRole): TemplateResult {
    const starting = this.#busyFlow === role.flow;
    const refusal = this.#refusals.get(role.flow);
    return html`
      <button
        type="button"
        class="card ${starting ? "starting" : ""}"
        data-action="choose"
        data-flow=${role.flow}
        aria-label=${role.label}
        ?disabled=${this.#busyFlow !== null}
      >
        <span class="mark">${drawIcon(iconForId(role.icon), 32)}</span>
        <span class="words">
          <span class="role">${role.label}</span>
          <span class="line">${role.line}</span>
          <span class="chips" aria-hidden="true">
            ${role.tiles.map((tile) => html`<span class="chip">${tile.name}</span>`)}
          </span>
          <span class="cta" aria-hidden="true">Sign in as ${role.label}</span>
          ${refusal === undefined ? nothing : html`<span class="refusal">${refusal}</span>`}
        </span>
      </button>
    `;
  }

  #comingRow(role: ComingRole): TemplateResult {
    return html`
      <div class="coming-role">
        <button type="button" disabled aria-label=${role.label}>${role.label}</button>
        <span class="chips" aria-hidden="true">
          ${role.modules.map((module) => html`<span class="chip">${module}</span>`)}
        </span>
        <span class="coming-tag">Coming</span>
      </div>
    `;
  }

  #home(): TemplateResult {
    const session = this.#session;
    const role =
      session === null
        ? undefined
        : LIVE_ROLES.find((candidate) => candidate.flow === session.flow);
    if (session === null || role === undefined) {
      // A flow the catalog does not know cannot render a home: the honest
      // answer is the picker, not a wrong page. The custody stays (the
      // server minted it); only this page declines to guess.
      return this.#picker();
    }
    return html`
      <div class="stage">
        <h2 class="headline" data-testid="home-title">${role.label}</h2>
        <p class="intro">Open a module to walk it</p>
        <div class="tiles">
          ${role.tiles.filter((tile) => tile.href !== null).map((tile) => this.#tile(tile))}
        </div>
        ${
          role.tiles.some((tile) => tile.href === null)
            ? html`
                <section class="coming-next">
                  <h3 class="coming-next-heading">Coming next</h3>
                  <div class="tiles coming-tiles">
                    ${role.tiles.filter((tile) => tile.href === null).map((tile) => this.#comingTile(tile))}
                  </div>
                </section>
              `
            : nothing
        }
      </div>
    `;
  }

  #tile(tile: ModuleTile): TemplateResult {
    return html`
      <button
        type="button"
        class="tile live"
        data-action="open-module"
        data-href=${tile.href ?? nothing}
        aria-label=${tile.name}
      >
        <span class="mark">${drawIcon(iconForId(tile.icon), 32)}</span>
        <span class="words">
          <span class="module">${tile.name}</span>
          <span class="what">${tileBlurb(tile)}</span>
          <span class="cta" aria-hidden="true">Open the ${tile.name}</span>
        </span>
      </button>
    `;
  }

  #comingTile(tile: ModuleTile): TemplateResult {
    return html`
      <button
        type="button"
        class="tile muted"
        data-action="coming-soon"
        data-module=${tile.name}
        aria-label=${tile.name}
      >
        <span class="mark">${drawIcon(iconForId(tile.icon), 20)}</span>
        <span class="words">
          <span class="module">${tile.name}</span>
        </span>
      </button>
    `;
  }

  #comingSoon(): TemplateResult {
    if (this.#screen.kind !== "coming-soon") return this.#picker();
    const module = this.#screen.module;
    const session = this.#session;
    const roleLabel = session === null ? "" : session.roleLabel;
    const tile = LIVE_ROLES.flatMap((role) => role.tiles).find((candidate) => candidate.name === module);
    return html`
      <div class="stage">
        <div class="soon">
          <span class="mark">${drawIcon(iconForId(tile?.icon ?? "lucide:circle"), 29)}</span>
          <h2>
            ${module}
            <span class="coming-tag">Coming</span>
          </h2>
          <p>The ${module} is not on this demo box yet. What you signed in as still works from the role's home.</p>
          <button type="button" class="btn-secondary" data-action="back-home">
            Back to${roleLabel === "" ? " the role home" : ` ${roleLabel}`}
          </button>
        </div>
      </div>
    `;
  }
}

defineElement("fd-app-root", FrontDoorApp);

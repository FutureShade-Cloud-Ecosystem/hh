// The front door: the demo box's first screen (HH-DEMO-1 D2). One element,
// three states, one header.
//
//   picker      pre-auth: "Pick a role to walk the demo.", the live role
//               cards, the contractor cards, and the decided matrix's other roles as coming
//               rows that show their modules and never tap.
//   home        post-auth: the role's modules as tiles. The box's live
//               modules navigate (D1 wires the routes); the manager's two
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
import { ApiClient, staffDemo, staffFeedback, staffIdentity } from "@hardscapehouse/api-client";
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
  comingHeading,
  LIVE_ROLES,
  contractorEntries,
  type ComingRole,
  type ContractorEntry,
  type LiveRole,
  type ModuleTile,
} from "./roles.js";
import {
  FrontDoorSessionStore,
  validateMint,
  type FrontDoorSession,
} from "./session.js";
import { countMarginApprovals } from "./approvals-count.js";
import { createAppwriteAccountSurface } from "@hardscapehouse/auth/appwrite-adapter";
import { loadRuntimeConfig, type RuntimeConfigFailure } from "@hardscapehouse/auth/runtime-config";
import { ConsoleSession, coreBaseFor } from "./console.js";
import { consoleStyles } from "./console-styles.js";
import { bootScreen, configErrorScreen, dealerLockup, personaCards, signInScreen } from "./console-views.js";
import { landingFor } from "./signin.js";
import { THEME_LABEL, ThemeController, themeControlLabel, themeStatusText } from "./theme.js";

const DEMO_ORG = "hh-demo";

type Screen = { kind: "picker" } | { kind: "home" } | { kind: "coming-soon"; module: string };

/**
 * How this copy signs people in, from the served config.json: `boot` until it
 * is read, `demo` (tap a role), `console` (the real sign in), or
 * `config-error` (no usable mode: an honest error page, never the demo cards).
 */
type Mode = "boot" | "demo" | "console" | "config-error";

/** The tile's one line of what the module is, keyed by the module's name. */
const TILE_BLURBS: Record<string, string> = {
  "Counter Till": "The counter sale flow.",
  "Yard Pick": "Pull the picks, count the skids, stage what is done.",
  "Quote Desk": "Size the job, build and send the quote.",
  "Money Desk": "Receivables, payables and the bounced payment queue.",
  "Buying and Stock": "Draft and place orders, and see what is on the way.",
  "Manager Home": "What needs you, the approvals, the reports.",
  "Dealer Admin": "Staff, roles, pricing rules, the audit log.",
  "HH Drive": "The day's run, stop to proof of delivery.",
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
        /* The side tracks never shrink under their content (round 7: on Manager Home the longer
           role word left the lockup's track too narrow and the Demo badge dropped under the
           logo); they still share what is left equally, so the word stays centred when it can. */
        grid-template-columns: minmax(max-content, 1fr) auto minmax(max-content, 1fr);
        align-items: center;
        gap: var(--hh-s-12);
        box-sizing: border-box;
        min-height: 56px;
        padding: var(--hh-s-8) var(--hh-s-20);
        border-bottom: 1px solid var(--hh-line);
      }
      .demo-banner-cell {
        display: flex;
        flex-wrap: nowrap;
        align-items: center;
        gap: var(--hh-s-12);
      }
      /* The brand lockup, the ERP shell's own (hh-rd2's builder list, the
         reference): the HH mark at the left, the product name with the house
         line under it. Gold is a fill, never an ink, so the mark's H is the
         near-black ink the gold fill carries; the accent word takes the
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
        /* The Hardscape House badge, on yellow: the square is the badge's
           gold ground (the badge yellow IS --hh-accent), square cornered as
           the source colourways are, and the inline mark-only SVG draws the
           measured H in the ink a gold fill carries, through currentColor.
           The path is principal/assets/hh-logos/svg/mark.svg, pixel measured
           from the 3000 px colourways; the design system package carries the
           same files under assets/hh-badge. */
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
      }
      .brand-mark svg {
        width: 100%;
        height: 100%;
        display: block;
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
      /* Too narrow for one row (the lockup, the badge, the word and the controls need about
         1200px): the zones stack and centre, and the badge may wrap under the lockup here. */
      @media (max-width: 1200px) {
        header {
          grid-template-columns: 1fr;
          justify-items: center;
        }
        .demo-banner-cell {
          justify-self: center;
          flex-wrap: wrap;
          justify-content: center;
        }
        .controls {
          justify-self: center;
        }
      }
      @media (max-width: 480px) {
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
      .controls {
        flex-wrap: wrap;
        justify-content: center;
      }
      .controls button {
        white-space: nowrap;
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

      /* ── the coming list: one collapsed disclosure. The coming
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
        /* Round 2 (FROM-PRINCIPAL 9): the name wore the disabled INK on the card, and in
           brand that pair reads 4.30:1 (the ink's own pair is on the disabled FILL, which
           this lighter treatment drops). The name reads the dimmed text ink instead, the
           ink the matrix holds at AA on the card in both halves and the one the chips and
           the Coming pill beside it already carry; the disabled state stays in the
           semantics and the pill, held by test/contrast.test.mjs. */
        color: var(--hh-text-dim);
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
      .tile .approval-note {
        font-size: 12px;
        color: var(--hh-text-dim);
      }
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
        /* Round 2 (FROM-PRINCIPAL 9): one rule with the coming name above; the muted name
           is text on the card and reads the dimmed text ink, not the disabled ink (4.30:1
           in brand). The tile stays muted by its border, its lost shadow and its mark. */
        color: var(--hh-text-dim);
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
    consoleStyles,
  ];

  readonly #store = new FrontDoorSessionStore();
  readonly #demoClient: ApiClient;
  #session: FrontDoorSession | null = null;
  #screen: Screen = { kind: "picker" };
  /** The flow whose mint is in flight, or null: one at a time. */
  #busyFlow: string | null = null;
  /** The contractor cards the flows listing offers (rows of class tenant); empty until it answers, and when it fails. */
  #contractors: readonly ContractorEntry[] = [];
  /** Per-card refusals, keyed by flow, cleared on that card's retry. */
  #refusals = new Map<string, string>();
  /** The polite region's sentence. Empty is the resting value. */
  #notice = "";
  /** The staff client carries the session's bearer token. */
  #staffClient: ApiClient;
  #mode: Mode = "boot";
  #configFailure: RuntimeConfigFailure = "missing";
  #console: ConsoleSession | null = null;
  #watch: ReturnType<typeof setInterval> | null = null;
  /** Count of pending margin approvals for the manager, fetched on session adopt. */
  #approvalCount = 0;

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
        const headers: Record<string, string> = { "X-Org": this.#org() };
        const bearer = this.#bearer();
        if (bearer !== null) headers["Authorization"] = `Bearer ${bearer}`;
        return headers;
      },
    });
    this.#staffClient = new ApiClient({
      baseUrl: "",
      headers: async () => ({
        Authorization: `Bearer ${this.#bearer() ?? ""}`,
        ...(this.#console !== null ? { "X-Org": this.#console.config.org } : {}),
      }),
    });
  }

  /** The feedback bridge's client (the widget's sender rides it). */
  #feedbackClient: ApiClient;

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

  /** The theme choice: Auto follows the system, Light and Brand pin it; carried in the tab's session storage. */
  readonly #theme = new ThemeController(() => this.requestUpdate());
  /** What a screen reader hears after a Theme press: focus stays on the button, so the new name is not re-read. */
  #themeStatus = "";

  /** Whether the shared Feedback panel is open, as the widget reports it. */
  #feedbackOpen = false;

  readonly #onFeedbackToggle = (event: Event): void => {
    const open = (event as CustomEvent<{ open: boolean }>).detail.open === true;
    this.#feedbackOpen = open;
    this.requestUpdate();
    // Closing the panel hands focus back to the button that opened it.
    if (!open) {
      void this.updateComplete.then(() => {
        this.renderRoot.querySelector<HTMLElement>('[data-testid="feedback-button"]')?.focus();
      });
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("hh-feedback-toggle", this.#onFeedbackToggle);
    this.addEventListener("click", this.#onHostClick);
    // Light is the box's default unless the person chose otherwise in this tab (the choice is carried
    // between the front door, the till and the Quote Desk); Auto follows the system.
    this.#theme.start();
    this.renderRoot.addEventListener("submit", this.#onSubmit);
    window.addEventListener("online", this.#onNetwork);
    window.addEventListener("offline", this.#onNetwork);
    void this.#boot();
  }

  /** Read the served config.json, then become the demo picker, the console sign in, or the error page. */
  async #boot(): Promise<void> {
    const loaded = await loadRuntimeConfig();
    if (!this.isConnected) return;
    if (!loaded.ok) {
      this.#configFailure = loaded.reason;
      this.#mode = "config-error";
      this.requestUpdate();
      return;
    }
    if (loaded.config.mode === "demo") {
      this.#mode = "demo";
      void this.#loadContractors();
      // A reload or a back navigation in this tab restores the role the mint
      // was for, until the token's own expiry says otherwise. A new tab starts
      // at the picker: sessionStorage is per tab.
      const restored = this.#store.current();
      if (restored !== null) {
        this.#session = restored;
        this.#screen = { kind: "home" };
        void this.#loadApprovals();
      }
      this.requestUpdate();
      return;
    }
    const config = loaded.config;
    let account;
    try {
      account = createAppwriteAccountSurface({
        endpoint: config.consoleEndpoint,
        projectId: config.consoleProjectId,
        coreBaseUrl: config.coreBaseUrl === "" ? window.location.origin : config.coreBaseUrl,
      });
    } catch {
      // A page origin that cannot carry a password (plain http off localhost) is a configuration fault.
      this.#configFailure = "incomplete";
      this.#mode = "config-error";
      this.requestUpdate();
      return;
    }
    const session = new ConsoleSession({
      config,
      account,
      fetch: globalThis.fetch.bind(globalThis),
      online: () => navigator.onLine !== false,
      pageUrl: () => window.location.href,
      replaceUrl: (url) => window.history.replaceState(null, "", url),
      onChange: () => this.requestUpdate(),
    });
    // The page's own core calls (the feedback send, the approvals count) were built before the
    // config was read, with the page's origin; a console copy may name its core elsewhere.
    const coreBase = coreBaseFor(config, window.location.origin);
    this.#feedbackClient = new ApiClient({
      baseUrl: coreBase,
      headers: async () => {
        const headers: Record<string, string> = { "X-Org": this.#org() };
        const bearer = this.#bearer();
        if (bearer !== null) headers["Authorization"] = `Bearer ${bearer}`;
        return headers;
      },
    });
    this.#staffClient = new ApiClient({
      baseUrl: coreBase,
      headers: async () => ({
        Authorization: `Bearer ${this.#bearer() ?? ""}`,
        "X-Org": config.org,
      }),
    });
    this.#console = session;
    this.#mode = "console";
    // A session that ended underneath the page (a refresh the core refused, a token that ran out) is noticed here.
    this.#watch = setInterval(() => session.tick(), 2000);
    this.requestUpdate();
    await session.start();
    if (session.phase === "signed-in" && session.landed) void this.#loadApprovals();
  }

  /** The org header the page's own calls carry: the demo box's constant, or the console copy's org. */
  #org(): string {
    return this.#console !== null ? this.#console.config.org : DEMO_ORG;
  }

  /** The bearer a call made by this page carries: the demo mint's, or the core's session token. Read per request. */
  #bearer(): string | null {
    if (this.#console !== null) return this.#console.bearer;
    return this.#store.current()?.token ?? null;
  }

  readonly #onNetwork = (): void => {
    this.#console?.setOnline();
  };

  readonly #onSubmit = (event: Event): void => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || form.getAttribute("data-form") !== "signin") return;
    // Never a navigation: the password must not travel in an address or a reload.
    event.preventDefault();
    const field = (name: string): string => {
      const control = form.elements.namedItem(name);
      return control instanceof HTMLInputElement ? control.value : "";
    };
    void this.#console?.submit(field("email"), field("password"));
  };

  disconnectedCallback(): void {
    if (this.#watch !== null) clearInterval(this.#watch);
    this.#watch = null;
    this.renderRoot.removeEventListener("submit", this.#onSubmit);
    window.removeEventListener("online", this.#onNetwork);
    window.removeEventListener("offline", this.#onNetwork);
    this.#theme.stop();
    this.removeEventListener("hh-feedback-toggle", this.#onFeedbackToggle);
    this.removeEventListener("click", this.#onHostClick);
    super.disconnectedCallback();
  }

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
    if (action === "choose" && this.#mode === "demo") {
      const flow = control.getAttribute("data-flow");
      if (typeof flow === "string" && flow !== "") this.#choose(flow);
    }
    if (action === "theme") {
      this.#theme.cycle();
      this.#themeStatus = themeStatusText(this.#theme.choice);
      this.requestUpdate();
    }
    if (action === "feedback") {
      const widget = this.renderRoot.querySelector("hh-feedback");
      if (widget instanceof HHFeedback) widget.show();
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
      if (this.#console !== null) {
        this.#approvalCount = 0;
        this.#screen = { kind: "home" };
        void this.#console.switchRole();
        return;
      }
      this.#endSession();
      this.#notice = "Role cleared: pick a role.";
      this.requestUpdate();
    }
    if (action === "sign-out") {
      if (this.#console !== null) {
        this.#approvalCount = 0;
        this.#screen = { kind: "home" };
        void this.#console.signOut();
        return;
      }
      this.#endSession();
      this.#notice = "Signed out. The demo is ready for the next person.";
      this.requestUpdate();
    }
    if (this.#console !== null) this.#onConsoleAction(action, control);
    if (action === "reload") window.location.reload();
  };

  /** The console mode's own controls; the shared ones (theme, feedback, modules) are handled above. */
  #onConsoleAction(action: string | null, control: Element): void {
    const session = this.#console;
    if (session === null) return;
    if (action === "google") void session.google();
    if (action === "methods-retry") void session.retryMethods();
    if (action === "continue-own") {
      session.continueOwn();
      this.#screen = { kind: "home" };
      void this.#loadApprovals();
    }
    if (action === "persona") {
      const persona = control.getAttribute("data-persona");
      if (typeof persona === "string" && persona !== "") {
        this.#screen = { kind: "home" };
        void session.persona(persona).then(() => this.#loadApprovals());
      }
    }
  }

  /**
   * The contractor cards come from the flows listing's class: a tenant row is a contractor persona and
   * is routed to the Pro app for its role. A listing that fails shows no contractor cards and leaves the
   * staff cards working: the listing is only the box's own table, never a gate.
   */
  async #loadContractors(): Promise<void> {
    const answered = await staffDemo.demoListFlows(this.#demoClient);
    if (!answered.ok) return;
    this.#contractors = contractorEntries(answered.value.flows);
    this.requestUpdate();
  }

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
      this.#refusals = new Map(this.#refusals).set(flow, `Sign-in did not go through. ${answered.error}`);
      this.#notice = "The demo refused that sign in; the card says why.";
      this.requestUpdate();
      return;
    }
    const minted = validateMint(answered.value, flow, role.label, Date.now());
    if (minted === null) {
      this.#refusals = new Map(this.#refusals).set(
        flow,
        "Sign-in did not go through. Pick the role again.",
      );
      this.#notice = "The demo refused that sign in; the card says why.";
      this.requestUpdate();
      return;
    }
    this.#store.adopt(minted);
    this.#session = minted;
    this.#screen = { kind: "home" };
    this.#notice = "";
    void this.#loadApprovals();
    this.requestUpdate();
  }

  /** Switch role and Sign out end the same way: custody wiped, picker back. */
  #endSession(): void {
    this.#store.clear();
    this.#session = null;
    this.#approvalCount = 0;
    this.#screen = { kind: "picker" };
    this.#refusals = new Map();
  }

  /** Fetch pending approval count for the sales manager role. */
  async #loadApprovals(): Promise<void> {
    const session = this.#identity();
    if (session === null) return;
    const key = `${session.roleCode}|${session.name}`;
    if (session.roleCode !== "sales_manager") {
      this.#approvalCount = 0;
      return;
    }
    const count = await countMarginApprovals((cursor) =>
      staffIdentity.identityListApprovals(this.#staffClient, {
        status: "PENDING",
        ...(cursor !== undefined ? { cursor } : {}),
      }),
    );
    // The session changed while the request was out: the answer is for someone else.
    const after = this.#identity();
    if (after === null || `${after.roleCode}|${after.name}` !== key) return;
    this.#approvalCount = count ?? 0;
    this.requestUpdate();
  }

  render(): TemplateResult {
    return html`
      <p class="visually-hidden" role="status" aria-live="polite" data-testid="theme-status">${this.#themeStatus}</p>
      ${this.#header()}
      <main>${this.#body()}</main>
      ${this.#mode === "demo"
        ? html`<div class="stage">
            <p class="notice" role="status" aria-live="polite" aria-atomic="true">${this.#notice}</p>
          </div>`
        : nothing}
      <hh-feedback screen="Front door" trigger="external"></hh-feedback>
    `;
  }

  #body(): TemplateResult | typeof nothing {
    if (this.#mode === "boot") return bootScreen();
    if (this.#mode === "config-error") return configErrorScreen(this.#configFailure);
    const consoleSession = this.#console;
    if (this.#mode === "console" && consoleSession !== null) {
      if (consoleSession.phase === "starting") return bootScreen();
      if (consoleSession.phase === "signed-in") {
        if (!consoleSession.landed) return this.#entry(consoleSession);
        return this.#screen.kind === "coming-soon" ? this.#comingSoon() : this.#home();
      }
      const open = consoleSession.config.personas && consoleSession.config.personasOpen;
      return signInScreen(
        consoleSession,
        open
          ? html`<div class="persona-stage">
              <h2 class="entry-subhead">Or walk the demo as a role</h2>
              ${this.#personaCards(consoleSession)}
            </div>`
          : nothing,
      );
    }
    if (this.#screen.kind === "picker") return this.#picker();
    if (this.#screen.kind === "home") return this.#home();
    return this.#comingSoon();
  }

  /** The eight live cards and the five coming rows, calling the persona route. */
  #personaCards(session: ConsoleSession): TemplateResult {
    return personaCards(
      session,
      (role, refusal, busy) =>
        this.#roleCard(role, {
          action: "persona",
          verb: "Enter as",
          busy,
          anyBusy: session.personaBusy !== null,
          refusal: refusal === "" ? undefined : refusal,
        }),
      () => this.#comingList(),
    );
  }

  /** The role entry after sign in: continue as the person's own role, and the persona cards where the deployment has them. */
  #entry(session: ConsoleSession): TemplateResult {
    const who = session.who;
    const landing = who === null ? null : landingFor(who.roleCode);
    return html`
      <div class="stage">
        <section class="entry-card" aria-labelledby="entry-title" data-testid="role-entry">
          <h2 id="entry-title">You are signed in</h2>
          <p data-testid="entry-name">${who?.name ?? ""}</p>
          ${landing === null
            ? html`<p data-testid="entry-no-screens">This site has no screens for your role yet. Ask your administrator.</p>`
            : html`
                <button type="button" class="btn btn-primary btn-wide" data-action="continue-own" data-testid="continue-own">
                  Continue as ${who?.roleLabel ?? landing.label}
                </button>
                <span class="chips" aria-hidden="true">
                  ${landing.tiles.map((tile) => html`<span class="chip">${tile.name}</span>`)}
                </span>
              `}
        </section>
        ${session.config.personas
          ? html`<h3 class="entry-subhead">Or walk the demo as another role</h3>${this.#personaCards(session)}`
          : nothing}
      </div>
    `;
  }

  /** The widget's sender rides the property, never a template binding (the
   * design system's position gate refuses . bindings in dependents too). */
  protected override updated(): void {
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) {
      widget.send = this.#feedbackSender;
    }
    // The fields stay put (value and focus) and only stop taking input while a sign in is in flight.
    const signingIn = this.#console?.phase === "signing-in";
    for (const id of ["#signin-email", "#signin-password"]) {
      const field = this.renderRoot.querySelector<HTMLInputElement>(id);
      if (field !== null) field.readOnly = signingIn;
    }
    // Focus returns to where the person was after a refused sign in: the
    // password field after wrong credentials, the button otherwise.
    const focus = this.#console?.takeFocus() ?? null;
    if (focus !== null) {
      const target = focus === "submit" ? '[data-testid="signin-submit"]' : `#signin-${focus}`;
      const control = this.renderRoot.querySelector<HTMLElement>(target);
      if (control !== null) {
        control.focus();
        if (control instanceof HTMLInputElement) control.select();
      }
    }
  }

  /** The HH Yard lockup, the ERP shell's own: the mark in the house gold, the product name and the house line. */
  #brand(): TemplateResult {
    return html`
          <div class="brand" role="img" aria-label="HH Yard, a Hardscape House product">
            <span class="brand-mark" aria-hidden="true">
              <svg viewBox="0 0 3000 3000">
                <path fill="currentColor" d="M644.94 465.17 H1386.06 A29.15 29.15 0 0 1 1415.21 494.32 V1306.38 A84.79 84.79 0 0 0 1500 1391.17 H2355.05 A29.15 29.15 0 0 1 2384.2 1420.32 V2505.68 A29.15 29.15 0 0 1 2355.05 2534.83 H1613.94 A29.15 29.15 0 0 1 1584.79 2505.68 V1940.1 A84.79 84.79 0 0 0 1415.21 1940.1 V2505.68 A29.15 29.15 0 0 1 1386.06 2534.83 H644.94 A29.15 29.15 0 0 1 615.79 2505.68 V494.32 A29.15 29.15 0 0 1 644.94 465.17 Z M1613.94 465.17 H2355.05 A29.15 29.15 0 0 1 2384.2 494.32 V1192.85 A29.15 29.15 0 0 1 2355.05 1222 H1613.94 A29.15 29.15 0 0 1 1584.79 1192.85 V494.32 A29.15 29.15 0 0 1 1613.94 465.17 Z" />
              </svg>
            </span>
            <span class="brand-names">
              <span class="brand-product">HH <span class="accent">Yard</span></span>
              <span class="brand-sub">a Hardscape House product</span>
            </span>
          </div>
    `;
  }

  /** Before the mode is known, and on the configuration error page: the lockup alone, no demo badge and no session controls. */
  #plainHeader(): TemplateResult {
    return html`
      <header>
        <div class="demo-banner-cell">${this.#brand()}</div>
        <p class="who" data-testid="header-word"></p>
        <div class="controls"></div>
      </header>
    `;
  }

  /**
   * The console copy's header band (RULES-S4 section 3): the dealer lockup and,
   * once signed in, the app and surface selector; the person's name and role in
   * the middle; Theme, Feedback and Sign out on the right. The pin control and
   * the rail of pinned surfaces are the shells cycle's.
   */
  #consoleHeader(session: ConsoleSession): TemplateResult {
    const who = session.who;
    const landing = who === null ? undefined : (landingFor(who.roleCode) ?? undefined);
    const openable = landing === undefined ? [] : landing.tiles.filter((tile) => tile.href !== null);
    return html`
      <header>
        <div class="demo-banner-cell">
          ${dealerLockup(session.config.dealer)}
          ${who !== null && session.landed
            ? html`
                <details class="apps-menu" data-testid="apps-menu">
                  <summary aria-label="Apps and surfaces">Apps</summary>
                  <ul class="apps-list">
                    ${openable.map(
                      (tile) =>
                        html`<li><button type="button" data-action="open-module" data-href=${tile.href ?? nothing}>${tile.name}</button></li>`,
                    )}
                  </ul>
                </details>
              `
            : nothing}
        </div>
        ${who === null
          ? html`<p class="who" data-testid="header-word">Sign in</p>`
          : html`<p class="person" data-testid="header-word">
              <span class="person-name">${who.name}</span>
              <span class="person-role">${who.roleLabel}</span>
            </p>`}
        <div class="controls console-controls">
          <button
            type="button"
            class="btn-secondary"
            data-action="theme"
            data-testid="theme-control"
            aria-label=${themeControlLabel(this.#theme.choice)}
            title="Cycle the theme: Auto, Light, Brand"
          >
            Theme: ${THEME_LABEL[this.#theme.choice]}
          </button>
          <button type="button" class="btn-secondary" data-action="feedback" data-testid="feedback-button" aria-haspopup="dialog" aria-expanded=${this.#feedbackOpen ? "true" : "false"} title="Send feedback about this screen">
            Feedback
          </button>
          ${who !== null && session.config.personas && session.landed
            ? html`<button type="button" class="btn-secondary" data-action="switch-role">Switch role</button>`
            : nothing}
          ${who !== null ? html`<button type="button" class="btn-secondary" data-action="sign-out">Sign out</button>` : nothing}
        </div>
      </header>
    `;
  }

  #header(): TemplateResult {
    if (this.#mode === "boot" || this.#mode === "config-error") return this.#plainHeader();
    if (this.#mode === "console" && this.#console !== null) return this.#consoleHeader(this.#console);
    const session = this.#session;
    const signedIn = session !== null;
    return html`
      <header>
        <div class="demo-banner-cell">
          ${this.#brand()}
          <div class="demo-banner" role="note">
            <span class="word">Demo</span>
            <span class="copy">Everything on this box is invented demo data: nothing here is real.</span>
          </div>
        </div>
        <p class="who" data-testid="header-word">${session === null ? "Pick a role" : session.roleLabel}</p>
        <div class="controls">
          <button
            type="button"
            class="btn-secondary"
            data-action="theme"
            data-testid="theme-control"
            aria-label=${themeControlLabel(this.#theme.choice)}
            title="Cycle the theme: Auto, Light, Brand"
          >
            Theme: ${THEME_LABEL[this.#theme.choice]}
          </button>
          <button type="button" class="btn-secondary" data-action="feedback" data-testid="feedback-button" aria-haspopup="dialog" aria-expanded=${this.#feedbackOpen ? "true" : "false"} title="Send feedback about this screen">
            Feedback
          </button>
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
          ${LIVE_ROLES.map((role) =>
            this.#roleCard(role, {
              action: "choose",
              verb: "Sign in as",
              busy: this.#busyFlow === role.flow,
              anyBusy: this.#busyFlow !== null,
              refusal: this.#refusals.get(role.flow),
            }),
          )}
        </div>
        ${this.#contractorSection()}
        ${this.#comingList()}
      </div>
    `;
  }

  /** The roles that are coming, as the one collapsed disclosure both modes show; nothing when none are. */
  #comingList(): TemplateResult {
    if (COMING_ROLES.length === 0) return html`${nothing}`;
    return html`
      <details class="coming-card" data-testid="coming-disclosure">
        <summary>
          <span class="coming-heading">${comingHeading(COMING_ROLES.length)}</span>
          <span class="chev" aria-hidden="true">${drawIcon(iconForId("lucide:chevron-up"), 16)}</span>
        </summary>
        <ul class="coming" aria-label="Coming roles">
          ${COMING_ROLES.map((role) => html`<li>${this.#comingRow(role)}</li>`)}
        </ul>
      </details>
    `;
  }

  /** One live role card. The demo's calls the mint with the flow; the console's calls the persona route with the flow id (the core's persona id). */
  #roleCard(
    role: LiveRole,
    options: { action: "choose" | "persona"; verb: string; busy: boolean; anyBusy: boolean; refusal: string | undefined },
  ): TemplateResult {
    const starting = options.busy;
    const refusal = options.refusal;
    return html`
      <button
        type="button"
        class="card ${starting ? "starting" : ""}"
        data-action=${options.action}
        data-flow=${options.action === "choose" ? role.flow : nothing}
        data-persona=${options.action === "persona" ? role.flow : nothing}
        aria-label=${role.label}
        ?disabled=${options.anyBusy}
      >
        <span class="mark">${drawIcon(iconForId(role.icon), 32)}</span>
        <span class="words">
          <span class="role">${role.label}</span>
          <span class="line">${role.line}</span>
          <span class="chips" aria-hidden="true">
            ${role.tiles.map((tile) => html`<span class="chip">${tile.name}</span>`)}
          </span>
          <span class="cta" aria-hidden="true">${options.verb} ${role.label}</span>
          ${refusal === undefined ? nothing : html`<span class="refusal">${refusal}</span>`}
        </span>
      </button>
    `;
  }

  /** Summit Ridge Builders' five roles: one card each, opening the Pro app for the role. */
  #contractorSection(): TemplateResult | typeof nothing {
    if (this.#contractors.length === 0) return nothing;
    return html`
      <section class="contractors" data-testid="contractor-section" aria-labelledby="contractor-heading">
        <h3 class="coming-next-heading" id="contractor-heading">Contractor accounts</h3>
        <p class="intro">Summit Ridge Builders buys from Gable Landscape Supply. Tap a role to open the Pro apps as it.</p>
        <div class="cards">
          ${this.#contractors.map(
            (entry) => html`
              <button type="button" class="card" data-action="open-module" data-href=${entry.href} data-testid="contractor-card" aria-label=${entry.label}>
                <span class="mark">${drawIcon(iconForId(entry.icon), 32)}</span>
                <span class="words">
                  <span class="role">${entry.label}</span>
                  <span class="line">${entry.line}</span>
                  <span class="chips" aria-hidden="true"><span class="chip">${entry.app}</span></span>
                  <span class="cta" aria-hidden="true">Open ${entry.app} as ${entry.label}</span>
                </span>
              </button>
            `,
          )}
        </div>
      </section>
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

  /** Who is signed in, in either mode: the one place the header, the home and the approvals read it. */
  #identity(): { label: string; roleCode: string; name: string } | null {
    if (this.#console !== null) {
      const who = this.#console.who;
      return who === null ? null : { label: who.roleLabel, roleCode: who.roleCode, name: who.name };
    }
    const session = this.#session;
    return session === null ? null : { label: session.roleLabel, roleCode: session.roleCode, name: session.user.name };
  }

  /** The live role whose home the signed in person lands on: by the mint's flow in the demo, by the person's own role code in the console. */
  #landingRole(): LiveRole | undefined {
    if (this.#console !== null) {
      const who = this.#console.who;
      return who === null ? undefined : (landingFor(who.roleCode) ?? undefined);
    }
    const session = this.#session;
    return session === null ? undefined : LIVE_ROLES.find((candidate) => candidate.flow === session.flow);
  }

  #home(): TemplateResult {
    const role = this.#landingRole();
    if (this.#console !== null && role === undefined) {
      return html`
        <div class="stage">
          <h2 class="headline" data-testid="home-title">No screens for your role yet</h2>
          <p class="intro">This site has no screens for your role yet. Ask your administrator.</p>
        </div>
      `;
    }
    if (this.#identity() === null || role === undefined) {
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
    const approvalNote =
      tile.name === "Quote Desk" && this.#approvalCount > 0
        ? html`<span class="approval-note">${this.#approvalCount} approval${this.#approvalCount === 1 ? "" : "s"} waiting</span>`
        : nothing;
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
          ${approvalNote}
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
    const roleLabel = this.#identity()?.label ?? "";
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

// Yard Pick's root element: the chrome and the way in. The way in is the
// demo's role cards (the shared kit's entry, the demo adapter behind the auth
// package's AccountSurface seam). After the mint the branch resolves once
// (the branches this login may reach; the demo yard team holds Trenton), the
// ports are wired with the caller's headers, and the board renders.
//
// The organisation header is the demo org by construction: the staff header
// provider refuses to send a request without X-Org, so no call ever leaves
// this app unresolved.
//
// The header is fed through plain attributes (signed-in, demo, compact) and,
// for the role label and theme, assignments in updated(): the design
// system's position gate refuses every "." and "@" binding and every bound
// value outside the data- and aria- attributes in a dependent package. Its
// buttons ride data-action names and the host's click listener below acts on
// them.
//
// THE FIELD MIRROR. No control carries a bound value (the gate refuses it),
// so every input, select and textarea writes what the yard types into the
// mirrors below on input and change, and updated() writes each mirror back
// to its field after a render. A re-render never eats a typed number, and a
// field the yard has not touched keeps the value the model gave it.
import { html, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// hh-sheet: the shared design-system chrome element.
import "@hardscapehouse/design-system/src/sheet.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
// The offline strip and the conflict cards ride the shell chrome package,
// wired here through their properties (the injected seams): this app maps the
// runner's state, the backend's ownership and the store's cards onto them.
import { fieldTabsForRole, setFieldFrame } from "@hardscapehouse/shell-chrome";
import { readDealerName } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { apiClientTransport } from "@hardscapehouse/offline-store";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { YardApi, type YardChange, type YardPick, type YardRecordLine } from "./yard-api.js";
import { YardOffline, bannerMode, openYardOfflineOpfs, type QueueAnswer } from "./yard-offline.js";
import type { Card, OutboxStall, Staleness } from "@hardscapehouse/offline-store";
import type { ShellSyncState } from "@hardscapehouse/shell-chrome";
import {
  RESOLVE_WALL_NAMES,
  CHANGE_ACK_WALL,
  ackDoneWords,
  actionDone,
  isLocalLine,
  isUnclaimed,
  lineIsShort,
  pickSummary,
  pickQueuedWords,
  qtyText,
  rereadHeldPicks,
  recordBlockedReason,
  shortOf,
  shortPickWords,
  validateStage,
  skidBlockedReason,
  skidWords,
  shortageSentWords,
  validateShortage,
  yardError,
  heldByOther,
  PICK_TAKEN_WORDS,
} from "./yard-logic.js";
import { yardStyles } from "./styles.js";
import {
  renderChanges,
  renderConflicts,
  renderEntry,
  renderPick,
  renderQueue,
  stalenessWords,
  type EntryView,
  type PickModel,
  type QueueModel,
} from "./views.js";

const DEMO_ORG = "hh-demo";

/** One drafted line: what the yard typed, mirrored from the field. */
interface Draft {
  qty: string;
  reason: string;
  note: string;
}

export class YardPickApp extends HHElement {
  static styles = [baseStyles, yardStyles];

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // demo routes. It is the injected AccountSurface for the auth package.
  readonly #demoAccount: AccountSurface = new DemoAccount({ client: this.#demoClient });

  // The custody the served config chose: in demo mode the auth controller over the demo account, as
  // always; in console mode this page recovers its own session on load (boot-custody.ts).
  readonly #custody = bootCustody(servedConsoleConfig(), { sessions: this.#sessions, demoAccount: this.#demoAccount, demoOrg: DEMO_ORG });

  // The auth controller: owns sign-in, sign-out, and adoption.
  readonly #auth: AuthController = this.#custody.controller;

  // The staff client: carries the mint's bearer after adoption.
  readonly #staffClient = new ApiClient({
    baseUrl: this.#custody.coreBaseUrl,
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => this.#custody.org }),
  });

  // The yard's calls, over the staff client.
  readonly #yard = new YardApi(this.#staffClient);

  // The offline data layer over the store, opened once the person is in. Null
  // means the host cannot carry a store: the app then behaves online as
  // today, over the direct api above, and the strip keeps quiet.
  #yardOffline: YardOffline | null = null;
  #offlineOpen: Promise<void> | null = null;
  #worldGen = 0;
  #readOnlyTab = false;
  #syncState = "offline";
  #waiting = 0;
  #stalls: OutboxStall[] = [];
  #openCards: Card[] = [];
  #queueStaleness: Staleness | null = null;

  // The board's state. The branch is the one this login reaches (the demo
  // yard team holds Trenton); the queue names it on every read because a
  // branch-scoped caller must.
  #phase: "loading" | "ready" | "error" = "loading";
  #picks: YardPick[] = [];
  #branchId = "";
  #branchName = "";
  #error = "";
  #notice = "";
  #busy = false;
  #moreBehind = false;
  #nextCursor = "";

  // The changes waiting on the yard: the list behind the landing's door, the
  // door's count, and the open one. The count is the last read's answer as a
  // word ("2", "50+" when a page came back full), null when nothing is known.
  #view: { kind: "queue" } | { kind: "pick"; id: string } | { kind: "changes" } | { kind: "conflicts" } = { kind: "queue" };
  #changes: YardChange[] = [];
  #changesPhase: "loading" | "ready" | "error" = "loading";
  #changesWaiting: string | null = null;
  #moreChangesBehind = false;
  #nextChangesCursor = "";
  // The open change's sheet: the row's facts, the optional note, and the
  // refusal's own line. A failed write keeps the sheet and says so in place.
  #sheetChange: YardChange | null = null;
  #changeNote = "";
  #changeError = "";
  #pick: YardPick | null = null;
  #drafts = new Map<number, Draft>();
  #editing: number | null = null;
  #sheetFor: number | null = null;
  #resolveFor = "";
  #skids: number | null = null;
  #skidSaved = false;
  readonly #queuedPickTasks = new Set<string>();
  readonly #sentPickTasks = new Set<string>();

  // The field mirrors: what the yard typed, keyed by field name (and line).
  #qtyMirror = new Map<number, string>();
  #reasonMirror = new Map<number, string>();
  #noteMirror = new Map<number, string>();
  #sheetQty = "";
  #sheetReason = "";
  #sheetNote = "";
  #skidReason = "";
  #skidNote = "";
  #stageWhere = "";
  #resolveText = "";

  // Cached blob URLs for product thumbnails on pick lines, keyed by product id.
  readonly #imageUrls = new Map<string, string>();
  readonly #imageInFlight = new Set<string>();

  // The way in: this app's own card, drawn by views.ts.
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

  // The role label the frame shows; set from the handoff or the mint.
  #roleLabel = "";

  // The role code the frame's tabs follow; set with the label from the handoff, the console entry or the mint.
  #roleCode = "";

  // The dealer's name for the frame's lockup, from the public tenant config; empty until it lands.
  #org = "";

  // The theme: light by default (the yard reads this in daylight), brand
  // (the dark half) one tap away. Held in memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  // The caller's own id, so the board can say "Claimed by you".
  #me = "";

  // The entry cards and every control ride data-action names; one host
  // listener reads them, as the till's does.
  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    const action = control?.getAttribute("data-action");
    if (action === "switch-role" || action === "sign-out") {
      this.#onSignOut();
      return;
    }
    if (action === "theme") {
      this.#theme = this.#theme === "light" ? "dark" : "light";
      this.requestUpdate();
      return;
    }
    if (action === "reload" || action === "more") {
      void this.#loadQueue(action !== "more");
      return;
    }
    if (action === "choose") {
      const flow = control?.getAttribute("data-flow") ?? "";
      if (flow !== "") void this.#chooseFlow(flow);
      return;
    }
    if (action === "open-changes") {
      this.#openChanges();
      return;
    }
    if (action === "more-changes") {
      void this.#loadChanges(false);
      return;
    }
    if (action === "open-change") {
      this.#openChange(control?.getAttribute("data-change-id") ?? "");
      return;
    }
    if (action === "close-change-sheet") {
      this.#sheetChange = null;
      this.#changeNote = "";
      this.#changeError = "";
      this.requestUpdate();
      return;
    }
    if (action === "ack-change") {
      void this.#ackChange();
      return;
    }
    if (action === "back") {
      this.#view = { kind: "queue" };
      this.#pick = null;
      this.#sheetChange = null;
      this.#changeNote = "";
      this.#changeError = "";
      this.#error = "";
      this.#notice = "";
      // Back re-reads the whole board: a pick that moved while it was open
      // must not ride beside its own stale row. The waiting changes' count
      // re-reads with it: a change answered elsewhere must not linger.
      void (this.#takePromise ?? Promise.resolve()).then(() => {
        void this.#loadQueue(true);
        void this.#loadChangesCount();
      });
      return;
    }
    if (action === "take-pick") {
      void this.#ensureClaimed();
      return;
    }
    if (action === "claim") {
      void this.#claim(control?.getAttribute("data-task-id") ?? "");
      return;
    }
    if (action === "open-pick") {
      void this.#openPick(control?.getAttribute("data-task-id") ?? "");
      return;
    }
    if (action === "start") {
      void this.#start(control?.getAttribute("data-task-id") ?? "");
      return;
    }
    if (action === "pull-line") {
      this.#pullLine(Number(control?.getAttribute("data-line-no") ?? "0"));
      return;
    }
    if (action === "edit-line") {
      this.#openEditor(Number(control?.getAttribute("data-line-no") ?? "0"));
      return;
    }
    if (action === "qty-step") {
      this.#stepQty(Number(control?.getAttribute("data-line-no") ?? "0"), Number(control?.getAttribute("data-step") ?? "1"));
      return;
    }
    if (action === "qty-full" || action === "qty-none") {
      const lineNo = Number(control?.getAttribute("data-line-no") ?? "0");
      const line = this.#ticketLine(lineNo);
      if (line !== null) this.#setQty(lineNo, action === "qty-full" ? qtyText(line.qty_ordered) : "0");
      return;
    }
    if (action === "qty-done") {
      this.#editing = null;
      this.requestUpdate();
      return;
    }
    if (action === "pick-reason") {
      const scope = control?.getAttribute("data-scope") ?? "";
      const reason = control?.getAttribute("data-reason") ?? "";
      if (scope === "line") {
        const lineNo = Number(control?.getAttribute("data-line-no") ?? "0");
        this.#reasonMirror.set(lineNo, reason);
        this.#draftFor(lineNo).reason = reason;
      } else if (scope === "sheet") {
        this.#sheetReason = reason;
      } else if (scope === "skid") {
        this.#skidReason = reason;
      }
      this.requestUpdate();
      return;
    }
    if (action === "save-pick") {
      void this.#savePick();
      return;
    }
    if (action === "open-sheet") {
      this.#openSheet(Number(control?.getAttribute("data-line-no") ?? "0"));
      return;
    }
    if (action === "close-sheet") {
      this.#sheetFor = null;
      this.#sheetQty = "";
      this.#sheetReason = "";
      this.#sheetNote = "";
      this.requestUpdate();
      return;
    }
    if (action === "send-shortage") {
      void this.#sendShortage();
      return;
    }
    if (action === "resolve") {
      this.#resolveFor = control?.getAttribute("data-shortage-id") ?? "";
      this.#resolveText = "";
      this.requestUpdate();
      return;
    }
    if (action === "cancel-resolve") {
      this.#resolveFor = "";
      this.requestUpdate();
      return;
    }
    if (action === "send-resolve") {
      void this.#sendResolve(control?.getAttribute("data-shortage-id") ?? "");
      return;
    }
    if (action === "skid-step") {
      this.#stepSkids(Number(control?.getAttribute("data-step") ?? "1"));
      return;
    }
    if (action === "save-skids") {
      void this.#saveSkids();
      return;
    }
    if (action === "pick-bay") {
      const bay = control?.getAttribute("data-bay") ?? "";
      if (bay !== "") {
        this.#stageWhere = bay;
        this.requestUpdate();
      }
      return;
    }
    if (action === "close-stage") {
      this.#stageWhere = "";
      this.requestUpdate();
      return;
    }
    if (action === "stage") {
      void this.#stage();
      return;
    }
    if (action === "open-conflicts") {
      this.#view = { kind: "conflicts" };
      this.#error = "";
      this.#notice = "";
      void this.#refreshOfflineChrome();
      return;
    }
    if (action === "dismiss-card") {
      const id = control?.getAttribute("data-card-id") ?? "";
      if (id !== "" && this.#yardOffline !== null) {
        void this.#yardOffline.dismissCard(id).then(() => this.#refreshOfflineChrome());
      }
      return;
    }
  };

  // The form fields: quantities, reasons, notes, the sheet, the stage spot.
  readonly #onField = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) return;
    const field = target.getAttribute("data-field");
    const lineNo = Number(target.getAttribute("data-line-no") ?? "0");
    if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement) {
      const value = target.value;
      if (field === "qty") {
        this.#qtyMirror.set(lineNo, value);
        this.#draftFor(lineNo).qty = value;
        this.requestUpdate();
        return;
      }
      if (field === "short-note") {
        this.#noteMirror.set(lineNo, value);
        this.#draftFor(lineNo).note = value;
        return;
      }
      if (field === "skid-note") {
        this.#skidNote = value;
        return;
      }
      if (field === "sheet-qty") {
        this.#sheetQty = value;
        // The SHORT tile reads live from what the yard types.
        this.requestUpdate();
        return;
      }
      if (field === "sheet-note") {
        this.#sheetNote = value;
        return;
      }
      if (field === "resolution") {
        this.#resolveText = value;
        return;
      }
      if (field === "change-note") {
        this.#changeNote = value;
        return;
      }
      if (field === "stage-where") {
        this.#stageWhere = value;
        return;
      }
      return;
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    void readDealerName().then((name) => {
      this.#org = name;
      this.requestUpdate();
    });
    this.addEventListener("click", this.#onHostClick);
    // input and change do not leave this element's own shadow root (change is
    // not composed), so the form listeners sit on the root itself.
    this.renderRoot.addEventListener("input", this.#onField);
    this.renderRoot.addEventListener("change", this.#onField);
    window.addEventListener("online", this.#onConnectivity);
    window.addEventListener("offline", this.#onConnectivity);
    // Some hosts deliver the platform's word late or not at all; a short
    // watcher repeats it, and the layer dispatches only on a change.
    this.#connectivityWatch = setInterval(this.#onConnectivity, 1_000);
    if (this.#custody.mode === "console") {
      this.#custody.begin("yard-pick", (outcome) => this.#onRecovered(outcome));
      return;
    }
    const handoff = readFrontDoorHandoff();
    if (handoff.kind === "ready") {
      const adopted = this.#auth.adopt({
        token: handoff.session.token,
        tokenExpiresAtMs: handoff.session.tokenExpiresAtMs,
        user: {
          userId: handoff.session.user.userId,
          email: handoff.session.user.email,
          name: handoff.session.user.name,
        },
      });
      if (adopted) {
        this.#roleLabel = handoff.session.roleName;
        this.#roleCode = handoff.session.roleCode;
        this.#me = handoff.session.user.userId;
        clearFrontDoorHandoff();
        void this.#loadWorld();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  // Console mode: this page recovered its own session, or could not. A role the matrix gives this
  // app lands on its screen; an unavailable page keeps the unavailable entry while the custody retries;
  // a session the front door must settle goes back there.
  #onRecovered(outcome: AppStart): void {
    if (outcome.kind === "ready") {
      this.#roleLabel = outcome.entry.roleLabel;
      this.#roleCode = outcome.entry.roleCode;
      this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      this.#me = outcome.entry.userId;
      void this.#loadWorld();
    } else if (outcome.kind === "front-door" && outcome.why === "ended") {
      this.#onSignOut();
      return;
    } else {
      this.#entryView = entryUnavailable();
      if (outcome.kind === "front-door") this.#custody.toFrontDoor();
    }
    this.requestUpdate();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("input", this.#onField);
    this.renderRoot.removeEventListener("change", this.#onField);
    window.removeEventListener("online", this.#onConnectivity);
    window.removeEventListener("offline", this.#onConnectivity);
    if (this.#connectivityWatch !== null) clearInterval(this.#connectivityWatch);
    super.disconnectedCallback();
  }

  // The platform's connectivity word, fed straight to the machine.
  #connectivityWatch: ReturnType<typeof setInterval> | null = null;
  readonly #onConnectivity = (): void => {
    if (this.#yardOffline !== null) void this.#yardOffline.setOnline(navigator.onLine);
  };

  // The strip's mode: a second tab says read only whether or not a layer
  // opened; otherwise the runner's state through the layer's ownership.
  #bannerModeNow(): string {
    if (this.#yardOffline === null) return this.#readOnlyTab ? "read-only" : "";
    return bannerMode(this.#syncState, this.#yardOffline.store.ownership());
  }

  // The offline layer's chrome: the outbox depth, the stalls, the open cards.
  async #refreshOfflineChrome(): Promise<void> {
    const layer = this.#yardOffline;
    if (layer === null) return;
    this.#waiting = await layer.waiting();
    this.#stalls = await layer.stalls();
    this.#openCards = await layer.openCards();
    this.requestUpdate();
  }

  // The role label and the theme are the header inputs the position gate
  // keeps out of a template (only data- and aria- attributes take a bound
  // value), so they are set on the element here, after each render. The same
  // pass writes every field mirror back to its control, skipping the one
  // the yard is typing into, so a re-render never eats a typed number.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    setFieldFrame(this.renderRoot.querySelector("hh-field-frame"), {
      org: this.#org,
      appName: "Yard Pick",
      surface: this.#surfaceWords(),
      roleLabel: this.#roleLabel,
      syncState: this.#syncState as ShellSyncState,
      theme: this.#theme,
      signedIn: this.#sessions.current !== null,
      demo: true,
      current: "pick",
      bellAction: "open-changes",
      bellCount: this.#changesWaiting !== null && /^\d+$/.test(this.#changesWaiting) ? Number(this.#changesWaiting) : 0,
      tabs: fieldTabsForRole(this.#roleCode),
    });
    // The offline strip and the card list take their state through properties
    // (the position gate refuses a bound value outside data- and aria-), the
    // same pass the header's label rides.
    const banner = this.renderRoot.querySelector("hh-shell-offline-banner") as (HTMLElement & { mode: string; waiting: number }) | null;
    if (banner !== null) {
      banner.mode = this.#bannerModeNow();
      banner.waiting = this.#waiting;
    }
    const cards = this.renderRoot.querySelector("hh-shell-conflict-list") as (HTMLElement & { cards: Card[] }) | null;
    if (cards !== null) cards.cards = this.#openCards;
    for (const field of Array.from(this.renderRoot.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>("[data-field]"))) {
      if (field === (document.activeElement as HTMLElement | null)) continue;
      const name = field.getAttribute("data-field") ?? "";
      const lineNo = Number(field.getAttribute("data-line-no") ?? "0");
      let value: string | null = null;
      if (name === "qty") value = this.#qtyMirror.get(lineNo) ?? "";
      else if (name === "short-note") value = this.#noteMirror.get(lineNo) ?? "";
      else if (name === "sheet-qty") value = this.#sheetQty;
      else if (name === "sheet-note") value = this.#sheetNote;
      else if (name === "skid-note") value = this.#skidNote;
      else if (name === "stage-where") value = this.#stageWhere;
      else if (name === "resolution") value = this.#resolveText;
      else if (name === "change-note") value = this.#changeNote;
      if (value !== null && field.value !== value) field.value = value;
    }
    // The change sheet takes the yard's attention when it opens: at phone
    // width it draws above a list longer than the screen, so a lower row's
    // tap or Enter must land on it, in view and in focus, not leave both on
    // the row behind. Focus already inside the sheet (the note, the buttons,
    // the heading this pass put there) is left alone.
    const sheet = this.renderRoot.querySelector<HTMLElement>("[data-change-sheet]");
    if (sheet !== null) {
      const active = this.renderRoot instanceof ShadowRoot ? this.renderRoot.activeElement : document.activeElement;
      if (!(active instanceof Node && sheet.contains(active))) {
        sheet.scrollIntoView({ block: "start" });
        const head = sheet.querySelector<HTMLElement>(".sheet-head");
        if (head !== null) head.focus();
      }
    }
    // Hydrate per-line product thumbnails over the authenticated staff client.
    void this.#hydrateProductImages();
  }

  /** Fetches each product image once over the staff client and writes a blob URL onto the line's img element. */
  async #hydrateProductImages(): Promise<void> {
    const imgs = Array.from(this.renderRoot.querySelectorAll<HTMLImageElement>("[data-line-img]"));
    for (const img of imgs) {
      const productId = img.getAttribute("data-product-id") ?? "";
      if (productId === "") continue;
      const cached = this.#imageUrls.get(productId);
      if (cached !== undefined) {
        if (img.src !== cached) img.src = cached;
        continue;
      }
      if (this.#imageInFlight.has(productId)) continue;
      this.#imageInFlight.add(productId);
      const res = await this.#yard.productImage(productId);
      this.#imageInFlight.delete(productId);
      if (!res.ok) continue;
      const bytes = res.value;
      const copy = new Uint8Array(bytes.byteLength);
      copy.set(bytes);
      const blob = new Blob([copy], { type: "image/png" });
      const url = URL.createObjectURL(blob);
      this.#imageUrls.set(productId, url);
      for (const match of Array.from(this.renderRoot.querySelectorAll<HTMLImageElement>("[data-line-img]"))) {
        if ((match.getAttribute("data-product-id") ?? "") === productId) {
          match.src = url;
        }
      }
    }
  }

  private async loadFlows(): Promise<void> {
    this.#entryView = { flows: { kind: "loading" }, busy: false, message: null };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).listFlows();
    if (result.ok) {
      this.#entryView = { flows: { kind: "ready", flows: result.value }, busy: false, message: null };
    } else {
      this.#entryView = { flows: { kind: "unavailable" }, busy: false, message: null };
    }
    this.requestUpdate();
  }

  // The offline door, opened once per sign in: the store over its OPFS
  // backend when the host can carry one, the first pull through the runner,
  // and the chrome fed from then on. A host that cannot carry a store leaves
  // the layer null and the app behaves online as today.
  async #bootOffline(gen: number): Promise<void> {
    if (this.#me === "") return;
    if (this.#offlineOpen === null) {
      this.#offlineOpen = (async () => {
        const opened = await openYardOfflineOpfs({
          dbName: "hh-yard-pick",
          transport: apiClientTransport(this.#staffClient),
          direct: this.#yard,
          actorId: this.#me,
          onChange: () => void this.#refreshOfflineChrome(),
        });
        // A second tab reads an empty cache: it keeps the online board and only
        // says it is read only.
        this.#readOnlyTab = opened.readOnly;
        const layer = opened.layer;
        if (layer === null) {
          this.requestUpdate();
          return;
        }
        this.#yardOffline = layer;
        layer.runner.onState((state) => {
          // The state is on the glass in this update, not behind the health read.
          this.#syncState = state;
          this.requestUpdate();
          void this.#refreshOfflineChrome();
          // A cycle came to rest with its pull landed: the open pick reads the
          // server's row (a record the device queued shows the server's lines,
          // not its own). The read runs even while a save or a shortage is still
          // finishing: a cycle that rests in between would otherwise leave the
          // device's queued lines on the glass after the server's row arrived.
          if (state === "idle" || state === "conflict") {
            if (this.#view.kind === "pick") void this.#refreshPick().then(() => this.requestUpdate());
            else if (this.#view.kind === "queue" && this.#branchId !== "") void this.#loadQueue(false);
          }
        });
      })();
    }
    await this.#offlineOpen;
    const layer = this.#yardOffline;
    if (layer === null || gen !== this.#worldGen || this.#sessions.current === null) return;
    await layer.setOnline(navigator.onLine);
    if (gen !== this.#worldGen || this.#sessions.current === null) return;
    await layer.cycle();
    if (gen !== this.#worldGen || this.#sessions.current === null) return;
    await this.#refreshOfflineChrome();
  }

  // The door the yard's calls go through: the offline layer when it opened,
  // the direct api when it did not. The shapes agree, so the screens and
  // their notices read the same on both sides.
  #door(): YardApi | YardOffline {
    return this.#yardOffline ?? this.#yard;
  }

  // The branch the yard works: the branches this login may reach, the first
  // one the board opens on. A login that reaches none is told so, in words.
  async #loadWorld(): Promise<void> {
    const gen = ++this.#worldGen;
    this.#phase = "loading";
    this.requestUpdate();
    await this.#bootOffline(gen);
    if (gen !== this.#worldGen || this.#sessions.current === null) return;
    const branches = await this.#yard.branches();
    if (gen !== this.#worldGen || this.#sessions.current === null) return;
    if (!branches.ok) {
      this.#phase = "error";
      this.#error = yardError(branches.error, branches.status, "The yard did not load. Check your signal and try again.");
      this.requestUpdate();
      return;
    }
    if (branches.value.length === 0) {
      this.#phase = "error";
      this.#error = "You are not set up on a yard. Ask your branch manager to link your login to a branch.";
      this.requestUpdate();
      return;
    }
    this.#branchId = branches.value[0]?.id ?? "";
    this.#branchName = branches.value[0]?.name ?? "";
    await this.#loadQueue(true);
    if (gen !== this.#worldGen || this.#sessions.current === null) return;
    void this.#loadChangesCount();
  }

  /** The landing door's count: one light read, the word the board shows ("2", "50+"). */
  async #loadChangesCount(): Promise<void> {
    const answer = await this.#yard.changeQueue("PENDING_YARD");
    this.#changesWaiting = answer.ok
      ? answer.value.nextCursor !== ""
        ? `${answer.value.changes.length}+`
        : String(answer.value.changes.length)
      : null;
    this.requestUpdate();
  }

  /** The changes view's list: one page at a time, oldest first, the server's order. */
  async #loadChanges(reset: boolean): Promise<void> {
    if (reset) this.#changesPhase = "loading";
    this.requestUpdate();
    const cursor = reset ? "" : this.#nextChangesCursor;
    const answer = await this.#yard.changeQueue("PENDING_YARD", cursor);
    if (answer.ok) {
      this.#changes = reset ? answer.value.changes : [...this.#changes, ...answer.value.changes];
      this.#nextChangesCursor = answer.value.nextCursor;
      this.#moreChangesBehind = answer.value.nextCursor !== "";
      this.#changesPhase = "ready";
      this.#changesWaiting = this.#moreChangesBehind ? `${this.#changes.length}+` : String(this.#changes.length);
      this.#error = "";
    } else {
      this.#changesPhase = this.#changes.length > 0 ? "ready" : "error";
      this.#error = yardError(answer.error, answer.status, "The changes did not load. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  #openChanges(): void {
    this.#view = { kind: "changes" };
    // The door opens on a clean list: no sheet, note or refusal left over
    // from whoever held the phone before this read.
    this.#sheetChange = null;
    this.#changeNote = "";
    this.#changeError = "";
    this.#error = "";
    this.#notice = "";
    void this.#loadChanges(true);
  }

  #openChange(changeId: string): void {
    const change = this.#changes.find((c) => c.id === changeId);
    if (change === undefined) return;
    this.#sheetChange = change;
    this.#changeNote = "";
    this.#changeError = "";
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
  }

  /** The yard's word on a change: acknowledge, with the optional note. A
   *  refusal keeps the sheet and shows the server's own sentence in place;
   *  a landed acknowledgement moves the row out of the yard's stage and
   *  names the next stage from the server's answer. */
  async #ackChange(): Promise<void> {
    const change = this.#sheetChange;
    if (change === null || this.#busy) return;
    this.#busy = true;
    this.#changeError = "";
    this.requestUpdate();
    const answer = await this.#yard.acknowledgeChange(change.id, this.#changeNote);
    this.#busy = false;
    if (answer.ok) {
      this.#notice = ackDoneWords(answer.value);
      this.#sheetChange = null;
      this.#changeNote = "";
      await this.#loadChanges(true);
    } else {
      this.#changeError = yardError(answer.error, answer.status, "The acknowledgement did not go through. Check your signal and try again.", {
        wallSentence: CHANGE_ACK_WALL,
      });
    }
    this.requestUpdate();
  }

  async #loadQueue(reset: boolean): Promise<void> {
    if (this.#branchId === "") {
      await this.#loadWorld();
      return;
    }
    if (reset) this.#phase = "loading";
    this.requestUpdate();
    if (this.#yardOffline !== null) {
      // The board reads answer from the store, with the scope's staleness.
      const answer: QueueAnswer = await this.#yardOffline.queue(this.#branchId);
      if (answer.ok) {
        this.#picks = answer.value.picks;
        this.#nextCursor = "";
        this.#moreBehind = false;
        this.#phase = "ready";
        this.#error = "";
        this.#queueStaleness = answer.staleness;
        this.requestUpdate();
        // The device's held word is not the last word: re-read each held card from the server, so a
        // hold cleared since the pull releases the card (see rereadHeldPicks).
        const base = this.#picks;
        const fresh = await rereadHeldPicks(base, (id) => this.#yard.pick(id));
        if (this.#picks === base && fresh.some((p, i) => p !== base[i])) this.#picks = fresh;
      } else {
        this.#phase = this.#picks.length > 0 ? "ready" : "error";
        this.#error = yardError(answer.error, answer.status, "The board did not load. Check your signal and try again.");
      }
      this.requestUpdate();
      return;
    }
    const cursor = reset ? "" : this.#nextCursor;
    const answer = await this.#yard.queue(this.#branchId, cursor);
    if (answer.ok) {
      this.#picks = reset ? answer.value.picks : [...this.#picks, ...answer.value.picks];
      this.#nextCursor = answer.value.nextCursor;
      this.#moreBehind = answer.value.nextCursor !== "";
      this.#phase = "ready";
      this.#error = "";
    } else {
      this.#phase = this.#picks.length > 0 ? "ready" : "error";
      this.#error = yardError(answer.error, answer.status, "The board did not load. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  // Re-read the open pick without blanking the screen: the server's record
  // wins, and a failed re-read keeps what is on the glass.
  async #refreshPick(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#pick === null) return;
    const taskId = this.#view.id;
    const answer = await this.#door().pick(taskId);
    if (answer.ok) {
      this.#pick = answer.value;
      if (this.#pick.picked.some(isLocalLine)) {
        this.#queuedPickTasks.add(taskId);
        this.#sentPickTasks.delete(taskId);
      } else if (this.#queuedPickTasks.has(taskId)) {
        this.#queuedPickTasks.delete(taskId);
        if (this.#pick.task.status !== "CANCELLED") {
          this.#sentPickTasks.add(taskId);
        }
      }
    }
    if (this.#yardOffline !== null) await this.#refreshOfflineChrome();
  }

  #ticketLine(lineNo: number): { line_no: number; product_id: string; sku: string; description: string; qty_ordered: string; uom: string } | null {
    const line = this.#pick?.ticket?.lines.find((l) => l.line_no === lineNo);
    return line === undefined ? null : { line_no: line.line_no, product_id: line.product_id, sku: line.sku, description: line.description, qty_ordered: line.qty_ordered, uom: line.uom };
  }

  #draftFor(lineNo: number): Draft {
    let draft = this.#drafts.get(lineNo);
    if (draft === undefined) {
      draft = { qty: "", reason: "", note: "" };
      this.#drafts.set(lineNo, draft);
    }
    return draft;
  }

  #pullLine(lineNo: number): void {
    const line = this.#ticketLine(lineNo);
    if (line === null) return;
    if (this.#refuseIfTaken()) return;
    if (this.#pick !== null && isUnclaimed(this.#pick.task.status)) {
      void this.#ensureClaimed();
    }
    const draft = this.#draftFor(lineNo);
    const recorded = this.#pick?.picked.find((p) => p.order_line_no === lineNo);
    const current = draft.qty !== "" ? draft.qty : (recorded !== undefined ? qtyText(recorded.qty_picked) : "");
    const isFull = current !== "" && !lineIsShort(line.qty_ordered, current);
    if (isFull) {
      this.#qtyMirror.set(lineNo, "");
      draft.qty = "";
    } else {
      const fullQty = qtyText(line.qty_ordered);
      this.#qtyMirror.set(lineNo, fullQty);
      draft.qty = fullQty;
      this.#reasonMirror.set(lineNo, "");
      this.#noteMirror.set(lineNo, "");
      draft.reason = "";
      draft.note = "";
      if (this.#editing === lineNo) this.#editing = null;
    }
    this.requestUpdate();
  }

  #openEditor(lineNo: number): void {
    if (lineNo <= 0 || this.#ticketLine(lineNo) === null) return;
    if (this.#refuseIfTaken()) return;
    if (this.#pick !== null && isUnclaimed(this.#pick.task.status)) {
      void this.#ensureClaimed();
    }
    this.#editing = lineNo;
    const draft = this.#draftFor(lineNo);
    if (!this.#qtyMirror.has(lineNo)) {
      const recorded = this.#pick?.picked.find((p) => p.order_line_no === lineNo);
      this.#qtyMirror.set(lineNo, recorded !== undefined ? qtyText(recorded.qty_picked) : "");
      draft.qty = this.#qtyMirror.get(lineNo) ?? "";
      if (recorded !== undefined) {
        this.#reasonMirror.set(lineNo, recorded.short_reason);
        this.#noteMirror.set(lineNo, recorded.short_note);
        draft.reason = recorded.short_reason;
        draft.note = recorded.short_note;
      }
    }
    this.requestUpdate();
  }

  #setQty(lineNo: number, value: string): void {
    this.#qtyMirror.set(lineNo, value);
    this.#draftFor(lineNo).qty = value;
    this.requestUpdate();
  }

  #stepQty(lineNo: number, step: number): void {
    const line = this.#ticketLine(lineNo);
    if (line === null) return;
    const current = Number((this.#qtyMirror.get(lineNo) ?? "").trim());
    const base = Number.isFinite(current) ? current : 0;
    const next = Math.max(0, Number((base + step).toFixed(4)));
    this.#setQty(lineNo, String(next));
  }

  #takePromise: Promise<void> | null = null;

  /** The pulled row says another person holds this pick: say so, and take nothing from this person. */
  #refuseIfTaken(): boolean {
    const pick = this.#pick;
    if (pick === null || !heldByOther(pick.task.claimed_by, this.#me, pick.task.status)) return false;
    this.#loseRace();
    return true;
  }

  /** The second person in a race: the draft checks, the pending edit and the optimistic notice go, and the screen says who won. */
  #loseRace(): void {
    this.#drafts = new Map();
    this.#qtyMirror = new Map();
    this.#reasonMirror = new Map();
    this.#noteMirror = new Map();
    this.#editing = null;
    this.#notice = "";
    this.#error = PICK_TAKEN_WORDS;
    this.requestUpdate();
  }

  async #ensureClaimed(): Promise<void> {
    if (this.#takePromise !== null) return this.#takePromise;
    if (this.#view.kind !== "pick" || this.#pick === null) return;
    const taskId = this.#view.id;
    const held = this.#pick.ticket?.held_from_picking === true;
    if (held || !isUnclaimed(this.#pick.task.status)) return;
    const run = (async () => {
      const claimed = this.#yardOffline !== null ? await this.#yardOffline.claim(taskId, false) : await this.#door().claim(taskId);
      if (!claimed.ok) {
        if (claimed.status === 409) {
          const reread = await this.#door().pick(taskId);
          if (reread.ok && this.#view.kind === "pick" && this.#view.id === taskId) this.#pick = reread.value;
          this.#loseRace();
          return;
        }
        this.#error = yardError(claimed.error, claimed.status, "That did not go through. Check your signal and try again.", {
          notFound: "That pick is no longer on the board.",
          taken: PICK_TAKEN_WORDS,
        });
        this.requestUpdate();
        return;
      }
      if (this.#yardOffline !== null) {
        // Online, the server answers the claim now; a refusal or a row that shows someone else means the race was lost.
        const refused = await this.#yardOffline.sendClaimNow(taskId);
        const seen = await this.#door().pick(taskId);
        const lost = refused || (seen.ok && heldByOther(seen.value.task.claimed_by, this.#me, seen.value.task.status));
        if (lost) {
          if (seen.ok && this.#view.kind === "pick" && this.#view.id === taskId) this.#pick = seen.value;
          await this.#refreshOfflineChrome();
          this.#loseRace();
          return;
        }
      }
      const started = await this.#door().start(taskId);
      if (!started.ok) {
        this.#error = yardError(started.error, started.status, "That did not go through. Check your signal and try again.");
        this.requestUpdate();
        return;
      }
      const reread = await this.#door().pick(taskId);
      if (reread.ok && this.#view.kind === "pick" && this.#view.id === taskId) {
        this.#pick = reread.value;
        this.#notice = actionDone("claim");
      }
      if (this.#yardOffline !== null) await this.#refreshOfflineChrome();
      this.requestUpdate();
    })();
    this.#takePromise = run;
    try {
      await run;
    } finally {
      if (this.#takePromise === run) this.#takePromise = null;
    }
  }

  async #claim(taskId: string): Promise<void> {
    if (taskId === "" || this.#busy) return;
    await this.#openPick(taskId);
  }

  async #openPick(taskId: string): Promise<void> {
    if (taskId === "" || this.#busy) return;
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const first = await this.#door().pick(taskId);
    if (!first.ok) {
      this.#busy = false;
      this.#error = yardError(first.error, first.status, "That pick did not open. Check your signal and try again.", {
        notFound: "That pick is no longer on the board.",
      });
      this.requestUpdate();
      return;
    }
    let current = first.value;
    let openNotice = "";
    const held = current.ticket?.held_from_picking === true;
    const mineOrUnclaimed = current.task.claimed_by === "" || current.task.claimed_by === this.#me;
    if (!held && current.task.status === "ACCEPTED" && mineOrUnclaimed) {
      const started = await this.#door().start(taskId);
      if (!started.ok) {
        this.#busy = false;
        this.#error = yardError(started.error, started.status, "That did not go through. Check your signal and try again.");
        this.requestUpdate();
        return;
      }
      const reread = await this.#door().pick(taskId);
      if (reread.ok) current = reread.value;
      openNotice = actionDone("start");
    }
    this.#busy = false;
    this.#pick = current;
    if (current.picked.some(isLocalLine)) {
      this.#queuedPickTasks.add(taskId);
      this.#sentPickTasks.delete(taskId);
    }
    this.#view = { kind: "pick", id: taskId };
    this.#notice = openNotice;
    this.#drafts = new Map();
    this.#qtyMirror = new Map();
    this.#reasonMirror = new Map();
    this.#noteMirror = new Map();
    this.#editing = null;
    this.#sheetFor = null;
    this.#resolveFor = "";
    this.#skids = null;
    this.#skidSaved = false;
    this.#skidReason = "";
    this.#skidNote = "";
    this.#stageWhere = "";
    if (this.#yardOffline !== null) await this.#refreshOfflineChrome();
    this.requestUpdate();
  }

  async #start(taskId: string): Promise<void> {
    if (taskId === "" || this.#busy) return;
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const answer = await this.#door().start(taskId);
    this.#busy = false;
    if (answer.ok) {
      this.#notice = actionDone("start");
      await this.#refreshPick();
    } else {
      this.#error = yardError(answer.error, answer.status, "That did not go through. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  #draftedLines(): YardRecordLine[] {
    const ticket = this.#pick?.ticket;
    if (ticket === null || ticket === undefined) return [];
    const out: YardRecordLine[] = [];
    for (const line of ticket.lines) {
      const draft = this.#drafts.get(line.line_no);
      if (draft === undefined || draft.qty.trim() === "") continue;
      const short = lineIsShort(line.qty_ordered, draft.qty.trim());
      out.push({
        order_line_no: line.line_no,
        product_id: line.product_id,
        sku: line.sku,
        description: line.description,
        qty_ordered: line.qty_ordered,
        qty_picked: draft.qty.trim(),
        uom: line.uom,
        short_reason: short ? draft.reason : "",
        short_note: short ? draft.note.trim() : "",
      });
    }
    return out;
  }

  async #savePick(): Promise<void> {
    if (this.#takePromise !== null) await this.#takePromise;
    if (this.#view.kind !== "pick" || this.#busy) return;
    const lines = this.#draftedLines();
    if (lines.length === 0) {
      this.#error = "Say what was picked first: open a line and type how many came off the shelf.";
      this.requestUpdate();
      return;
    }
    const blocked = recordBlockedReason(lines);
    if (blocked !== "") {
      this.#error = blocked;
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const taskId = this.#view.id;
    const answer = await this.#door().recordLines(taskId, lines);
    // Connected: send now, so the picker reads the server's answer (its
    // refusal in its own words, the backorder figure) as they always have. No
    // signal: the record waits in the outbox and the notice says so.
    const layer = this.#yardOffline;
    let refused = "";
    if (answer.ok && layer !== null && layer.runner.state !== "offline") refused = await layer.sendRecordNow(taskId);
    this.#busy = false;
    if (answer.ok && refused === "") {
      await this.#refreshPick();
      const firstShort = answer.value.find((p) => lineIsShort(p.qty_ordered, p.qty_picked));
      if (layer !== null && (await layer.pendingRecord(taskId))) {
        // Not sent: no signal, or the server could not be reached. Nobody has been told yet.
        this.#queuedPickTasks.add(taskId);
        this.#sentPickTasks.delete(taskId);
        this.#notice = pickQueuedWords(answer.value);
      } else if (firstShort !== undefined) {
        this.#queuedPickTasks.delete(taskId);
        this.#sentPickTasks.delete(taskId);
        const ticketLine = this.#pick?.ticket?.lines.find((l) => l.line_no === firstShort.order_line_no);
        this.#notice = shortPickWords(firstShort, ticketLine?.qty_backordered ?? "");
      } else {
        this.#queuedPickTasks.delete(taskId);
        this.#sentPickTasks.delete(taskId);
        this.#notice = pickSummary(this.#pick?.picked ?? answer.value) || "The pick is recorded.";
      }
    } else {
      this.#error = yardError(answer.ok ? refused : answer.error, answer.ok ? 400 : answer.status, "The pick did not save. Check your signal and try again.");
      await this.#refreshPick();
    }
    this.requestUpdate();
  }

  #openSheet(statedLineNo: number): void {
    const ticket = this.#pick?.ticket;
    if (ticket === null || ticket === undefined || ticket.lines.length === 0) return;
    let lineNo = statedLineNo;
    if (lineNo <= 0) {
      const shortDrafted = ticket.lines.find((l) => {
        const draft = this.#drafts.get(l.line_no);
        return draft !== undefined && draft.qty.trim() !== "" && lineIsShort(l.qty_ordered, draft.qty.trim());
      });
      lineNo = (shortDrafted ?? ticket.lines[0]).line_no;
    }
    this.#sheetFor = lineNo;
    // The board's SHORT tile starts from what the pull already measured: the
    // ordered less what was pulled, when the line has a number; empty when it
    // does not, never a confident guess.
    const line = ticket.lines.find((l) => l.line_no === lineNo);
    const pulled = line === undefined ? "" : (this.#drafts.get(lineNo)?.qty.trim() ?? this.#pick?.picked.find((p) => p.order_line_no === lineNo)?.qty_picked ?? "");
    this.#sheetQty = line === undefined || pulled === "" ? "" : shortOf(line.qty_ordered, pulled);
    this.#sheetReason = "";
    this.#sheetNote = "";
    this.#error = "";
    this.requestUpdate();
  }

  async #sendShortage(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#sheetFor === null || this.#busy) return;
    const line = this.#ticketLine(this.#sheetFor);
    if (line === null) return;
    const qtyBad = validateShortage(this.#sheetQty);
    if (qtyBad !== "") {
      this.#error = qtyBad;
      this.requestUpdate();
      return;
    }
    if (this.#sheetReason === "") {
      this.#error = "Give the reason. The counter has to tell the customer why.";
      this.requestUpdate();
      return;
    }
    if (this.#sheetReason === "OTHER" && this.#sheetNote.trim() === "") {
      this.#error = "Say what happened in the note when the reason is Other.";
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const answer = await this.#door().reportShortage(this.#view.id, {
      orderLineNo: line.line_no,
      description: line.description,
      qtyShort: this.#sheetQty.trim(),
      uom: line.uom,
      note: this.#sheetNote.trim(),
    });
    this.#busy = false;
    if (answer.ok) {
      this.#sheetFor = null;
      this.#sheetQty = "";
      this.#sheetReason = "";
      this.#sheetNote = "";
      this.#notice = shortageSentWords(answer.value);
      await this.#refreshPick();
    } else {
      this.#error = yardError(answer.error, answer.status, "The shortage did not send. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  async #sendResolve(shortageId: string): Promise<void> {
    if (this.#view.kind !== "pick" || shortageId === "" || this.#busy) return;
    const resolution = this.#resolveText.trim();
    if (resolution === "") {
      this.#error = "Say how it was settled. The counter reads this back to the customer.";
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const answer = await this.#door().resolveShortage(this.#view.id, shortageId, resolution);
    this.#busy = false;
    if (answer.ok) {
      this.#resolveFor = "";
      this.#resolveText = "";
      this.#notice = `Shortage cleared: ${answer.value.resolution}`;
      await this.#refreshPick();
    } else {
      // The resolve door is the one list wall this app meets: a caller off the
      // shortage resolvers list is answered with the wall's own words.
      this.#error = yardError(answer.error, answer.status, "That did not go through. Check your signal and try again.", { wall: RESOLVE_WALL_NAMES });
    }
    this.requestUpdate();
  }

  #stepSkids(step: number): void {
    const base = this.#skids ?? 0;
    this.#skids = Math.max(0, base + step);
    this.#skidSaved = false;
    this.requestUpdate();
  }

  async #saveSkids(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#busy) return;
    const blocked = skidBlockedReason(this.#skids, this.#skidReason, this.#skidNote);
    if (blocked !== "") {
      this.#error = blocked;
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const answer = await this.#door().recordSkids(this.#view.id, this.#skids ?? 0, this.#skidReason, this.#skidNote.trim());
    this.#busy = false;
    if (answer.ok) {
      this.#skids = answer.value.skid_count;
      this.#skidSaved = true;
      this.#notice = skidWords(answer.value);
    } else {
      this.#error = yardError(answer.error, answer.status, "The skid count did not save. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  async #stage(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#busy) return;
    const locationBad = validateStage(this.#stageWhere);
    if (locationBad !== "") {
      this.#error = locationBad;
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const answer = await this.#door().stage(this.#view.id, this.#stageWhere.trim(), "", "");
    this.#busy = false;
    if (answer.ok) {
      this.#stageWhere = "";
      this.#notice = actionDone("stage", { location: answer.value.stage_location, ref: answer.value.stage_ref });
      await this.#refreshPick();
    } else {
      this.#error = yardError(answer.error, answer.status, "The pick did not stage. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  async #chooseFlow(flow: string): Promise<void> {
    this.#entryView = { ...this.#entryView, busy: true };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).chooseFlow(flow);
    if (result.ok) {
      const adopted = this.#auth.adopt(result.value);
      if (adopted) {
        this.#roleLabel = result.value.identity.roleName;
        this.#roleCode = result.value.roleCode;
        this.#me = result.value.user.userId;
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
        void this.#loadWorld();
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Sign-in did not go through. Try the role again." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Sign-in did not go through. ${result.error}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    // The frame's account chip names no role once the person is out.
    this.#roleLabel = "";
    this.#roleCode = "";
    this.#worldGen++;
    this.#sessions.clear();
    this.#phase = "loading";
    this.#picks = [];
    this.#branchId = "";
    this.#branchName = "";
    this.#error = "";
    this.#notice = "";
    this.#view = { kind: "queue" };
    this.#pick = null;
    this.#changes = [];
    this.#changesPhase = "loading";
    this.#changesWaiting = null;
    this.#moreChangesBehind = false;
    this.#nextChangesCursor = "";
    // The shared phone changes hands here: the open sheet, its typed note and
    // its refusal line belong to the person who left, never to the next one.
    this.#sheetChange = null;
    this.#changeNote = "";
    this.#changeError = "";
    clearFrontDoorHandoff();
    if (this.#custody.mode === "console") {
      this.#entryView = entryUnavailable();
      this.requestUpdate();
      void this.#custody.leave();
      return;
    }
    void this.loadFlows();
  }

  // The surface the frame's chip names beside the app.
  #surfaceWords(): string {
    if (this.#view.kind === "pick") return "Pick";
    if (this.#view.kind === "changes") return "Changes waiting";
    if (this.#view.kind === "conflicts") return "Review";
    return "Pick queue";
  }

  // The doors the old header carried, now the first row of the board: the "Changes waiting on the yard" door with its
  // badge, the Review door, and Refresh. Theme, Switch role and Sign out live in the frame's account menu.
  private renderHeaderControls(): TemplateResult {
    const waiting = this.#changesWaiting;
    const conflicts = this.#openCards.length;
    return html`
      <div class="header-extra">
        <button type="button" class="btn btn-secondary header-door" data-action="open-changes" data-changes-door data-tap>
          <span>Changes waiting on the yard</span>
          ${waiting !== null ? html`<span class="pill branch-pill" data-changes-count>${waiting}</span>` : nothing}
        </button>
        <button type="button" class="btn btn-secondary theme-button" data-action="open-conflicts" data-tap>
          ${conflicts > 0 ? `Review ${conflicts}` : "Review"}
        </button>
        <button type="button" class="btn btn-secondary theme-button" data-action="reload" data-tap>Refresh</button>
      </div>
    `;
  }

  private renderWork(): TemplateResult {
    if (this.#view.kind === "conflicts") {
      return renderConflicts({ cards: this.#openCards.length });
    }
    if (this.#view.kind === "pick" && this.#pick !== null) {
      const drafts: Array<{ order_line_no: number; qty_picked: string; short_reason: string; short_note: string }> = [];
      for (const [lineNo, draft] of this.#drafts) {
        drafts.push({
          order_line_no: lineNo,
          qty_picked: draft.qty,
          short_reason: draft.reason,
          short_note: draft.note,
        });
      }
      const detail: PickModel = {
        pick: this.#pick,
        me: this.#me,
        drafts,
        editing: this.#editing,
        sheetFor: this.#sheetFor,
        sheetQty: this.#sheetQty,
        sheetReason: this.#sheetReason,
        resolveFor: this.#resolveFor,
        busy: this.#busy,
        error: this.#error,
        notice: this.#notice,
        skids: this.#skids,
        skidReason: this.#skidReason,
        skidSaved: this.#skidSaved,
        recordSent: this.#sentPickTasks.has(this.#pick.task.id),
      };
      return renderPick(detail);
    }
    if (this.#view.kind === "changes") {
      return renderChanges({
        phase: this.#changesPhase,
        changes: this.#changes,
        moreBehind: this.#moreChangesBehind,
        busy: this.#busy,
        error: this.#error,
        notice: this.#notice,
        sheet: this.#sheetChange,
        sheetNote: this.#changeNote,
        sheetError: this.#changeError,
      });
    }
    const isOffline = this.#bannerModeNow() === "offline";
    const model: QueueModel = {
      phase: this.#phase,
      branchName: this.#branchName,
      picks: this.#picks,
      me: this.#me,
      error: this.#error,
      notice: this.#notice,
      moreBehind: this.#moreBehind,
      changesWaiting: this.#changesWaiting,
      staleness:
        this.#yardOffline !== null
          ? stalenessWords(this.#queueStaleness ?? { neverPulled: true, ageMs: null, lastPullAt: null }, isOffline)
          : "",
      conflicts: this.#openCards.length,
    };
    return renderQueue(model);
  }

  // The stall strip: what the outbox's health says is stuck, in the store's
  // own words. One attempt on a write is not a stall; a stall shows at once.
  private renderStallStrip(): TemplateResult | typeof nothing {
    if (this.#stalls.length === 0) return nothing;
    return html`
      <div class="stall-strip" role="status" data-stall-strip>
        ${this.#stalls.map((stall) => html`<p data-stall data-stall-kind=${stall.kind}>${stall.message}</p>`)}
      </div>
    `;
  }

  private renderSignedIn(): TemplateResult {
    const mode = this.#bannerModeNow();
    return html`
      <hh-field-frame></hh-field-frame>
      ${mode !== "" ? html`<hh-shell-offline-banner data-offline-banner></hh-shell-offline-banner>` : nothing}
      ${this.renderStallStrip()}
      <main>
        ${this.renderHeaderControls()}
        ${this.renderWork()}
      </main>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hh-field-frame></hh-field-frame>
      <main>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("yard-pick-app", YardPickApp);

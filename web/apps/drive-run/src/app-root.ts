// HH Drive's root element: the chrome and the way in. The way in is the
// demo's role cards (the shared kit's entry, the demo adapter behind the auth
// package's AccountSurface seam). After the mint the branch resolves once, the
// ports are wired with the caller's headers, and the work area renders.
//
// The organisation header is the demo org by construction: the staff header
// provider refuses to send a request without X-Org, so no call ever leaves
// this app unresolved.
//
// The header is fed through plain attributes (signed-in, demo) and, for the
// role label, an assignment in updated(): the design system's position gate
// refuses every "." and "@" binding and every bound value outside the data-
// and aria- attributes in a dependent package. Its two buttons ride data-action names
// and the host's click listener below acts on them.
import { html, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, entryAfter, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
// The door stage (UX-143): the door line sits on the shared left-aligned stage. Its own sheet
// keeps its distance: doorStageStyles carries the .stage rule alone, no entryStyles card classes.
import { doorStageStyles } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { bandIdentity } from "./band-identity.js";
import { houseDay, houseZone, serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
// The offline strip and the conflict cards ride the shell chrome package,
// wired here through their properties (the injected seams): this app maps the
// runner's state, the backend's ownership and the store's cards onto them.
import { fieldTabsForRole, setFieldFrame, type ShellSyncState } from "@hardscapehouse/shell-chrome";
import { readDealerName } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { apiClientTransport } from "@hardscapehouse/offline-store";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { DriveApi, type DriverRun, type DriverStop } from "./drive-api.js";
import { DriveOffline, bannerMode, openDriveOfflineOpfs, type RunsAnswer } from "./drive-offline.js";
import { HANDBACK_TITLE, PROOF_QUEUED_NOTICE, canRecordFailure, canSign, normalizeName, resolveFailureReason, roadsideError } from "./drive-logic.js";
import { driveStyles } from "./styles.js";
import { readyPhoto } from "./photo.js";
import {
  renderClose,
  renderConflicts,
  renderEntry,
  renderFail,
  renderPod,
  renderRunSheet,
  renderStopDetail,
  stalenessWords,
  type EntryView,
} from "./views.js";

import type { Card, OutboxStall, Staleness } from "@hardscapehouse/offline-store";

const DEMO_ORG = "hh-demo";

export class DriveRunApp extends HHElement {
  static styles = [baseStyles, driveStyles, doorStageStyles];

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // two demo routes. It is the injected AccountSurface for the auth package.
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

  // The driver's calls, over the staff client.
  readonly #drive = new DriveApi(this.#staffClient);

  // The offline data layer over the store, opened once the person is in. Null
  // means the host cannot carry a store: the app then behaves online as
  // today, over the direct api above, and the strip keeps quiet.
  #driveOffline: DriveOffline | null = null;
  #offlineBooted = false;
  #readOnlyTab = false;
  #syncState = "offline";
  #waiting = 0;
  #stalls: OutboxStall[] = [];
  #openCards: Card[] = [];
  #runStaleness: Staleness | null = null;

  // The run sheet's state. The runs are the server's answer for the dealer's
  // day; the view is which screen shows.
  #phase: "loading" | "ready" | "error" = "loading";
  #runs: DriverRun[] = [];
  #error = "";
  #notice = "";
  #busy = false;
  #view:
    | { kind: "run" }
    | { kind: "stop"; id: string }
    | { kind: "pod"; id: string }
    | { kind: "fail"; id: string }
    | { kind: "close" }
    | { kind: "conflicts" } = { kind: "run" };

  // Proof of delivery's draft. The name field is not bound (the position
  // rules allow no value binding), so these mirror it from the input events.
  #podName = "";
  #podAffirmed = false;
  #podBusy = false;
  #podPrefill: string | null = null;
  #photoBusy = false;
  // Pictures chosen while one is still going up wait here, in order, each with
  // the stop it was taken for; none is ever dropped.
  readonly #photoQueue: Array<{ file: File; stopId: string; late: boolean }> = [];
  // The pictures taken in this visit, by stop, kept in memory for the
  // thumbnails: the core's photo route needs the bearer, so a picture cannot
  // load as an image address, and one taken before this visit shows as a tile.
  readonly #thumbs = new Map<string, string[]>();

  // Could not deliver's draft: the chosen preset, the Other note, and the resolved reason.
  #failPreset = "";
  #failOtherText = "";
  #failReason = "";
  #closing = false;

  // Sheet focus management: which opener button launched the sheet, and which
  // focus transition should run after the next render.
  #sheetOpenerAction: "open-pod" | "open-fail" | null = null;
  #sheetFocusPending: "dialog" | "opener" | null = null;

  // The way in: this app's own card, drawn by views.ts.
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The role code the frame's tabs follow; set with the label from the handoff, the console entry or the mint.
  #roleCode = "";

  // The dealer's name for the frame's lockup, from the public tenant config; empty until it lands.
  #org = "";

  // The theme: light by default (a driver reads this in daylight), brand
  // (the dark half) one tap away. Held in memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  // The entry cards carry data-action="choose" and data-flow; one host
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
    if (action === "open-stop") {
      this.#openStop(control?.getAttribute("data-stop-id") ?? "");
      return;
    }
    if (action === "back") {
      this.#view = { kind: "run" };
      this.#error = "";
      this.#notice = "";
      this.requestUpdate();
      return;
    }
    if (action === "reload") {
      void this.#loadRuns();
      return;
    }
    if (action === "open-pod") {
      this.#openPod();
      return;
    }
    if (action === "back-to-stop") {
      this.#closeSheet();
      return;
    }
    if (action === "open-fail") {
      if (this.#view.kind === "stop") {
        this.#failPreset = "";
        this.#failOtherText = "";
        this.#failReason = "";
        this.#error = "";
        this.#notice = "";
        this.#sheetOpenerAction = "open-fail";
        this.#sheetFocusPending = "dialog";
        this.#view = { kind: "fail", id: this.#view.id };
        this.requestUpdate();
      }
      return;
    }
    if (action === "confirm-fail") {
      void this.#recordFailure();
      return;
    }
    if (action === "open-close") {
      this.#view = { kind: "close" };
      this.#error = "";
      this.#notice = "";
      this.requestUpdate();
      return;
    }
    if (action === "complete-run") {
      void this.#completeRun(control?.getAttribute("data-run-id") ?? "");
      return;
    }
    if (action === "sign") {
      void this.#sign();
      return;
    }
    if (action === "advance") {
      const to = control?.getAttribute("data-status");
      if (to === "LOADED" || to === "EN_ROUTE") void this.#advance(to);
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
      if (id !== "" && this.#driveOffline !== null) {
        void this.#driveOffline.dismissCard(id).then(() => this.#refreshOfflineChrome());
      }
      return;
    }
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  // The form fields: the typed name, the recipient's confirmation, the photo, and the failure reason.
  readonly #onField = (event: Event): void => {
    const target = event.target;
    if (target instanceof HTMLTextAreaElement && target.getAttribute("data-field") === "reason") {
      this.#failOtherText = target.value;
      this.#failReason = resolveFailureReason(this.#failPreset, this.#failOtherText);
      this.requestUpdate();
      return;
    }
    if (!(target instanceof HTMLInputElement)) return;
    const field = target.getAttribute("data-field");
    if (field === "preset-reason") {
      const preset = target.getAttribute("data-reason") ?? "";
      this.#failPreset = preset;
      this.#failReason = resolveFailureReason(preset, this.#failOtherText);
      this.requestUpdate();
    } else if (field === "name") {
      this.#podName = target.value;
      this.requestUpdate();
    } else if (field === "affirm") {
      this.#podAffirmed = target.checked;
      this.requestUpdate();
    } else if (field === "photo" && event.type === "change") {
      const file = target.files?.[0];
      target.value = "";
      if (file !== undefined) void this.#sendPhoto(file);
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    void readDealerName().then((name) => {
      this.#org = name;
      this.requestUpdate();
    });
    this.addEventListener("click", this.#onHostClick);
    window.addEventListener("keydown", this.#onKeyDown);
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
      this.#custody.begin("drive-run", (outcome) => this.#onRecovered(outcome));
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
        clearFrontDoorHandoff();
        void this.#loadRuns();
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
      void this.#loadRuns();
    } else if (outcome.kind === "front-door" && outcome.why === "ended") {
      this.#onSignOut();
      return;
    } else {
      this.#entryView = entryAfter(outcome);
      if (outcome.kind === "front-door") this.#custody.toFrontDoor();
    }
    this.requestUpdate();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    window.removeEventListener("keydown", this.#onKeyDown);
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
    if (this.#driveOffline !== null) void this.#driveOffline.setOnline(navigator.onLine);
  };

  // The strip's mode: a second tab says read only whether or not a layer
  // opened; otherwise the runner's state through the layer's ownership.
  #bannerModeNow(): string {
    if (this.#driveOffline === null) return this.#readOnlyTab ? "read-only" : "";
    return bannerMode(this.#syncState, this.#driveOffline.store.ownership());
  }

  // The offline layer's chrome: the outbox depth, the stalls, the open cards.
  async #refreshOfflineChrome(): Promise<void> {
    const layer = this.#driveOffline;
    if (layer === null) return;
    this.#waiting = await layer.waiting();
    this.#stalls = await layer.stalls();
    this.#openCards = await layer.openCards();
    this.requestUpdate();
  }

  // The offline door, opened once per sign in: the store over its OPFS
  // backend when the host can carry one, the first pull through the runner,
  // and the chrome fed from then on.
  async #bootOffline(): Promise<void> {
    if (this.#offlineBooted) return;
    this.#offlineBooted = true;
    const opened = await openDriveOfflineOpfs({
      dbName: "hh-drive-run",
      transport: apiClientTransport(this.#staffClient),
      direct: this.#drive,
      onChange: () => void this.#refreshOfflineChrome(),
    });
    // A second tab reads an empty cache: it keeps the online sheet and only
    // says it is read only.
    this.#readOnlyTab = opened.readOnly;
    const layer = opened.layer;
    if (layer === null) {
      this.requestUpdate();
      return;
    }
    this.#driveOffline = layer;
    layer.runner.onState((state) => {
      // The state is on the glass in this update, not behind the health read.
      this.#syncState = state;
      this.requestUpdate();
      void this.#refreshOfflineChrome();
      // A cycle that came to rest has pulled the server's truth into the
      // store: the sheet re-reads, so a row the server moved shows moved.
      if (state === "idle" || state === "conflict") void this.#refreshRuns();
    });
    await layer.setOnline(navigator.onLine);
    await layer.cycle();
    await this.#refreshOfflineChrome();
  }

  // The door the driver's stop moves and proofs go through: the offline layer
  // when it opened, the direct api when it did not. The photos and the run
  // close keep their direct doors (the attachments queue is item 6's other
  // half, and the manifest gives the runs scope no writable policy); the
  // layer's close waits for the outbox first.
  #door(): DriveApi | DriveOffline {
    return this.#driveOffline ?? this.#drive;
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const identity = bandIdentity(servedConsoleConfig(), this.#org);
    setFieldFrame(this.renderRoot.querySelector("hh-field-frame"), {
      org: identity.org,
      appName: "Drive",
      surface: this.#surfaceWords(),
      roleLabel: this.#roleLabel,
      syncState: this.#syncState as ShellSyncState,
      theme: this.#theme,
      signedIn: this.#sessions.current !== null,
      demo: identity.demo,
      current: "drive",
      bellAction: "open-conflicts",
      bellCount: this.#openCards.length,
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
    // The name field's first value, set once when proof of delivery opens.
    if (this.#podPrefill !== null) {
      const field = this.renderRoot.querySelector<HTMLInputElement>("input[data-field=name]");
      if (field !== null) {
        field.value = this.#podPrefill;
        this.#podPrefill = null;
      }
    }
    // The thumbnails: an object address cannot ride a template binding (the
    // gate admits http, https and relative only), so each picture takes its
    // address here; a tile with none shows its words alone.
    for (const img of Array.from(this.renderRoot.querySelectorAll<HTMLImageElement>("img[data-thumb]"))) {
      const [stopId = "", index = "0"] = (img.getAttribute("data-thumb") ?? "").split(":");
      const url = this.#thumbs.get(stopId)?.[Number(index)];
      if (url !== undefined) {
        if (img.getAttribute("src") !== url) img.setAttribute("src", url);
        img.hidden = false;
      } else {
        img.hidden = true;
      }
    }
    // A telephone address cannot ride a template binding (the position gate
    // admits only http, https and relative addresses), so the call links take
    // their href here, from a value that is digits and phone punctuation only.
    for (const link of Array.from(this.renderRoot.querySelectorAll("a[data-tel]"))) {
      const number = link.getAttribute("data-tel") ?? "";
      if (/^[0-9+().\-\sx]+$/.test(number)) link.setAttribute("href", `tel:${number.replace(/\s+/g, "")}`);
    }
    for (const radio of Array.from(this.renderRoot.querySelectorAll<HTMLInputElement>('input[data-field="preset-reason"]'))) {
      radio.checked = radio.getAttribute("data-reason") === this.#failPreset;
    }
    const reasonField = this.renderRoot.querySelector<HTMLTextAreaElement>('textarea[data-field="reason"]');
    if (reasonField !== null && reasonField.value !== this.#failOtherText) {
      reasonField.value = this.#failOtherText;
    }
    const sheetOpen = this.#view.kind === "pod" || this.#view.kind === "fail";
    const pageBehind = this.renderRoot.querySelector<HTMLElement>("[data-page-behind]");
    if (pageBehind !== null) pageBehind.inert = sheetOpen;
    const frame = this.renderRoot.querySelector<HTMLElement>("hh-field-frame");
    if (frame !== null) frame.inert = sheetOpen;
    if (this.#sheetFocusPending === "dialog" && sheetOpen) {
      this.#sheetFocusPending = null;
      const firstField = this.renderRoot.querySelector<HTMLElement>(
        '[role="dialog"] textarea:not([disabled]), [role="dialog"] input[type="text"]:not([disabled]), [role="dialog"] input[type="radio"]:not([disabled]), [role="dialog"] button:not([disabled])',
      );
      firstField?.focus();
    } else if (this.#sheetFocusPending === "opener" && !sheetOpen) {
      const action = this.#sheetOpenerAction;
      this.#sheetFocusPending = null;
      this.#sheetOpenerAction = null;
      if (action !== null) {
        const opener = this.renderRoot.querySelector<HTMLElement>(`button[data-action="${action}"]`);
        opener?.focus();
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
      this.#entryView = { flows: { kind: "unavailable", failure: { status: result.status, code: result.code } }, busy: false, message: null };
    }
    this.requestUpdate();
  }

  // The dealer's own day names the run to ask for, never the browser's UTC day.
  async #loadRuns(): Promise<void> {
    this.#phase = "loading";
    this.requestUpdate();
    await this.#bootOffline();
    if (this.#driveOffline !== null) {
      // A cold start with no signal reads the store on the zone the last
      // online session served, never on the Ontario default: the stored zone
      // is restored before the day is named. A config read that fails below
      // never serves anything, so it cannot overwrite the restored zone.
      const storedZone = await this.#driveOffline.readZone();
      if (storedZone !== null) serveHouseZoneFrom({ business_zone: storedZone });
      // The store answers at once on the last served zone (the default until one is served);
      // the zone is refreshed on the side and never holds the run sheet.
      const day = houseDay();
      const zoneDay = this.#drive.servedDealersDay();
      // The run sheet reads answer from the store, with the scope's staleness.
      const answer: RunsAnswer = await this.#driveOffline.runs(day);
      if (answer.ok) {
        this.#runs = answer.value.runs;
        this.#phase = "ready";
        this.#error = "";
        this.#runStaleness = answer.staleness;
      } else {
        this.#runs = [];
        this.#phase = "error";
        this.#error = roadsideError(answer.error, answer, "Your run did not load.", {
          notFound: "You are not set up as a driver. Ask your branch manager to link your login.",
        });
      }
      this.requestUpdate();
      void zoneDay.then(({ day: servedDay, served }) => {
        // A read that served persists the zone (a dealer's zone change is
        // stored at the next read); a failed read stores nothing.
        if (served) void this.#driveOffline?.saveZone(houseZone());
        return this.#rereadOnZone(day, servedDay);
      });
      return;
    }
    const result = await this.#drive.myRuns(await this.#drive.dealersDay());
    if (result.ok) {
      this.#runs = result.value;
      this.#phase = "ready";
      this.#error = "";
    } else {
      this.#runs = [];
      this.#phase = "error";
      this.#error = roadsideError(result.error, result, "Your run did not load.", {
        notFound: "You are not set up as a driver. Ask your branch manager to link your login.",
      });
    }
    this.requestUpdate();
  }

  #openStop(id: string): void {
    if (id === "") return;
    this.#view = { kind: "stop", id };
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
  }

  #findStop(id: string): { run: DriverRun; stop: DriverStop } | null {
    for (const run of this.#runs) {
      const stop = run.stops.find((candidate) => candidate.id === id);
      if (stop !== undefined) return { run, stop };
    }
    return null;
  }

  // A plain move. The server decides what is legal; a refusal is shown in its
  // own words and the stop stays where it was.
  async #advance(to: "LOADED" | "EN_ROUTE"): Promise<void> {
    if (this.#view.kind !== "stop" || this.#busy) return;
    const stopId = this.#view.id;
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const result = await this.#door().advance(stopId, to);
    if (result.ok) {
      await this.#refreshRuns();
    } else {
      this.#error = roadsideError(result.error, result, "That did not go through.");
      await this.#refreshRuns();
    }
    this.#busy = false;
    this.requestUpdate();
  }

  #openPod(): void {
    if (this.#view.kind !== "stop") return;
    const found = this.#findStop(this.#view.id);
    if (found === null) return;
    // The first line of the form is the site contact the dispatcher recorded:
    // a starting point the driver can change, never a signature.
    this.#podName = found.stop.contact_name;
    this.#podPrefill = found.stop.contact_name;
    this.#podAffirmed = false;
    this.#podBusy = false;
    this.#error = "";
    this.#notice = "";
    this.#sheetOpenerAction = "open-pod";
    this.#sheetFocusPending = "dialog";
    this.#view = { kind: "pod", id: found.stop.id };
    this.requestUpdate();
  }

  #closeSheet(): void {
    if (this.#view.kind !== "pod" && this.#view.kind !== "fail") return;
    const busy = this.#view.kind === "pod" ? this.#podBusy : this.#busy;
    if (busy) return;
    this.#view = { kind: "stop", id: this.#view.id };
    this.#error = "";
    this.#sheetFocusPending = "opener";
    this.requestUpdate();
  }

  readonly #onKeyDown = (event: KeyboardEvent): void => {
    if (this.#view.kind !== "pod" && this.#view.kind !== "fail") return;
    if (event.key === "Escape") {
      event.preventDefault();
      this.#closeSheet();
      return;
    }
    if (event.key !== "Tab") return;
    const dialog = this.renderRoot.querySelector<HTMLElement>('[role="dialog"]');
    if (dialog === null) return;
    // A radio group is one tab stop (arrows move inside it): the checked
    // radio stands for the group, or the first one when none is checked.
    const focusables = Array.from(
      dialog.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((el) => {
      if (!(el instanceof HTMLInputElement) || el.type !== "radio" || el.name === "") return true;
      const group = Array.from(dialog.querySelectorAll<HTMLInputElement>('input[type="radio"]:not([disabled])')).filter(
        (r) => r.name === el.name,
      );
      const stop = group.find((r) => r.checked) ?? group[0];
      return el === stop;
    });
    if (focusables.length === 0) return;
    const active = (this.renderRoot as ShadowRoot).activeElement as HTMLElement | null;
    const idx = active !== null ? focusables.indexOf(active) : -1;
    event.preventDefault();
    if (event.shiftKey) {
      const prev = idx <= 0 ? focusables[focusables.length - 1] : focusables[idx - 1];
      prev?.focus();
    } else {
      const next = idx < 0 || idx >= focusables.length - 1 ? focusables[0] : focusables[idx + 1];
      next?.focus();
    }
  };

  async #recordFailure(): Promise<void> {
    if (this.#view.kind !== "fail" || this.#busy || !canRecordFailure(this.#failReason)) return;
    const stopId = this.#view.id;
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const result = await this.#door().advance(stopId, "FAILED", this.#failReason.trim());
    this.#busy = false;
    if (result.ok) {
      await this.#refreshRuns();
      this.#view = { kind: "stop", id: stopId };
      this.#notice = HANDBACK_TITLE;
    } else {
      this.#error = roadsideError(result.error, result, "That did not go through.");
    }
    this.requestUpdate();
  }

  async #completeRun(runId: string): Promise<void> {
    if (runId === "" || this.#closing) return;
    const run = this.#runs.find((candidate) => candidate.id === runId);
    this.#closing = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    // Through the layer when it opened: the close waits for the queued moves.
    const result = await this.#door().completeRun(runId);
    if (result.ok) {
      this.#notice = `${run?.run_no ?? "The run"} is closed. Thanks: that is the day.`;
    } else {
      this.#error = roadsideError(result.error, result, "The run did not close.");
    }
    // The layer's close already waited for the pull that carries the closed
    // run into the store; read it.
    await this.#refreshRuns();
    this.#closing = false;
    this.requestUpdate();
  }

  async #sign(): Promise<void> {
    if (this.#view.kind !== "pod") return;
    const draft = { receivedBy: this.#podName, affirmed: this.#podAffirmed, busy: this.#podBusy };
    if (!canSign(draft)) return;
    const stopId = this.#view.id;
    const found = this.#findStop(stopId);
    this.#podBusy = true;
    this.#error = "";
    this.requestUpdate();
    const result = await this.#door().pod(stopId, normalizeName(this.#podName));
    // Connected: send now, so the driver reads the server's answer (its
    // refusal in its own words) as they always have. No signal: the
    // signature waits in the outbox and the notice says so.
    const layer = this.#driveOffline;
    let refused = "";
    if (result.ok && layer !== null && layer.runner.state !== "offline") refused = await layer.sendProofNow(stopId);
    this.#podBusy = false;
    if (result.ok && refused === "") {
      await this.#refreshRuns();
      this.#view = { kind: "stop", id: stopId };
      const waiting = this.#findStop(stopId)?.stop.proof_waiting === true;
      this.#notice = waiting ? PROOF_QUEUED_NOTICE : `Delivery to ${found?.stop.customer_name || "this stop"} is signed for.`;
    } else {
      this.#error = roadsideError(result.ok ? refused : result.error, result.ok ? { status: 400 } : result, "The signature did not go through.");
      await this.#refreshRuns();
    }
    this.requestUpdate();
  }

  // One proof photo: checked, shrunk if it is camera sized, sent on its own
  // call. On the proof screen the stop is still en route; on a delivered stop
  // it is a late photo and is folded into the proof once it is up. A failure
  // never touches the signature.
  async #sendPhoto(file: File): Promise<void> {
    if (this.#view.kind === "run" || this.#view.kind === "close" || this.#view.kind === "fail" || this.#view.kind === "conflicts") {
      return;
    }
    const stopId = this.#view.id;
    const late = this.#view.kind === "stop";
    // The photo is a direct door and the server does not hold the delivery
    // yet: it waits for the signature, in words, rather than failing at the core.
    if (this.#findStop(stopId)?.stop.proof_waiting === true) {
      this.#error = "The signature is still waiting to send. Add the photo once it has gone through.";
      this.requestUpdate();
      return;
    }
    this.#photoQueue.push({ file, stopId, late });
    if (this.#photoBusy) return;
    this.#photoBusy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    try {
      for (let next = this.#photoQueue.shift(); next !== undefined; next = this.#photoQueue.shift()) {
        try {
          await this.#sendOnePhoto(next.file, next.stopId, next.late);
        } catch {
          this.#error = "The photo did not send. Take it again and try once more.";
        }
      }
    } finally {
      this.#photoBusy = false;
      this.requestUpdate();
    }
  }

  async #sendOnePhoto(file: File, stopId: string, late: boolean): Promise<void> {
    const ready = await readyPhoto(file);
    if (!ready.ok) {
      this.#error = ready.error;
      return;
    }
    const sent = await this.#drive.uploadPhoto(stopId, ready.bytes, ready.type);
    if (!sent.ok) {
      this.#error = roadsideError(sent.error, sent, "The photo did not send.", { retry: "add it again" });
      return;
    }
    const made = URL.createObjectURL(new Blob([ready.bytes as BlobPart], { type: ready.type }));
    this.#thumbs.set(stopId, [...(this.#thumbs.get(stopId) ?? []), made]);
    // Decided at send time: a photo picked on the proof screen is late once the
    // signature has landed before it went up. Attaching twice is harmless. A
    // signature still queued in the outbox holds proof_waiting on the stop: the
    // server has no proof to attach to yet (the queued attestation folds the
    // photo in when it lands), so the attach is skipped and the refusal its
    // direct door would answer is never shown.
    const currentStop = this.#findStop(stopId)?.stop;
    if ((late || (currentStop?.pod_received_by ?? "") !== "") && !currentStop?.proof_waiting) {
      const folded = await this.#drive.attachPhotos(stopId);
      if (!folded.ok) {
        this.#error = roadsideError(folded.error, folded, "", { neutral: "The photo is up but is not on the proof yet. Add it again in a moment." });
      }
    }
    // The photo door is direct, so the count it moved lands in the store
    // only through the next pull; wait for it, then read.
    if (this.#driveOffline !== null) await this.#driveOffline.syncNow();
    await this.#refreshRuns();
  }

  // The store was read on the zone held at the time. When the zone read then serves a different
  // day (the dealer's zone arrives after the first read), read the store again on that day; an
  // unchanged day (an Ontario dealer, zone absent) reads nothing more.
  async #rereadOnZone(readDay: string, servedDay: string): Promise<void> {
    if (servedDay === readDay || this.#driveOffline === null) return;
    const answer = await this.#driveOffline.runs(servedDay);
    if (!answer.ok) return;
    this.#runs = answer.value.runs;
    this.#runStaleness = answer.staleness;
    this.#phase = "ready";
    this.#error = "";
    this.requestUpdate();
  }

  // Re-read the day without blanking the screen: the server's order and
  // statuses win, and a failed re-read keeps what is on the glass.
  async #refreshRuns(): Promise<void> {
    if (this.#driveOffline !== null) {
      const day = houseDay();
      const zoneDay = this.#drive.servedDealersDay();
      const answer = await this.#driveOffline.runs(day);
      if (answer.ok) {
        this.#runs = answer.value.runs;
        this.#runStaleness = answer.staleness;
      }
      await this.#refreshOfflineChrome();
      void zoneDay.then(({ day: servedDay, served }) => {
        // A read that served keeps the stored zone current (a dealer's zone
        // change is stored at the next read); a failed read stores nothing.
        if (served) void this.#driveOffline?.saveZone(houseZone());
        return this.#rereadOnZone(day, servedDay);
      });
      return;
    }
    const result = await this.#drive.myRuns(await this.#drive.dealersDay());
    if (result.ok) this.#runs = result.value;
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
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
        void this.#loadRuns();
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
    this.#sessions.clear();
    this.#phase = "loading";
    this.#runs = [];
    this.#error = "";
    this.#notice = "";
    this.#view = { kind: "run" };
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
    const kind = this.#view.kind;
    if (kind === "stop") return "Stop";
    if (kind === "pod") return "Proof of delivery";
    if (kind === "fail") return "Could not deliver";
    if (kind === "close") return "End of run";
    if (kind === "conflicts") return "Review";
    return "My run";
  }

  private renderWork(): TemplateResult {
    if (this.#view.kind === "close") {
      return renderClose({ runs: this.#runs, closing: this.#closing, error: this.#error, notice: this.#notice });
    }
    if (this.#view.kind === "stop" || this.#view.kind === "pod" || this.#view.kind === "fail") {
      const found = this.#findStop(this.#view.id);
      if (found !== null) {
        const sheetOpen = this.#view.kind === "pod" || this.#view.kind === "fail";
        return html`
          <div class="page-behind" data-page-behind aria-hidden=${sheetOpen ? "true" : nothing}>
            ${renderStopDetail({
              ...found,
              busy: this.#busy,
              photoBusy: this.#photoBusy,
              error: sheetOpen ? "" : this.#error,
              notice: sheetOpen ? "" : this.#notice,
            })}
          </div>
          ${this.#view.kind === "pod"
            ? renderPod({
                stop: found.stop,
                draft: { receivedBy: this.#podName, affirmed: this.#podAffirmed, busy: this.#podBusy },
                photoBusy: this.#photoBusy,
                error: this.#error,
              })
            : this.#view.kind === "fail"
              ? renderFail({
                  stop: found.stop,
                  preset: this.#failPreset,
                  reason: this.#failReason,
                  busy: this.#busy,
                  error: this.#error,
                })
              : nothing}
        `;
      }
    }
    if (this.#view.kind === "conflicts") {
      return renderConflicts({ cards: this.#openCards.length });
    }
    return html`
      ${renderRunSheet({
        phase: this.#phase,
        runs: this.#runs,
        error: this.#error,
        notice: this.#notice,
        staleness: this.#driveOffline !== null ? stalenessWords(this.#runStaleness ?? { neverPulled: true, ageMs: null }) : "",
        conflicts: this.#openCards.length,
      })}
      ${this.#phase === "error" ? html`<button type="button" class="btn btn-secondary big" data-action="reload" data-tap>Try again</button>` : nothing}
    `;
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
      <main>${this.renderWork()}</main>
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

defineElement("drive-run-app", DriveRunApp);

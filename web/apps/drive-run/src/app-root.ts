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
// hos-app-header: the shared chrome element (decision log row 938).
import "@hardscapehouse/design-system/src/header.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { HOUSE_ZONE, houseDayInZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { DriveApi, type DriverRun, type DriverStop } from "./drive-api.js";
import { HANDBACK_TITLE, canRecordFailure, canSign, normalizeName, roadsideError } from "./drive-logic.js";
import { driveStyles } from "./styles.js";
import { readyPhoto } from "./photo.js";
import { renderClose, renderEntry, renderFail, renderPod, renderRunSheet, renderStopDetail, type EntryView } from "./views.js";

const DEMO_ORG = "hh-demo";

export class DriveRunApp extends HHElement {
  static styles = [baseStyles, driveStyles];

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // two demo routes. It is the injected AccountSurface for the auth package.
  readonly #demoAccount: AccountSurface;

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The auth controller: owns sign-in, sign-out, and adoption.
  readonly #auth: AuthController;

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The staff client: carries the mint's bearer after adoption.
  readonly #staffClient = new ApiClient({
    baseUrl: "",
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => DEMO_ORG }),
  });

  // The driver's calls, over the staff client.
  readonly #drive = new DriveApi(this.#staffClient);

  // The run sheet's state. The runs are the server's answer for the dealer's
  // day; the view is which screen shows.
  #phase: "loading" | "ready" | "error" = "loading";
  #runs: DriverRun[] = [];
  #error = "";
  #notice = "";
  #busy = false;
  #view: { kind: "run" } | { kind: "stop"; id: string } | { kind: "pod"; id: string } | { kind: "fail"; id: string } | { kind: "close" } =
    { kind: "run" };

  // Proof of delivery's draft. The name field is not bound (the position
  // rules allow no value binding), so these mirror it from the input events.
  #podName = "";
  #podAffirmed = false;
  #podBusy = false;
  #podPrefill: string | null = null;
  #photoBusy = false;
  // The pictures taken in this visit, by stop, kept in memory for the
  // thumbnails: the core's photo route needs the bearer, so a picture cannot
  // load as an image address, and one taken before this visit shows as a tile.
  readonly #thumbs = new Map<string, string[]>();

  // Could not deliver's draft: the reason in the driver's words.
  #failReason = "";
  #closing = false;

  // The way in: this app's own card, drawn by views.ts.
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

  constructor() {
    super();
    this.#demoAccount = new DemoAccount({ client: this.#demoClient });
    this.#auth = new AuthController({ account: this.#demoAccount, sessions: this.#sessions });
  }

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

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
      if (this.#view.kind === "pod" || this.#view.kind === "fail") this.#view = { kind: "stop", id: this.#view.id };
      this.#error = "";
      this.requestUpdate();
      return;
    }
    if (action === "open-fail") {
      if (this.#view.kind === "stop") {
        this.#failReason = "";
        this.#error = "";
        this.#notice = "";
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
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  // The form fields: the typed name, the recipient's confirmation, the photo.
  readonly #onField = (event: Event): void => {
    const target = event.target;
    if (target instanceof HTMLTextAreaElement && target.getAttribute("data-field") === "reason") {
      this.#failReason = target.value;
      this.requestUpdate();
      return;
    }
    if (!(target instanceof HTMLInputElement)) return;
    const field = target.getAttribute("data-field");
    if (field === "name") {
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
    this.addEventListener("click", this.#onHostClick);
    // input and change do not leave this element's own shadow root (change is
    // not composed), so the form listeners sit on the root itself.
    this.renderRoot.addEventListener("input", this.#onField);
    this.renderRoot.addEventListener("change", this.#onField);
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
        clearFrontDoorHandoff();
        void this.#loadRuns();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("input", this.#onField);
    this.renderRoot.removeEventListener("change", this.#onField);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
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

  // The dealer's own day names the run to ask for, never the browser's UTC day.
  async #loadRuns(): Promise<void> {
    this.#phase = "loading";
    this.requestUpdate();
    const result = await this.#drive.myRuns(houseDayInZone(HOUSE_ZONE));
    if (result.ok) {
      this.#runs = result.value;
      this.#phase = "ready";
      this.#error = "";
    } else {
      this.#runs = [];
      this.#phase = "error";
      this.#error = roadsideError(result.error, result.status, "Your run did not load. Check your signal and try again.", {
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
    const result = await this.#drive.advance(stopId, to);
    if (result.ok) {
      await this.#refreshRuns();
    } else {
      this.#error = roadsideError(result.error, result.status, "That did not go through. Check your signal and try again.");
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
    this.#view = { kind: "pod", id: found.stop.id };
    this.requestUpdate();
  }

  async #recordFailure(): Promise<void> {
    if (this.#view.kind !== "fail" || this.#busy || !canRecordFailure(this.#failReason)) return;
    const stopId = this.#view.id;
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const result = await this.#drive.advance(stopId, "FAILED", this.#failReason.trim());
    this.#busy = false;
    if (result.ok) {
      await this.#refreshRuns();
      this.#view = { kind: "stop", id: stopId };
      this.#notice = HANDBACK_TITLE;
    } else {
      this.#error = roadsideError(result.error, result.status, "That did not go through. Check your signal and try again.");
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
    const result = await this.#drive.completeRun(runId);
    if (result.ok) {
      this.#notice = `${run?.run_no ?? "The run"} is closed. Thanks: that is the day.`;
    } else {
      this.#error = roadsideError(result.error, result.status, "The run did not close. Check your signal and try again.");
    }
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
    const result = await this.#drive.pod(stopId, normalizeName(this.#podName));
    this.#podBusy = false;
    if (result.ok) {
      await this.#refreshRuns();
      this.#view = { kind: "stop", id: stopId };
      this.#notice = `Delivery to ${found?.stop.customer_name || "this stop"} is signed for.`;
    } else {
      this.#error = roadsideError(result.error, result.status, "The signature did not go through. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  // One proof photo: checked, shrunk if it is camera sized, sent on its own
  // call. On the proof screen the stop is still en route; on a delivered stop
  // it is a late photo and is folded into the proof once it is up. A failure
  // never touches the signature.
  async #sendPhoto(file: File): Promise<void> {
    if (this.#view.kind === "run" || this.#view.kind === "close" || this.#view.kind === "fail" || this.#photoBusy) return;
    const stopId = this.#view.id;
    const late = this.#view.kind === "stop";
    this.#photoBusy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const ready = await readyPhoto(file);
    if (!ready.ok) {
      this.#error = ready.error;
    } else {
      const sent = await this.#drive.uploadPhoto(stopId, ready.bytes, ready.type);
      if (sent.ok) {
        const made = URL.createObjectURL(new Blob([ready.bytes as BlobPart], { type: ready.type }));
        this.#thumbs.set(stopId, [...(this.#thumbs.get(stopId) ?? []), made]);
      }
      if (!sent.ok) {
        this.#error = roadsideError(sent.error, sent.status, "The photo did not send. Check your signal and add it again.");
      } else {
        if (late) {
          const folded = await this.#drive.attachPhotos(stopId);
          if (!folded.ok) {
            this.#error = roadsideError(folded.error, folded.status, "The photo is up but is not on the proof yet. Add it again in a moment.");
          }
        }
        await this.#refreshRuns();
      }
    }
    this.#photoBusy = false;
    this.requestUpdate();
  }

  // Re-read the day without blanking the screen: the server's order and
  // statuses win, and a failed re-read keeps what is on the glass.
  async #refreshRuns(): Promise<void> {
    const result = await this.#drive.myRuns(houseDayInZone(HOUSE_ZONE));
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
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
        void this.#loadRuns();
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Demo sign-in refused: the mint's session was not usable." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Demo sign-in refused: ${result.error}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#phase = "loading";
    this.#runs = [];
    this.#error = "";
    this.#notice = "";
    this.#view = { kind: "run" };
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Brand theme" : "Light theme";
    return html`<button slot="theme" type="button" class="btn btn-secondary" data-action="theme">${label}</button>`;
  }

  private renderWork(): TemplateResult {
    if (this.#view.kind === "pod") {
      const found = this.#findStop(this.#view.id);
      if (found !== null) {
        return renderPod({
          stop: found.stop,
          draft: { receivedBy: this.#podName, affirmed: this.#podAffirmed, busy: this.#podBusy },
          photoBusy: this.#photoBusy,
          error: this.#error,
        });
      }
    }
    if (this.#view.kind === "fail") {
      const found = this.#findStop(this.#view.id);
      if (found !== null) {
        return renderFail({ stop: found.stop, reason: this.#failReason, busy: this.#busy, error: this.#error });
      }
    }
    if (this.#view.kind === "close") {
      return renderClose({ runs: this.#runs, closing: this.#closing, error: this.#error, notice: this.#notice });
    }
    if (this.#view.kind === "stop") {
      const found = this.#findStop(this.#view.id);
      if (found !== null) {
        return renderStopDetail({ ...found, busy: this.#busy, photoBusy: this.#photoBusy, error: this.#error, notice: this.#notice });
      }
    }
    return html`
      ${renderRunSheet({ phase: this.#phase, runs: this.#runs, error: this.#error, notice: this.#notice })}
      ${this.#phase === "error" ? html`<button type="button" class="btn btn-secondary big" data-action="reload" data-tap>Try again</button>` : nothing}
    `;
  }

  private renderSignedIn(): TemplateResult {
    return html`
      <hos-app-header signed-in demo compact product-name="Drive" product-sub="HH Drive · HH Field shell">${this.renderThemeControl()}</hos-app-header>
      <main>${this.renderWork()}</main>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hos-app-header demo compact product-name="Drive" product-sub="HH Drive · HH Field shell">${this.renderThemeControl()}</hos-app-header>
      <main>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("drive-run-app", DriveRunApp);

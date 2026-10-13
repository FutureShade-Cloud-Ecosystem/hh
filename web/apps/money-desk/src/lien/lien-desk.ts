// The liens slice's state and its presses. The desk of the Financial Controller, Payables and Receivables and the
// General Manager: the Monday triage, the jobs and the new job form, the holdback sync, the holdback aging and the
// release, the documents and their versions.
//
// The app holds no authority of its own. Who may release a holdback is the server's answer; the one rule the screen
// shows before a press is the gatekeeper rule (the General Manager releases, a finance co-signer reviews), and the
// core enforces it again, with the co-signer's authority resolved server side. A release is a money action: it asks
// first, naming the job and the amount, and what the core refuses is said in the core's own sentence.
//
// Every control is a plain element carrying a data-action name; the host's one click listener hands it to press().
// Form fields carry data-field names and are read when the button is pressed; none is bound, and a select is read
// by the id of its chosen option.
import { html, type TemplateResult } from "lit";
import type { Branch, IdentityStaffMember, LienAgingRow, LienDocument, LienJob, LienTriageItem } from "@hardscapehouse/api-client";
import { banner, type BannerView } from "../compliance/compliance-logic.js";
import type { LienApi } from "./lien-api.js";
import {
  canRelease,
  canWrite,
  cosigners,
  jobForm,
  mergeData,
  refusalWords,
  releasedWords,
  renderedWords,
  syncWords,
  templateFields,
  templateWord,
  TEMPLATE_KINDS,
} from "./lien-logic.js";
import { formatMoney } from "./lien-money.js";
import { renderLiens, type LienTab, type LiensModel, type ReleaseModel } from "./lien-views.js";

export interface LienHost {
  readonly repaint: () => void;
  /** The text of a named field in the desk's render root; a merge field is read by its key. */
  readonly field: (name: string, key?: string) => string;
  /** The id of the chosen option of a named select ("" when none is chosen). */
  readonly choice: (name: string) => string;
  /** Puts a value into a named field of the desk's render root, a select by the id of its option. */
  readonly setField: (name: string, value: string, key?: string) => void;
  /** Brings the first element matching the selector into view in the desk's render root, and puts focus on a panel. */
  readonly reveal: (selector: string) => void;
  /** The signed in caller's user id, so a co-signer is never the person releasing. */
  readonly userId: () => string;
  /** The signed in caller's role code. */
  readonly roleCode: () => string;
}

export class LienDesk {
  readonly #api: LienApi;
  readonly #host: LienHost;
  #loaded = false;
  #phase: LiensModel["phase"] = "loading";
  #tab: LienTab = "triage";
  #jobs: LienJob[] = [];
  #triage: LienTriageItem[] = [];
  #aging: LienAgingRow[] = [];
  #branches: Branch[] = [];
  #banner: BannerView = banner(null);
  #bannerState = "";
  #selectedJob: string | null = null;
  #newJob = false;
  #docJob = "";
  #docs: LienDocument[] = [];
  #docsPhase: LiensModel["docsPhase"] = "idle";
  #docKind: string = TEMPLATE_KINDS[0];
  #viewing: LienDocument | null = null;
  #versions: LienDocument[] | null = null;
  #release: ReleaseModel | null = null;
  // The roster is read once, the first time a release opens, and kept.
  #people: IdentityStaffMember[] | null = null;
  #busy = false;
  #error = "";
  #notice = "";
  // A selection or a panel asks the screen to bring itself into view once it has rendered: on a phone the panel sits
  // under the whole list and a press would otherwise change nothing the person can see.
  #reveal = "";
  #clearMerge = false;

  constructor(api: LienApi, host: LienHost) {
    this.#api = api;
    this.#host = host;
  }

  /** Reads the area the first time it opens, and the banner again every time (the compliance area may have set it). */
  ensureLoaded(): void {
    if (!this.#loaded) {
      this.#loaded = true;
      void this.load();
      return;
    }
    void this.refreshBanner();
  }

  /** The first load: jobs, triage, aging, the branch words and the shared banner. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#error = "";
    this.#host.repaint();
    const [jobs, triage, aging, branches, review] = await Promise.all([this.#api.jobs(), this.#api.triage(), this.#api.aging(), this.#api.branches(), this.#api.reviewStatus()]);
    const failed = !jobs.ok ? jobs : !triage.ok ? triage : !aging.ok ? aging : null;
    if (failed !== null && !failed.ok) {
      this.#phase = "error";
      this.#error = refusalWords("load", failed.error, failed.status ?? 0);
      this.#host.repaint();
      return;
    }
    if (jobs.ok) this.#jobs = jobs.value;
    if (triage.ok) this.#triage = triage.value;
    if (aging.ok) this.#aging = aging.value;
    this.#branches = branches.ok ? branches.value : [];
    this.#setBanner(review.ok ? review.value : null);
    this.#phase = "ready";
    this.#host.repaint();
  }

  #setBanner(status: { state: string; banner_text: string; is_draft: boolean } | null): void {
    this.#banner = banner(status);
    this.#bannerState = status === null ? "" : status.state;
  }

  async refreshBanner(): Promise<void> {
    const review = await this.#api.reviewStatus();
    this.#setBanner(review.ok ? review.value : null);
    this.#host.repaint();
  }

  /** Reads the three lists again after a write, without a loading screen and without clearing what was just said. */
  async #refresh(): Promise<void> {
    const [jobs, triage, aging] = await Promise.all([this.#api.jobs(), this.#api.triage(), this.#api.aging()]);
    if (jobs.ok) this.#jobs = jobs.value;
    if (triage.ok) this.#triage = triage.value;
    if (aging.ok) this.#aging = aging.value;
    if (this.#error === "") {
      const failed = !jobs.ok ? jobs : !triage.ok ? triage : !aging.ok ? aging : null;
      if (failed !== null && !failed.ok) this.#error = refusalWords("load", failed.error, failed.status ?? 0);
    }
    this.#host.repaint();
  }

  /** A control was pressed; true when this slice owns it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === null || !action.startsWith("li-")) return false;
    const id = control.getAttribute("data-id") ?? "";
    switch (action) {
      case "li-tab": {
        const tab = control.getAttribute("data-tab");
        if (tab === "triage" || tab === "jobs" || tab === "holdbacks" || tab === "documents") this.#openTab(tab);
        return true;
      }
      case "li-reload":
        void this.load();
        return true;
      case "li-open-job":
        this.#tab = "jobs";
        this.#select(id);
        return true;
      case "li-select-job":
        this.#select(id);
        return true;
      case "li-new-job":
        if (!canWrite(this.#host.roleCode())) return true;
        this.#newJob = true;
        this.#clearMessages();
        this.#reveal = "[data-job-form]";
        this.#host.repaint();
        return true;
      case "li-cancel-job":
        this.#newJob = false;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "li-create-job":
        void this.#createJob();
        return true;
      case "li-sync":
        void this.#sync();
        return true;
      case "li-go-docs":
        this.#docJob = id;
        this.#openTab("documents");
        return true;
      case "li-release-open":
        void this.#openRelease(id);
        return true;
      case "li-release-close":
        this.#release = null;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "li-release-confirm":
        void this.#confirmRelease();
        return true;
      case "li-render":
        void this.#render();
        return true;
      case "li-view": {
        const found = this.#docs.find((d) => d.id === id) ?? this.#versions?.find((d) => d.id === id);
        if (found !== undefined) {
          this.#viewing = found;
          this.#reveal = "[data-doc-view]";
          this.#host.repaint();
        }
        return true;
      }
      case "li-history":
        void this.#history(id);
        return true;
      case "li-close-history":
        this.#versions = null;
        this.#host.repaint();
        return true;
      default:
        return false;
    }
  }

  /** A select changed: the job whose documents are read, the form being rendered, the co-signer chosen. */
  change(control: Element): void {
    const name = control.getAttribute("data-field");
    if (name === "doc_job") {
      this.#docJob = this.#host.choice("doc_job");
      this.#versions = null;
      this.#viewing = null;
      this.#clearMessages();
      void this.#loadDocs();
    } else if (name === "doc_kind") {
      this.#docKind = this.#host.choice("doc_kind");
      this.#clearMerge = true;
      this.#host.repaint();
    }
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  #openTab(tab: LienTab): void {
    this.#tab = tab;
    this.#clearMessages();
    if (tab === "documents") {
      if (!this.#jobs.some((j) => j.id === this.#docJob)) this.#docJob = this.#jobs[0]?.id ?? "";
      void this.#loadDocs();
    }
    this.#host.repaint();
  }

  #select(id: string): void {
    this.#selectedJob = id;
    this.#newJob = false;
    this.#clearMessages();
    this.#reveal = "[data-job-panel]";
    this.#host.repaint();
  }

  async #createJob(): Promise<void> {
    if (this.#busy || !canWrite(this.#host.roleCode())) return;
    const form = jobForm({
      job_no: this.#host.field("job_no"),
      branch_id: this.#host.choice("branch_id"),
      description: this.#host.field("description"),
      contract: this.#host.field("contract"),
      percent: this.#host.field("percent"),
      direction: this.#host.choice("direction"),
      perf: this.#host.field("perf"),
      supply: this.#host.field("supply"),
    });
    this.#clearMessages();
    if (!form.ok) {
      this.#error = form.words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.createJob(form.body);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("job", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = `Job ${answer.value.job_no} created.`;
    this.#newJob = false;
    this.#selectedJob = answer.value.id;
    this.#reveal = "[data-job-panel]";
    await this.#refresh();
  }

  async #sync(): Promise<void> {
    const id = this.#selectedJob;
    if (id === null || this.#busy || !canWrite(this.#host.roleCode())) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.syncHoldbacks(id);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("job", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = syncWords(answer.value.booked_count, formatMoney(answer.value.booked_cents), answer.value.skipped_count);
    await this.#refresh();
  }

  async #openRelease(jobId: string): Promise<void> {
    this.#tab = "holdbacks";
    this.#clearMessages();
    this.#release = { jobId, roster: this.#people === null && canRelease(this.#host.roleCode()) ? "loading" : "ready", people: this.#people ?? [], rosterError: "", cosigner: "" };
    this.#reveal = "[data-release-panel]";
    this.#host.repaint();
    if (!canRelease(this.#host.roleCode()) || this.#people !== null) return;
    const roster = await this.#api.roster();
    if (this.#release === null || this.#release.jobId !== jobId) return;
    if (roster.ok) {
      this.#people = roster.value;
      this.#release = { ...this.#release, roster: "ready", people: roster.value };
    } else {
      this.#release = { ...this.#release, roster: "refused", rosterError: refusalWords("job", roster.error, roster.status ?? 0) };
    }
    this.#host.repaint();
  }

  async #confirmRelease(): Promise<void> {
    const release = this.#release;
    if (release === null || this.#busy || !canRelease(this.#host.roleCode())) return;
    const chosen = this.#host.choice("cosigner");
    this.#clearMessages();
    const person = cosigners(release.people, this.#host.userId()).find((p) => p.user_id === chosen);
    if (person === undefined) {
      this.#error = "Pick the co-signer who reviewed this release.";
      this.#host.repaint();
      return;
    }
    const row = this.#aging.find((r) => r.job_id === release.jobId);
    if (row === undefined) return;
    this.#busy = true;
    this.#release = { ...release, cosigner: chosen };
    this.#host.repaint();
    const answer = await this.#api.release(release.jobId, person.user_id, person.role_code);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("release", answer.error, answer.status ?? 0);
      // A holdback released meanwhile, or a job that is gone, is read again so the list says what is true.
      this.#host.repaint();
      if (answer.status === 422 || answer.status === 404) await this.#refresh();
      return;
    }
    this.#notice = releasedWords(formatMoney(answer.value.amount_cents), row.job_no, person.display_name);
    this.#release = null;
    await this.#refresh();
  }

  async #loadDocs(): Promise<void> {
    if (this.#docJob === "") {
      this.#docs = [];
      this.#docsPhase = "idle";
      this.#host.repaint();
      return;
    }
    const wanted = this.#docJob;
    this.#docsPhase = "loading";
    this.#host.repaint();
    const answer = await this.#api.documents(wanted);
    if (this.#docJob !== wanted) return;
    if (answer.ok) {
      this.#docs = answer.value;
      this.#docsPhase = "ready";
    } else {
      this.#docs = [];
      this.#docsPhase = "error";
      this.#error = refusalWords("job", answer.error, answer.status ?? 0);
    }
    this.#host.repaint();
  }

  async #render(): Promise<void> {
    if (this.#busy || this.#docJob === "" || !canWrite(this.#host.roleCode())) return;
    const kind = this.#docKind as LienDocument["template_kind"];
    const data = mergeData(templateFields(kind).map((f) => ({ key: f.key, value: this.#host.field("merge", f.key) })));
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.createDocument(this.#docJob, kind, data);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("job", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = renderedWords(answer.value.version, templateWord(kind));
    this.#viewing = answer.value;
    this.#versions = null;
    this.#reveal = "[data-doc-view]";
    await this.#loadDocs();
  }

  async #history(documentId: string): Promise<void> {
    this.#clearMessages();
    const answer = await this.#api.versions(documentId);
    if (!answer.ok) {
      this.#error = refusalWords("job", answer.error, answer.status ?? 0);
    } else {
      this.#versions = answer.value;
      this.#reveal = "[data-versions]";
    }
    this.#host.repaint();
  }

  /** Called after each render: keeps the selects on the state, clears a form's fields when its form changed, brings a panel into view. */
  afterRender(): void {
    if (this.#tab === "documents") {
      this.#host.setField("doc_job", this.#docJob);
      this.#host.setField("doc_kind", this.#docKind);
      if (this.#clearMerge) {
        this.#clearMerge = false;
        for (const f of templateFields(this.#docKind)) this.#host.setField("merge", "", f.key);
      }
    }
    if (this.#release !== null && this.#release.cosigner !== "") this.#host.setField("cosigner", this.#release.cosigner);
    if (this.#reveal === "") return;
    const selector = this.#reveal;
    this.#reveal = "";
    this.#host.reveal(selector);
  }

  render(): TemplateResult {
    const model: LiensModel = {
      phase: this.#phase,
      tab: this.#tab,
      canRelease: canRelease(this.#host.roleCode()),
      canWrite: canWrite(this.#host.roleCode()),
      userId: this.#host.userId(),
      banner: this.#banner,
      bannerState: this.#bannerState,
      jobs: this.#jobs,
      triage: this.#triage,
      aging: this.#aging,
      branches: this.#branches,
      selectedJob: this.#selectedJob,
      newJob: this.#newJob,
      docJob: this.#docJob,
      docs: this.#docs,
      docsPhase: this.#docsPhase,
      docKind: this.#docKind,
      viewing: this.#viewing,
      versions: this.#versions,
      release: this.#release,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
    };
    return html`${renderLiens(model)}`;
  }
}

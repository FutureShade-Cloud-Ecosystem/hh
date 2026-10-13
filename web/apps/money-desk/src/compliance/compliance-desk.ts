// The compliance slice's state and its presses: what is due soon, the whole calendar and its status filter, and the
// shared review status every lien screen's banner comes from. The desk of the Financial Controller, Payables and
// Receivables and the General Manager.
//
// The app holds no authority and no clock of its own. What is due, upcoming and past due is the core's answer; the
// one rule the screen checks before a press is that the look ahead is a whole number of days, and the core still
// answers a refusal in its own words. Setting a review status is a write the core audits.
//
// Every control is a plain element carrying a data-action name; the host's one click listener hands it to press().
// Form fields carry data-field names and are read when the button is pressed; a select is read by the id of its
// chosen option.
import { html, type TemplateResult } from "lit";
import type { ComplianceDeadline, ComplianceReviewStatus, LienJob } from "@hardscapehouse/api-client";
import type { ComplianceApi, StatusFilter } from "./compliance-api.js";
import { canWrite } from "../lien/lien-logic.js";
import { COCKPIT_SUBJECT, horizonDays, refusalWords, reviewForm, reviewStateWord, STATUS_FILTERS } from "./compliance-logic.js";
import { renderCompliance, type ComplianceModel, type ComplianceTab } from "./compliance-views.js";
import type { LienHost } from "../lien/lien-desk.js";

export type ComplianceHost = Pick<LienHost, "repaint" | "field" | "choice" | "setField" | "reveal" | "roleCode">;

export class ComplianceDesk {
  readonly #api: ComplianceApi;
  readonly #host: ComplianceHost;
  #loaded = false;
  #phase: ComplianceModel["phase"] = "loading";
  #tab: ComplianceTab = "upcoming";
  #upcoming: ComplianceDeadline[] = [];
  #horizon: number | "default" = "default";
  #calendar: ComplianceDeadline[] = [];
  #status: StatusFilter = "";
  #jobs: LienJob[] = [];
  #subject = "cockpit";
  #review: ComplianceReviewStatus | null = null;
  #reviewPhase: ComplianceModel["reviewPhase"] = "loading";
  // A review just read asks the form to show it: the state and the words it holds, ready to edit.
  #fillReview = false;
  #busy = false;
  #error = "";
  #notice = "";

  constructor(api: ComplianceApi, host: ComplianceHost) {
    this.#api = api;
    this.#host = host;
  }

  ensureLoaded(): void {
    if (this.#loaded) return;
    this.#loaded = true;
    void this.load();
  }

  async load(): Promise<void> {
    this.#phase = "loading";
    this.#error = "";
    this.#host.repaint();
    const [upcoming, calendar, jobs] = await Promise.all([this.#api.upcoming(this.#horizon), this.#api.calendar(this.#status), this.#api.jobs()]);
    const failed = !upcoming.ok ? upcoming : !calendar.ok ? calendar : null;
    if (failed !== null && !failed.ok) {
      this.#phase = "error";
      this.#error = refusalWords(failed.error, failed.status ?? 0);
      this.#host.repaint();
      return;
    }
    if (upcoming.ok) this.#upcoming = upcoming.value;
    if (calendar.ok) this.#calendar = calendar.value;
    this.#jobs = jobs.ok ? jobs.value : [];
    this.#phase = "ready";
    this.#host.repaint();
    await this.#loadReview();
  }

  #subjectOf(): { type: "OTHER" | "LIEN_JOB"; id: string } {
    return this.#subject === "cockpit" ? { type: "OTHER", id: COCKPIT_SUBJECT } : { type: "LIEN_JOB", id: this.#subject };
  }

  async #loadReview(): Promise<void> {
    const wanted = this.#subject;
    const subject = this.#subjectOf();
    this.#reviewPhase = "loading";
    this.#host.repaint();
    const answer = await this.#api.reviewStatus(subject.type, subject.id);
    if (this.#subject !== wanted) return;
    this.#review = answer.ok ? answer.value : null;
    this.#reviewPhase = answer.ok ? "ready" : "error";
    this.#fillReview = answer.ok;
    this.#host.repaint();
  }

  /** A control was pressed; true when this slice owns it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === null || !action.startsWith("co-")) return false;
    switch (action) {
      case "co-tab": {
        const tab = control.getAttribute("data-tab");
        if (tab === "upcoming" || tab === "calendar" || tab === "review") {
          this.#tab = tab;
          this.#error = "";
          this.#notice = "";
          this.#host.repaint();
        }
        return true;
      }
      case "co-reload":
        void this.load();
        return true;
      case "co-show":
        void this.#show();
        return true;
      case "co-filter": {
        const filter = control.getAttribute("data-filter") ?? "";
        if (STATUS_FILTERS.some((f) => f.id === filter)) void this.#filter(filter as StatusFilter);
        return true;
      }
      case "co-save-review":
        void this.#save();
        return true;
      default:
        return false;
    }
  }

  /** The About select changed: the review of that subject is read. */
  change(control: Element): void {
    if (control.getAttribute("data-field") !== "review_subject") return;
    this.#subject = this.#host.choice("review_subject");
    this.#notice = "";
    this.#error = "";
    void this.#loadReview();
  }

  async #show(): Promise<void> {
    const horizon = horizonDays(this.#host.field("horizon"));
    this.#notice = "";
    if (horizon === null) {
      this.#error = "The look ahead is a whole number of days from 1 to 365, or blank for the core's own.";
      this.#host.repaint();
      return;
    }
    this.#error = "";
    const answer = await this.#api.upcoming(horizon);
    if (!answer.ok) {
      this.#error = refusalWords(answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#horizon = horizon;
    this.#upcoming = answer.value;
    this.#host.repaint();
  }

  async #filter(status: StatusFilter): Promise<void> {
    this.#error = "";
    this.#notice = "";
    const answer = await this.#api.calendar(status);
    if (!answer.ok) {
      this.#error = refusalWords(answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#status = status;
    this.#calendar = answer.value;
    this.#host.repaint();
  }

  async #save(): Promise<void> {
    if (this.#busy || !canWrite(this.#host.roleCode())) return;
    const subject = this.#subjectOf();
    const form = reviewForm(subject.type, subject.id, this.#host.choice("review_state"), this.#host.field("review_banner"));
    this.#error = "";
    this.#notice = "";
    if (!form.ok) {
      this.#error = form.words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.setReviewStatus(form.body);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords(answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#review = answer.value;
    this.#reviewPhase = "ready";
    this.#fillReview = true;
    this.#notice = `Saved: ${reviewStateWord(answer.value.state)}.`;
    this.#host.repaint();
  }

  /** Called after each render: keeps the selects on the state and shows a review just read in the form. */
  afterRender(): void {
    if (this.#tab !== "review") return;
    this.#host.setField("review_subject", this.#subject);
    if (this.#fillReview && this.#review !== null) {
      this.#fillReview = false;
      this.#host.setField("review_state", this.#review.state);
      this.#host.setField("review_banner", this.#review.banner_text);
    }
  }

  render(): TemplateResult {
    const model: ComplianceModel = {
      phase: this.#phase,
      tab: this.#tab,
      upcoming: this.#upcoming,
      calendar: this.#calendar,
      status: this.#status,
      jobs: this.#jobs,
      subject: this.#subject,
      review: this.#review,
      reviewPhase: this.#reviewPhase,
      canWrite: canWrite(this.#host.roleCode()),
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
    };
    return html`${renderCompliance(model)}`;
  }
}

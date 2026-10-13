// The compliance slice's screens, as plain render functions of a model. They hold no state and own no events: every
// control is a plain element carrying a data-action name and the slice's click listener acts on it. Form fields carry
// data-field names and are read when the button is pressed; none is bound.
//
// Every date on a screen is the core's: the app computes none. What is past due is the core's own upcoming list
// marking it, and the calendar reads that mark, so the two tabs never disagree about the same deadline. References:
// the Money Desk boards (cards border first, no shadow, status as a word before a colour, a table that gives way to
// cards on a phone) and the original app's release calendar and shared DRAFT banner.
import { html, nothing, type TemplateResult } from "lit";
import type { ComplianceDeadline, ComplianceReviewStatus, LienJob } from "@hardscapehouse/api-client";
import { empty, loadFailed, loading, messages, pill, tabsNav } from "../lien/lien-kit.js";
import { deadlineKindWord, deadlineStatusWord, deadlineTone, dueWords, READ_ONLY_REVIEW, REVIEW_STATES, reviewStateWord, reviewTone, STATUS_FILTERS } from "./compliance-logic.js";

export type ComplianceTab = "upcoming" | "calendar" | "review";

export interface ComplianceModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: ComplianceTab;
  readonly upcoming: readonly ComplianceDeadline[];
  readonly calendar: readonly ComplianceDeadline[];
  readonly status: string;
  readonly jobs: readonly LienJob[];
  readonly subject: string;
  readonly review: ComplianceReviewStatus | null;
  readonly reviewPhase: "loading" | "ready" | "error";
  readonly canWrite: boolean;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
}

const jobLabel = (jobs: readonly LienJob[], id: string): string => (id === "" ? "No job" : (jobs.find((j) => j.id === id)?.job_no ?? "Unknown job"));

/** The calendar reads the upcoming list's mark: the core stores a past due deadline as open and only marks it there. */
function statusOf(model: ComplianceModel, d: ComplianceDeadline): string {
  return model.upcoming.some((u) => u.id === d.id && u.status === "OVERDUE") ? "OVERDUE" : d.status;
}

function deadlineRow(model: ComplianceModel, d: ComplianceDeadline): TemplateResult {
  const status = statusOf(model, d);
  const job = jobLabel(model.jobs, d.job_id);
  return html`
    <tr data-row="deadline" data-kind=${d.deadline_kind}>
      <td><strong>${deadlineKindWord(d.deadline_kind)}</strong></td>
      <td class="li-mono">${job}</td>
      <td>${dueWords(d.due_date)}</td>
      <td class="num">${d.rule_days} days</td>
      <td>${pill(deadlineTone(status), deadlineStatusWord(status))}</td>
    </tr>
  `;
}

function deadlineCard(model: ComplianceModel, d: ComplianceDeadline): TemplateResult {
  const status = statusOf(model, d);
  return html`
    <li class="li-card" data-row="deadline" data-kind=${d.deadline_kind}>
      <div class="li-card-top"><strong>${deadlineKindWord(d.deadline_kind)}</strong>${pill(deadlineTone(status), deadlineStatusWord(status))}</div>
      <span class="li-mono">${jobLabel(model.jobs, d.job_id)}</span>
      <span class="li-sub">Due ${dueWords(d.due_date)} · rule ${d.rule_days} days</span>
    </li>
  `;
}

function deadlines(model: ComplianceModel, rows: readonly ComplianceDeadline[], label: string): TemplateResult {
  return html`
    <div class="li-scroll li-hide-narrow">
      <table aria-label=${label}>
        <thead><tr><th>Deadline</th><th>Job</th><th>Due</th><th class="num">Rule</th><th>Status</th></tr></thead>
        <tbody>${rows.map((d) => deadlineRow(model, d))}</tbody>
      </table>
    </div>
    <ul class="li-cards">${rows.map((d) => deadlineCard(model, d))}</ul>
  `;
}

function upcoming(model: ComplianceModel): TemplateResult {
  const past = model.upcoming.filter((d) => d.status === "OVERDUE").length;
  return html`
    <section class="li-panel">
      <div class="li-panel-head">
        <h2>Due soon</h2>
        <span class="li-summary" data-overdue-count>${past === 0 ? "None past due" : `${past} past due`}</span>
      </div>
      <p class="li-dim">The open deadlines due inside the look ahead, soonest first. A deadline already past is marked overdue.</p>
      <div class="li-toolbar">
        <label class="li-field">Look ahead (days)<input data-field="horizon" aria-label="Look ahead (days)" inputmode="numeric" autocomplete="off" /></label>
        <button type="button" class="btn btn-secondary" data-action="co-show">Show</button>
      </div>
      <p class="li-hint">Blank reads the core's own look ahead.</p>
      ${model.upcoming.length === 0 ? empty("upcoming", "Nothing is due inside this look ahead.", "Widen the look ahead to see what comes next.") : deadlines(model, model.upcoming, "Due soon")}
    </section>
  `;
}

function calendar(model: ComplianceModel): TemplateResult {
  return html`
    <section class="li-panel">
      <div class="li-panel-head"><h2>Calendar</h2></div>
      <p class="li-dim">Every deadline the core holds, by due date.</p>
      <div class="li-filters" role="group" aria-label="Status">
        ${STATUS_FILTERS.map((f) => html`<button type="button" class="li-filter" data-action="co-filter" data-filter=${f.id} aria-pressed="${model.status === f.id ? "true" : "false"}">${f.word}</button>`)}
      </div>
      ${model.calendar.length === 0
        ? empty("calendar", model.status === "" ? "No deadlines are registered." : "No deadlines with that status.", model.status === "" ? "A job with a substantial performance date registers its release deadline here." : "Pick another status, or All.")
        : deadlines(model, model.calendar, "Calendar")}
    </section>
  `;
}

function review(model: ComplianceModel): TemplateResult {
  const r = model.review;
  return html`
    <section class="li-panel">
      <div class="li-panel-head"><h2>Review status</h2></div>
      <p class="li-dim">The banner every lien screen carries comes from here, so changing it is a data change.</p>
      <label class="li-field">About
        <select data-field="review_subject" aria-label="About">
          <option data-id="cockpit">Whole lien cockpit</option>
          ${model.jobs.map((j) => html`<option data-id=${j.id}>Job ${j.job_no}</option>`)}
        </select>
      </label>
      ${model.reviewPhase === "loading"
        ? loading("Loading the review status.")
        : r === null
          ? html`<p class="li-dim" role="status">The review status did not load. Pick another subject or reload the area.</p>`
          : html`
              <div class="li-review">
                <p><span class="li-status li-status-${reviewTone(r.state)}" data-review-state>${reviewStateWord(r.state)}</span></p>
                <p class="li-banner li-banner-${reviewTone(r.state)}" data-review-banner>${r.banner_text}</p>
                ${r.reviewed_at === undefined ? nothing : html`<p class="li-hint">Last set ${dueWords(r.reviewed_at.slice(0, 10))}.</p>`}
              </div>
            `}
      ${model.canWrite ? reviewForm(model) : html`<p class="li-note" data-read-only>${READ_ONLY_REVIEW}</p>`}
    </section>
  `;
}

function reviewForm(model: ComplianceModel): TemplateResult {
  return html`
      <div class="li-form">
        <label class="li-field">State
          <select data-field="review_state" aria-label="State">${REVIEW_STATES.map((s) => html`<option data-id=${s}>${reviewStateWord(s)}</option>`)}</select>
        </label>
        <label class="li-field li-field-wide">Banner words<textarea data-field="review_banner" aria-label="Banner words" rows="3"></textarea></label>
      </div>
      <p class="li-hint">Blank banner words are the core's default words.</p>
      <div class="li-actions"><button type="button" class="btn btn-primary" data-action="co-save-review" ?disabled=${model.busy}>Save review status</button></div>
  `;
}

export function renderCompliance(model: ComplianceModel): TemplateResult {
  return html`
    <div data-compliance>
      <header class="li-head">
        <h1>Compliance</h1>
        <p class="li-dim">The deadlines the lien jobs register, and the review status of the forms. Demo data only.</p>
      </header>
      ${model.phase === "loading"
        ? loading("Loading the compliance calendar.")
        : model.phase === "error"
          ? loadFailed(model.error, "co-reload")
          : html`
              ${tabsNav("Compliance views", "co-tab", model.tab, [
                { id: "upcoming", label: "Upcoming", count: null },
                { id: "calendar", label: "Calendar", count: null },
                { id: "review", label: "Review status", count: null },
              ])}
              ${messages(model.error, model.notice)}
              ${model.tab === "upcoming" ? upcoming(model) : model.tab === "calendar" ? calendar(model) : review(model)}
            `}
    </div>
  `;
}

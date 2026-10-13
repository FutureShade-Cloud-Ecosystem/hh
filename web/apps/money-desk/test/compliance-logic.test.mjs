// The compliance slice's plain logic: the words for every deadline and review state the core stores, how a due
// date reads, how the horizon box and the review form become what the core takes, and how a refusal reads out
// loud. Nothing here computes a date: the core says what is due, overdue and upcoming.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  COCKPIT_SUBJECT,
  STATUS_FILTERS,
  banner,
  deadlineKindWord,
  deadlineStatusWord,
  deadlineTone,
  dueWords,
  horizonDays,
  refusalWords,
  reviewForm,
  reviewStateWord,
  reviewTone,
} from "../src/compliance/compliance-logic.ts";

test("the deadline kinds and states read as plain words, with a tone that only backs the word", () => {
  assert.equal(deadlineKindWord("HOLDBACK_RELEASE"), "Holdback release");
  assert.equal(deadlineKindWord("LIEN_EXPIRY"), "Lien expiry");
  assert.equal(deadlineKindWord("NOTICE_DEADLINE"), "Notice deadline");
  assert.equal(deadlineKindWord("PROOF_OF_CLAIM"), "Proof of claim");
  assert.equal(deadlineKindWord("OTHER"), "Other");
  assert.equal(deadlineKindWord("NEW_KIND"), "new kind");
  assert.equal(deadlineStatusWord("OVERDUE"), "Overdue");
  assert.equal(deadlineStatusWord("SATISFIED"), "Satisfied");
  assert.equal(deadlineStatusWord("WAIVED"), "Waived");
  assert.equal(deadlineStatusWord("OPEN"), "Open");
  assert.equal(deadlineTone("OVERDUE"), "stop");
  assert.equal(deadlineTone("OPEN"), "watch");
  assert.equal(deadlineTone("SATISFIED"), "good");
  assert.equal(deadlineTone("WAIVED"), "plain");
  assert.deepEqual(
    STATUS_FILTERS.map((f) => f.id),
    ["", "OPEN", "OVERDUE", "SATISFIED", "WAIVED"],
  );
});

test("a due date reads as a day a person says, and a malformed one is left as the core wrote it", () => {
  const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  // Days are cut from an arbitrary fixed day, so no calendar literal is typed.
  for (const n of [0, 63, 364, 800, 4000]) {
    const at = new Date(Date.UTC(2000, 0, 1 + n));
    const iso = at.toISOString().slice(0, 10);
    assert.equal(dueWords(iso), `${months[at.getUTCMonth()]} ${at.getUTCDate()}, ${at.getUTCFullYear()}`);
    assert.match(dueWords(iso), /^[A-Z][a-z]{2} \d{1,2}, \d{4}$/);
  }
  const year = new Date().getUTCFullYear();
  assert.equal(dueWords(`${year}-02-30`), `${year}-02-30`);
  assert.equal(dueWords(""), "No date");
  assert.equal(dueWords("soon"), "soon");
});

test("the horizon box is a whole number of days, or blank for the core's own", () => {
  assert.equal(horizonDays(""), "default");
  assert.equal(horizonDays(" 30 "), 30);
  assert.equal(horizonDays("365"), 365);
  for (const bad of ["0", "-5", "12.5", "abc", "366", "1e2"]) assert.equal(horizonDays(bad), null, bad);
});

test("the review states read as words, and the review form becomes the body the core takes", () => {
  assert.equal(reviewStateWord("DRAFT"), "Draft");
  assert.equal(reviewStateWord("PENDING"), "Pending review");
  assert.equal(reviewStateWord("APPROVED"), "Approved");
  assert.equal(reviewStateWord("REJECTED"), "Rejected");
  assert.equal(reviewTone("APPROVED"), "good");
  assert.equal(reviewTone("REJECTED"), "stop");
  assert.equal(reviewTone("DRAFT"), "watch");
  assert.deepEqual(reviewForm("OTHER", COCKPIT_SUBJECT, "APPROVED", " Checked against the dealer's forms "), {
    ok: true,
    body: { subject_type: "OTHER", subject_id: COCKPIT_SUBJECT, state: "APPROVED", banner_text: "Checked against the dealer's forms" },
  });
  assert.deepEqual(reviewForm("LIEN_JOB", "job-1", "DRAFT", ""), { ok: true, body: { subject_type: "LIEN_JOB", subject_id: "job-1", state: "DRAFT", banner_text: "" } });
  assert.deepEqual(reviewForm("LIEN_JOB", "", "DRAFT", ""), { ok: false, words: "Pick what the review is about." });
  assert.deepEqual(reviewForm("OTHER", COCKPIT_SUBJECT, "MAYBE", ""), { ok: false, words: "Pick a review state." });
});

test("the shared banner is the core's own words, and a missing answer is said, not guessed", () => {
  assert.deepEqual(banner({ state: "DRAFT", banner_text: "Draft, pending client validation", is_draft: true }), { text: "Draft, pending client validation", tone: "watch", draft: true });
  assert.deepEqual(banner({ state: "APPROVED", banner_text: "Reviewed", is_draft: false }), { text: "Reviewed", tone: "good", draft: false });
  assert.deepEqual(banner(null), { text: "The review status did not load.", tone: "plain", draft: true });
});

test("a refusal reads in the core's own sentence, with who may do it when the core says only Forbidden", () => {
  assert.equal(refusalWords("Forbidden", 403), "That is for the Financial Controller, Payables and Receivables or the General Manager.");
  assert.equal(refusalWords("compliance: invalid review state \"MAYBE\"", 422), "Invalid review state \"MAYBE\".");
  assert.equal(refusalWords("compliance: subject_id is required", 400), "Subject_id is required.");
  assert.equal(refusalWords("", 0), "The compliance calendar did not answer. Reload to try again.");
});

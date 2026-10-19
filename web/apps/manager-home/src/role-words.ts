// The words a role's landing carries: the sentence under the welcome and the
// strip of views it offers. The core's dashboard profile name decides which
// role this is (the widgets read names it); there is no client-side authority
// here. The strip is a courtesy of order and relevance: every view's own read is
// still gated by the core, and a view a role may open but is not listed for stays
// reachable from its needs row.
//
// The sentences are each role's first top jobs in docs/analysis/role-catalog.yaml,
// shortened to one line. A profile this table does not know keeps the plain
// sentence and the whole strip, so a legacy or future profile never loses a link.
import type { ViewKind } from "./manager-logic.js";

export type StripView = Extract<ViewKind, "shortages" | "receiving" | "approvals" | "drivers" | "warranty" | "quick-actions">;

/** The strip's own order; every role's list is a subsequence of it. */
const WHOLE_STRIP: readonly StripView[] = Object.freeze(["shortages", "receiving", "approvals", "drivers", "warranty", "quick-actions"]);

export interface RoleWords {
  readonly sub: string;
  readonly views: readonly StripView[];
}

const PLAIN: RoleWords = Object.freeze({ sub: "Your day at a glance.", views: WHOLE_STRIP });

const BY_PROFILE: Readonly<Record<string, RoleWords>> = Object.freeze({
  "manager.yard": {
    sub: "The pick queue, the shortages and what is going out.",
    views: ["shortages", "receiving", "drivers", "warranty"],
  },
  "manager.logistics": {
    sub: "The loads, the delivery dates and the trucks and drivers.",
    views: ["drivers", "warranty"],
  },
  "manager.branch": {
    sub: "What needs a person first, then the branch numbers.",
    views: ["shortages", "receiving", "drivers", "warranty"],
  },
  "manager.sales": {
    sub: "How selling is going, and the quotes the counter cannot commit.",
    views: ["approvals", "warranty"],
  },
  "manager.procurement": {
    sub: "The gaps the yard reports and where each purchase order got to.",
    views: ["shortages", "receiving", "warranty"],
  },
  "manager.payables": {
    sub: "The receivables book first, then the receipts waiting to be matched.",
    views: ["receiving", "warranty"],
  },
  "manager.controller": {
    sub: "What carries money first, then the book.",
    views: ["approvals", "warranty", "quick-actions"],
  },
  "manager.general": {
    sub: "The whole dealer at a glance: what needs you, then the numbers.",
    views: WHOLE_STRIP,
  },
});

export const ROLE_PROFILES: readonly string[] = Object.freeze(Object.keys(BY_PROFILE));

export function roleWords(profile: string): RoleWords {
  return Object.hasOwn(BY_PROFILE, profile) ? BY_PROFILE[profile] : PLAIN;
}

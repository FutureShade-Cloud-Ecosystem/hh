// The admin-rest surfaces' one door to the core: the assistant's admin reads
// and writes, the print queue's read, the two exports, the catalog's product
// maintenance, and the platform keys read, over the generated api-client,
// answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the screens use. A field the projection
// does not name is dropped at this door and never reaches a screen. Nothing
// here sums, converts or derives a figure: an amount arrives as the server set
// it and is parsed only through its decimal string (admin-logic.ts).
//
// THE UNREGISTERED READ. The operator platform keys read is contract-marked
// for this surface (x-access dealer-admin, level read, staff wide) but the
// core does not serve it yet (the parity ledger holds it for the operator
// lane); the day the core registers it the same call lands. Until then the
// read answers the transport's refusal and the screen says what waits.
//
// Port provenance: the shapes are the original app's assist-admin.ts and
// product-edit.ts reads (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md), rebuilt on HH's generated client and narrowed to what this
// app's screens use.
import { ApiClient, staffAssist, staffExport, staffMessaging, staffPrinting, type Result } from "@hardscapehouse/api-client";
import type { AssistSpendRow } from "./admin-logic.js";

// -- the assistant's spend, budgets and settings --------------------------------------------------

/** One settings payload: the four keys and the key's NAME, never a credential. */
export interface AssistSettingsView {
  readonly provider: string;
  readonly model: string;
  readonly chat_enabled: boolean;
  readonly spend_visible_to_non_admin: boolean;
  readonly key_env_var: string;
  readonly key_present: boolean;
}

/** The spend report: the month and every active person's row, zeros included. */
export interface AssistSpendReport {
  readonly month: string;
  readonly users: readonly AssistSpendRow[];
}

/** The limits editor's whole payload: role defaults, user overrides, code default. */
export interface AssistLimitsView {
  readonly role_budgets: Readonly<Record<string, number>>;
  readonly user_overrides: Readonly<Record<string, number>>;
  readonly default_budget: number;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const bool = (value: unknown): boolean => value === true;
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

function toSpendRow(raw: unknown): AssistSpendRow {
  const r = bag(raw);
  const b = bag(r.budget);
  return {
    user_id: text(r.user_id),
    display_name: text(r.display_name),
    email: text(r.email),
    role_code: text(r.role_code),
    exchanges: num(r.exchanges),
    tokens_in: num(r.tokens_in),
    tokens_out: num(r.tokens_out),
    budget: { tokens: num(b.tokens), source: text(b.source) },
  };
}

// -- the print queue -----------------------------------------------------------------------------

/** One row of the print queue, as the screens use it. */
export interface PrintJobRow {
  readonly id: string;
  readonly doc_type: string;
  readonly ref_id: string;
  readonly branch_id: string;
  readonly render_target: string;
  readonly status: string;
  readonly requested_by: string;
  readonly failure_reason: string;
  readonly requested_at: string;
  readonly printed_at: string;
}

function toPrintJobRow(raw: unknown): PrintJobRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    doc_type: text(r.doc_type),
    ref_id: text(r.ref_id),
    branch_id: text(r.branch_id),
    render_target: text(r.render_target),
    status: text(r.status),
    requested_by: text(r.requested_by),
    failure_reason: text(r.failure_reason),
    requested_at: text(r.requested_at),
    printed_at: text(r.printed_at),
  };
}

// -- the exports ---------------------------------------------------------------------------------

/** The audit workbook's filters, the audit trail's own four. */
export interface AuditWorkbookQuery {
  readonly entity_type?: string;
  readonly action?: string;
  readonly actor_id?: string;
  readonly since?: string;
}

/** One admin-masked message row, as the screens use it. */
export interface MessagesExportRow {
  readonly id: string;
  readonly channel_id: string;
  readonly kind: string;
  readonly author_name: string;
  readonly body: string;
  readonly created_at: string;
  readonly deleted_at: string;
}

function toMessagesExportRow(raw: unknown): MessagesExportRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    channel_id: text(r.channel_id),
    kind: text(r.kind),
    author_name: text(r.author_name),
    body: text(r.body),
    created_at: text(r.created_at),
    deleted_at: text(r.deleted_at),
  };
}

/** The messaging export's filters, the route's own optional four. */
export interface MessagesExportQuery {
  readonly channel_id?: string;
  readonly user_id?: string;
  readonly since?: string;
  readonly until?: string;
}

/** Every call the admin-rest surfaces make. */
export class AdminApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // -- the assistant's admin ------------------------------------------------------------------

  /** The month's spend report: every active person, biggest spender first, zeros included. */
  async assistSpend(): Promise<Result<AssistSpendReport>> {
    const answer = await staffAssist.assistAdminSpend(this.#client);
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: { month: text(page.month), users: list(page.users).map(toSpendRow) } };
  }

  /** The whole limits picture: the role defaults, the overrides, the code default. */
  async assistLimits(): Promise<Result<AssistLimitsView>> {
    const answer = await staffAssist.assistAdminLimits(this.#client);
    if (!answer.ok) return answer;
    const v = bag(answer.value);
    const numbers = (raw: unknown): Record<string, number> => {
      const out: Record<string, number> = {};
      for (const [k, n] of Object.entries(bag(raw))) out[k] = num(n);
      return out;
    };
    return {
      ok: true,
      value: {
        role_budgets: numbers(v.role_budgets),
        user_overrides: numbers(v.user_overrides),
        default_budget: num(v.default_budget),
      },
    };
  }

  /**
   * One role's monthly budget. A refusal arrives in the core's own words (a
   * negative budget is refused there with a sentence, not a constraint).
   */
  async putRoleLimit(role: string, tokens: number): Promise<Result<{ role_code: string; tokens: number }>> {
    const answer = await staffAssist.assistPutRoleLimit(this.#client, role, { tokens });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { role_code: text(r.role_code), tokens: num(r.tokens) } };
  }

  /** One person's override, whole or absent: the write REPLACES the role budget entirely. */
  async putUserLimit(user: string, tokens: number): Promise<Result<{ user_id: string; tokens: number; source: string }>> {
    const answer = await staffAssist.assistPutUserLimit(this.#client, user, { tokens });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { user_id: text(r.user_id), tokens: num(r.tokens), source: text(r.source) } };
  }

  /** Reset to role: the DELETE, idempotent, not a copy of the role's number. */
  async resetUserLimit(user: string): Promise<Result<{ user_id: string; source: string }>> {
    const answer = await staffAssist.assistResetUserLimit(this.#client, user);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { user_id: text(r.user_id), source: text(r.source) } };
  }

  /** The assistant's settings: the four keys, and the key's NAME with whether it was found. */
  async assistSettings(): Promise<Result<AssistSettingsView>> {
    const answer = await staffAssist.assistGetSettings(this.#client);
    if (!answer.ok) return answer;
    const v = bag(answer.value);
    const s = bag(v.settings);
    return {
      ok: true,
      value: {
        provider: text(s.provider),
        model: text(s.model),
        chat_enabled: bool(s.chat_enabled),
        spend_visible_to_non_admin: bool(s.spend_visible_to_non_admin),
        key_env_var: text(v.key_env_var),
        key_present: bool(v.key_present),
      },
    };
  }

  /** One config key. An unknown key is refused by the core, in its own words. */
  async putSetting(key: string, value: string): Promise<Result<{ key: string; value: string }>> {
    const answer = await staffAssist.assistPutSetting(this.#client, { key, value });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { key: text(r.key), value: text(r.value) } };
  }

  // -- the print queue ---------------------------------------------------------------------------

  /**
   * One document's print history, newest first as the core serves it. Both
   * parameters are the route's own: the doc type is one of the three papers,
   * and the reference is the document's id. A bad pair is refused by the core
   * in its own words (an unknown paper with a 400, a reference that is not a
   * document id with the bare not found).
   */
  async printJobs(docType: string, refId: string): Promise<Result<readonly PrintJobRow[]>> {
    const answer = await staffPrinting.printingListJobs(this.#client, {
      doc_type: docType as "RECEIPT" | "LABEL_ZPL" | "DELIVERY_DOC",
      ref_id: refId,
    });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.jobs).map(toPrintJobRow) };
  }

  // -- the exports --------------------------------------------------------------------------------

  /** The audit trail as a workbook: one call, the raw bytes of the file. */
  async auditWorkbook(query: AuditWorkbookQuery): Promise<Result<Uint8Array>> {
    return staffExport.exportAuditXlsx(this.#client, { ...query });
  }

  /** The house's messages, masked: one call, the rows the file is written from. */
  async messagingExport(query: MessagesExportQuery): Promise<Result<readonly MessagesExportRow[]>> {
    const answer = await staffMessaging.messagingExport(this.#client, { ...query });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.messages).map(toMessagesExportRow) };
  }
}

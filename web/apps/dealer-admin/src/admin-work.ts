// The admin-rest surfaces' controller: the state the screens draw from, the
// loads, and what each press does. It is not an element; the root element owns
// it, hands it a way to repaint, to read and set the form fields, and to hand
// bytes to the person as a file, and asks it for the active surface's
// template. The Money Desk's receivables desk is the pattern; here it keeps
// the root element's own file small so the registry's next surfaces land one
// file at a time.
//
// Every write is held to the server: a budget, a setting, a save lands only
// when the core answers, and the screen then shows what the core now says (the
// re-read report, the settings it answers), never what the app expected. A
// refusal reads in the core's own words (admin-logic.ts); an amount is parsed
// through its decimal string, never through float arithmetic.
import type { TemplateResult } from "lit";
import { AdminApi } from "./admin-api.js";
import {
  assistBudgetInputWords,
  assistRefusalWords,
  auditWorkbookName,
  imageTypeWords,
  messagesExportName,
  messagesRefusalWords,
  printDocTypeFromLabel,
  productChangedPatch,
  productDraftFrom,
  productRefusalWords,
  PRODUCT_TEXT_FIELDS,
  spendTotal,
  usedFraction,
  wholeAmount,
} from "./admin-logic.js";
import { renderAssist, type AssistEditTarget, type AssistModel } from "./admin-assist.js";
import { renderPrint, type PrintModel } from "./admin-print.js";
import { renderExports, type MessagesModel, type WorkbookModel } from "./admin-exports.js";
import { renderCatalog, type CatalogModel } from "./admin-catalog.js";
import { renderKeys, type KeysModel } from "./admin-keys.js";
import { sinceWire } from "./audit-logic.js";

/** The surfaces this controller owns, as the registry names them. */
export const ADMIN_WORK_SURFACES: readonly string[] = ["assist", "printing", "exports", "catalog", "keys"];

/** Is this surface id this controller's? The root's router line asks once. */
export function isAdminWorkSurface(surface: string): boolean {
  return ADMIN_WORK_SURFACES.includes(surface);
}

/** The ways the controller reaches the page: the root element provides them. */
export interface AdminHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string) => string;
  /** Sets a form field's text; the screens bind no value, so a fill goes through here. */
  readonly setField: (name: string, value: string) => void;
  /** Sets a checkbox's checked state; mirrored in code for the same reason. */
  readonly setChecked: (name: string, value: boolean) => void;
  /** Sets one meter's fill as a fraction of its track. */
  readonly meter: (name: string, fraction: number) => void;
  /** Hands bytes to the person as a file. */
  readonly download: (bytes: Uint8Array, filename: string, type: string) => void;
}

const emptyAssist = (): AssistModel => ({
  phase: "loading",
  error: "",
  report: null,
  limits: null,
  settings: null,
  editing: null,
  editError: "",
  notice: "",
  busy: false,
});

const emptyPrint = (): PrintModel => ({
  // idle: the surface asks a question (paper, reference) before it reads, so
  // its first paint is the question, not an empty answer. The paper is the
  // select's own first option, answered in its own words.
  phase: "idle",
  busy: false,
  error: "",
  docType: "Receipt",
  ref: "",
  rows: [],
});

const emptyWorkbook = (): WorkbookModel => ({
  busy: false,
  error: "",
  notice: "",
  entityType: "",
  action: "",
  actorId: "",
  since: "",
});

const emptyMessages = (): MessagesModel => ({
  busy: false,
  error: "",
  notice: "",
  asked: false,
  channelId: "",
  userId: "",
  since: "",
  until: "",
  rows: [],
});

const emptyCatalog = (): CatalogModel => ({
  phase: "idle",
  busy: false,
  error: "",
  notice: "",
  q: "",
  hits: [],
  opened: null,
  units: [],
  categories: [],
  pickedName: "",
  altText: "",
});

const emptyKeys = (): KeysModel => ({
  phase: "loading",
  error: "",
  rows: [],
});

export class AdminWork {
  readonly #api: AdminApi;
  readonly #host: AdminHost;

  // The assist surface's state.
  #assist: AssistModel = emptyAssist();

  // The print jobs surface's state.
  #print: PrintModel = emptyPrint();

  // The exports surface's state: two cards, each its own call.
  #workbook: WorkbookModel = emptyWorkbook();
  #messages: MessagesModel = emptyMessages();

  // The catalog surface's state, and the picked picture (a File is not state
  // the screens draw; the model holds only its words).
  #catalog: CatalogModel = emptyCatalog();
  #pickedImage: File | null = null;
  // The editor's fields carry no bound value, so a freshly opened (or newly
  // saved) row is seeded on the next mirror pass, once the fields exist.
  #seedPending = false;

  // The platform keys surface's state.
  #keys: KeysModel = emptyKeys();

  constructor(api: AdminApi, host: AdminHost) {
    this.#api = api;
    this.#host = host;
  }

  // -- the root's delegation ---------------------------------------------------------------------

  /** A surface was opened: the first open loads the reads. */
  open(surface: string): void {
    if (surface === "assist" && this.#assist.phase === "loading") void this.#loadAssist();
    if (surface === "catalog" && this.#catalog.phase === "idle") void this.#loadCatalogFacts();
    if (surface === "keys" && this.#keys.phase === "loading") void this.#loadKeys();
  }

  /**
   * One press on a control the screens ride a data-action name with. Answers
   * true when the press was this controller's, so the root stops walking its
   * own cases.
   */
  click(action: string, control: Element | undefined): boolean {
    if (action === "reload-assist") {
      void this.#loadAssist();
      return true;
    }
    if (action === "reload-keys") {
      void this.#loadKeys();
      return true;
    }
    if (action === "assist-edit") {
      this.#armAssistEdit(
        control?.getAttribute("data-budget-kind") ?? "",
        control?.getAttribute("data-budget-id") ?? "",
      );
      return true;
    }
    if (action === "assist-cancel-edit") {
      this.#assist = { ...this.#assist, editing: null, editError: "" };
      this.#host.repaint();
      return true;
    }
    if (action === "assist-save") {
      void this.#saveAssistBudget(
        control?.getAttribute("data-budget-kind") ?? "",
        control?.getAttribute("data-budget-id") ?? "",
      );
      return true;
    }
    if (action === "assist-reset-limit") {
      void this.#resetAssistUserLimit(control?.getAttribute("data-user-id") ?? "");
      return true;
    }
    if (action === "print-find") {
      void this.#findPrintJobs();
      return true;
    }
    if (action === "wb-download") {
      void this.#downloadWorkbook();
      return true;
    }
    if (action === "msg-export") {
      void this.#downloadMessages();
      return true;
    }
    if (action === "catalog-find") {
      void this.#findProducts();
      return true;
    }
    if (action === "catalog-open") {
      void this.#openProduct(control?.getAttribute("data-product-id") ?? "");
      return true;
    }
    if (action === "catalog-save") {
      void this.#saveProduct();
      return true;
    }
    if (action === "catalog-image-set") {
      void this.#setImage();
      return true;
    }
    if (action === "catalog-image-remove") {
      void this.#removeImage();
      return true;
    }
    return false;
  }

  /**
   * A checkbox or file input answered. Answers true when the change was this
   * controller's.
   */
  change(target: Element): boolean {
    const action = target.getAttribute("data-action") ?? "";
    if (action === "assist-setting") {
      const key = target.getAttribute("data-key") ?? "";
      const checked = target instanceof HTMLInputElement ? target.checked : false;
      void this.#putAssistSetting(key, checked);
      return true;
    }
    if (action === "catalog-image-file") {
      const file = target instanceof HTMLInputElement && target.type === "file" ? (target.files?.[0] ?? null) : null;
      this.#pickedImage = file;
      this.#catalog = { ...this.#catalog, pickedName: file?.name ?? "" };
      this.#host.repaint();
      return true;
    }
    return false;
  }

  /** A field's words moved. Answers true when the field was this controller's. */
  input(field: string, value: string): boolean {
    if (field === "print-doc-type") {
      this.#print = { ...this.#print, docType: value };
      return true;
    }
    if (field === "print-ref") {
      this.#print = { ...this.#print, ref: value };
      return true;
    }
    const wb = (key: keyof WorkbookModel): boolean => {
      this.#workbook = { ...this.#workbook, [key]: value };
      return true;
    };
    const msg = (key: keyof MessagesModel): boolean => {
      this.#messages = { ...this.#messages, [key]: value };
      return true;
    };
    if (field === "wb-entity-type") return wb("entityType");
    if (field === "wb-action") return wb("action");
    if (field === "wb-actor") return wb("actorId");
    if (field === "wb-since") return wb("since");
    if (field === "msg-channel") return msg("channelId");
    if (field === "msg-user") return msg("userId");
    if (field === "msg-since") return msg("since");
    if (field === "msg-until") return msg("until");
    if (field === "catalog-q") {
      this.#catalog = { ...this.#catalog, q: value };
      return true;
    }
    if (field === "catalog-alt") {
      this.#catalog = { ...this.#catalog, altText: value };
      return true;
    }
    return false;
  }

  /** The fields and meters the screens bind no value into are mirrored here. */
  mirror(): void {
    // The edit box: seeded from the row's own number when the edit armed, and
    // kept on the glass through the reloads that follow a save.
    if (this.#assist.editing !== null) {
      const current = this.#editSeed();
      this.#host.setField("assist-budget", current);
    }
    // The two setting toggles ride the server's answer, so a refused write
    // snaps back and a fresh read lands as the server now says.
    const settings = this.#assist.settings;
    this.#host.setChecked("assist-toggle-spend_visible_to_non_admin", settings?.spend_visible_to_non_admin === true);
    this.#host.setChecked("assist-toggle-chat_enabled", settings?.chat_enabled === true);
    // The meters' fills: one per spend row.
    const users = this.#assist.report?.users ?? [];
    for (const row of users) {
      this.#host.meter(row.user_id, usedFraction(spendTotal(row), row.budget.tokens));
    }
    // The print form's fields: the paper's select and the reference field
    // mirror the model, so a refused search leaves the person's words up.
    this.#host.setField("print-doc-type", this.#print.docType);
    this.#host.setField("print-ref", this.#print.ref);
    // The exports' filter fields, the audit trail's own mirror rule.
    this.#host.setField("wb-entity-type", this.#workbook.entityType);
    this.#host.setField("wb-action", this.#workbook.action);
    this.#host.setField("wb-actor", this.#workbook.actorId);
    this.#host.setField("wb-since", this.#workbook.since);
    this.#host.setField("msg-channel", this.#messages.channelId);
    this.#host.setField("msg-user", this.#messages.userId);
    this.#host.setField("msg-since", this.#messages.since);
    this.#host.setField("msg-until", this.#messages.until);
    // The catalog's fields: the finder's words and the alt words mirror the
    // model; the editor's twelve fields seed here when a row just opened or
    // a save landed, the one pass where the fields are on the glass.
    this.#host.setField("catalog-q", this.#catalog.q);
    this.#host.setField("catalog-alt", this.#catalog.altText);
    if (this.#seedPending && this.#catalog.opened !== null) {
      this.#seedPending = false;
      this.#seedEditor(this.#catalog.opened);
    }
  }

  /** The active surface's template, for the root's router line. */
  view(surface: string): TemplateResult | null {
    if (surface === "assist") return renderAssist(this.#assist);
    if (surface === "printing") return renderPrint(this.#print);
    if (surface === "exports") return renderExports(this.#workbook, this.#messages);
    if (surface === "catalog") return renderCatalog(this.#catalog);
    if (surface === "keys") return renderKeys(this.#keys);
    return null;
  }

  /** A sign out empties every surface, so the next sign in starts fresh. */
  signOut(): void {
    this.#assist = emptyAssist();
    this.#print = emptyPrint();
    this.#workbook = emptyWorkbook();
    this.#messages = emptyMessages();
    this.#catalog = emptyCatalog();
    this.#pickedImage = null;
    this.#seedPending = false;
    this.#keys = emptyKeys();
  }

  // -- the assist surface ------------------------------------------------------------------------

  #editSeed(): string {
    const editing = this.#assist.editing;
    if (editing === null) return "";
    if (editing.kind === "role") {
      return String(this.#assist.limits?.role_budgets[editing.code] ?? 0);
    }
    const row = (this.#assist.report?.users ?? []).find((u) => u.user_id === editing.id);
    return String(row?.budget.tokens ?? 0);
  }

  #armAssistEdit(kind: string, id: string): void {
    if (kind !== "user" && kind !== "role") return;
    if (id === "") return;
    const editing: AssistEditTarget = kind === "role" ? { kind: "role", code: id } : { kind: "user", id };
    this.#assist = { ...this.#assist, editing, editError: "", notice: "" };
    this.#host.repaint();
  }

  async #loadAssist(): Promise<void> {
    // The save's notice outlives the re-read that follows it, as the hotkeys'
    // save does: the reload answers the read, not the write.
    this.#assist = { ...emptyAssist(), notice: this.#assist.notice };
    this.#host.repaint();
    // Three reads, one screen: the spend report is the headline read and its
    // refusal is the screen's phase; the limits and the settings failing only
    // cost their panels their own line.
    const [spend, limits, settings] = await Promise.all([
      this.#api.assistSpend(),
      this.#api.assistLimits(),
      this.#api.assistSettings(),
    ]);
    if (!spend.ok) {
      if (spend.status === 403) {
        this.#assist = { ...this.#assist, phase: "refused", error: assistRefusalWords(spend.error, spend.status) };
      } else {
        this.#assist = {
          ...this.#assist,
          phase: "error",
          error: `The assistant's month did not load. ${assistRefusalWords(spend.error, spend.status)}`,
        };
      }
      this.#host.repaint();
      return;
    }
    this.#assist = {
      ...this.#assist,
      phase: "ready",
      report: spend.value,
      limits: limits.ok ? limits.value : null,
      settings: settings.ok ? settings.value : null,
    };
    this.#host.repaint();
  }

  async #saveAssistBudget(kind: string, id: string): Promise<void> {
    if (this.#assist.busy) return;
    const tokens = wholeAmount(this.#host.field("assist-budget"));
    if (tokens === null) {
      this.#assist = { ...this.#assist, editError: assistBudgetInputWords() };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: true, editError: "", notice: "" };
    this.#host.repaint();
    const result =
      kind === "role" ? await this.#api.putRoleLimit(id, tokens) : await this.#api.putUserLimit(id, tokens);
    if (!result.ok) {
      // The server's own sentence is the message: a 4xx arrives in words.
      this.#assist = {
        ...this.#assist,
        busy: false,
        editError: assistRefusalWords(result.error, result.status),
      };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: false, editing: null, notice: "Saved." };
    this.#host.repaint();
    await this.#loadAssist();
  }

  async #resetAssistUserLimit(id: string): Promise<void> {
    if (id === "" || this.#assist.busy) return;
    this.#assist = { ...this.#assist, busy: true, editError: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.resetUserLimit(id);
    if (!result.ok) {
      this.#assist = {
        ...this.#assist,
        busy: false,
        editError: assistRefusalWords(result.error, result.status),
      };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: false, notice: "Saved. The person's role budget applies again." };
    this.#host.repaint();
    await this.#loadAssist();
  }

  async #putAssistSetting(key: string, checked: boolean): Promise<void> {
    if (key === "" || this.#assist.busy) return;
    this.#assist = { ...this.#assist, busy: true, editError: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.putSetting(key, checked ? "true" : "false");
    if (!result.ok) {
      this.#assist = {
        ...this.#assist,
        busy: false,
        editError: assistRefusalWords(result.error, result.status),
      };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: false, notice: "Saved." };
    this.#host.repaint();
    await this.#loadAssist();
  }

  // -- the print jobs surface ---------------------------------------------------------------------

  /**
   * One document's history, on the person's own words. An empty ref asks for
   * nothing; a refusal keeps the form's words on the glass and names the
   * server's own reason.
   */
  async #findPrintJobs(): Promise<void> {
    if (this.#print.busy) return;
    const docType = printDocTypeFromLabel(this.#print.docType);
    const ref = this.#print.ref.trim();
    if (ref === "") {
      // The hotkeys' own empty draft guard: a question with no reference is
      // named beside the form, and no call leaves.
      this.#print = { ...this.#print, error: "Give the document's reference to look up its print history." };
      this.#host.repaint();
      return;
    }
    this.#print = { ...this.#print, phase: "ready", busy: true, rows: [], error: "" };
    this.#host.repaint();
    const result = await this.#api.printJobs(docType, ref);
    if (!result.ok) {
      // The server's own words: an unknown paper names the paper, an unknown
      // document is the bare not found. No paraphrase, no invention.
      this.#print = { ...this.#print, busy: false, error: serverWords(result.error) };
      this.#host.repaint();
      return;
    }
    this.#print = { ...this.#print, busy: false, rows: result.value, error: "" };
    this.#host.repaint();
  }

  // -- the exports surface -------------------------------------------------------------------------

  /** The audit workbook: one call, the bytes to the person's downloads. */
  async #downloadWorkbook(): Promise<void> {
    if (this.#workbook.busy) return;
    const w = this.#workbook;
    const query = {
      entity_type: w.entityType.trim() === "" ? undefined : w.entityType.trim(),
      action: w.action === "" ? undefined : w.action,
      actor_id: w.actorId.trim() === "" ? undefined : w.actorId.trim(),
      since: sinceWire(w.since) === "" ? undefined : sinceWire(w.since),
    };
    this.#workbook = { ...w, busy: true, error: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.auditWorkbook(query);
    if (!result.ok) {
      this.#workbook = { ...this.#workbook, busy: false, error: serverWords(result.error) };
      this.#host.repaint();
      return;
    }
    this.#host.download(result.value, auditWorkbookName(), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet");
    this.#workbook = {
      ...this.#workbook,
      busy: false,
      notice: `The workbook downloaded: ${auditWorkbookName()}, ${result.value.byteLength} bytes.`,
    };
    this.#host.repaint();
  }

  /** The house's messages: one call, the rows to the file, the file to the downloads. */
  async #downloadMessages(): Promise<void> {
    if (this.#messages.busy) return;
    const m = this.#messages;
    const query = {
      channel_id: m.channelId.trim() === "" ? undefined : m.channelId.trim(),
      user_id: m.userId.trim() === "" ? undefined : m.userId.trim(),
      since: sinceWire(m.since) === "" ? undefined : sinceWire(m.since),
      until: sinceWire(m.until) === "" ? undefined : sinceWire(m.until),
    };
    this.#messages = { ...m, busy: true, error: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.messagingExport(query);
    if (!result.ok) {
      this.#messages = {
        ...this.#messages,
        busy: false,
        error: messagesRefusalWords(result.error, result.status),
      };
      this.#host.repaint();
      return;
    }
    const file = new TextEncoder().encode(JSON.stringify({ messages: result.value }, null, 2));
    this.#host.download(file, messagesExportName(), "application/json");
    this.#messages = {
      ...this.#messages,
      busy: false,
      asked: true,
      rows: result.value,
      notice: `The export downloaded: ${messagesExportName()}, ${result.value.length} messages.`,
    };
    this.#host.repaint();
  }

  // -- the catalog surface ---------------------------------------------------------------------------

  /** The editor's twelve fields, read off the glass at save time. */
  #editorDraft(): Record<string, string> {
    const draft: Record<string, string> = {};
    for (const f of PRODUCT_TEXT_FIELDS) {
      draft[f.key] = this.#host.field(`pf-${f.key.replace(/_/g, "-")}`);
    }
    return draft;
  }

  /** The editor's fields seeded from a row, when it opens and when a save lands. */
  #seedEditor(row: object): void {
    const draft = productDraftFrom(row);
    for (const key of Object.keys(draft)) {
      this.#host.setField(`pf-${key.replace(/_/g, "-")}`, draft[key]);
    }
  }

  /** The unit catalogue and the categories: the surface's two staff reads, once. */
  async #loadCatalogFacts(): Promise<void> {
    this.#catalog = { ...this.#catalog, phase: "loading" };
    this.#host.repaint();
    const [units, categories] = await Promise.all([this.#api.productUnits(), this.#api.productCategories()]);
    this.#catalog = {
      ...this.#catalog,
      phase: "idle",
      units: units.ok ? units.value : [],
      categories: categories.ok ? categories.value : [],
    };
    this.#host.repaint();
  }

  async #findProducts(): Promise<void> {
    if (this.#catalog.busy) return;
    const q = this.#catalog.q.trim();
    if (q === "") {
      this.#catalog = { ...this.#catalog, phase: "idle", error: "" };
      this.#host.repaint();
      return;
    }
    this.#catalog = { ...this.#catalog, busy: true, error: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.productSearch(q);
    if (!result.ok) {
      this.#catalog = { ...this.#catalog, busy: false, phase: "ready", error: serverWords(result.error) };
      this.#host.repaint();
      return;
    }
    this.#catalog = { ...this.#catalog, busy: false, phase: "ready", hits: result.value, error: "" };
    this.#host.repaint();
  }

  async #openProduct(id: string): Promise<void> {
    if (id === "" || this.#catalog.busy) return;
    this.#catalog = { ...this.#catalog, busy: true, error: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.productGet(id);
    if (!result.ok) {
      this.#catalog = { ...this.#catalog, busy: false, error: productRefusalWords(result.error, result.status) };
      this.#host.repaint();
      return;
    }
    this.#catalog = { ...this.#catalog, busy: false, opened: result.value, pickedName: "" };
    this.#pickedImage = null;
    this.#seedPending = true;
    this.#host.repaint();
  }

  async #saveProduct(): Promise<void> {
    const opened = this.#catalog.opened;
    if (opened === null || this.#catalog.busy) return;
    const patch = productChangedPatch(this.#editorDraft(), opened);
    if (Object.keys(patch).length === 0) {
      this.#catalog = { ...this.#catalog, error: "", notice: "Nothing changed yet." };
      this.#host.repaint();
      return;
    }
    this.#catalog = { ...this.#catalog, busy: true, error: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.productUpdate(opened.id, patch);
    if (!result.ok) {
      // The server's own words: an uneditable column is named by the column.
      this.#catalog = { ...this.#catalog, busy: false, error: productRefusalWords(result.error, result.status) };
      this.#host.repaint();
      return;
    }
    this.#catalog = { ...this.#catalog, busy: false, opened: result.value, notice: "Saved." };
    this.#seedPending = true;
    this.#host.repaint();
    await this.#findProductsQuietly();
  }

  /** The list re-reads after a save, keeping the person on the same question. */
  async #findProductsQuietly(): Promise<void> {
    const q = this.#catalog.q.trim();
    if (q === "") return;
    const result = await this.#api.productSearch(q);
    if (result.ok) this.#catalog = { ...this.#catalog, hits: result.value };
    this.#host.repaint();
  }

  async #setImage(): Promise<void> {
    const opened = this.#catalog.opened;
    const file = this.#pickedImage;
    if (opened === null || file === null || this.#catalog.busy) return;
    const type = file.type === "" ? "" : file.type;
    if (type !== "image/jpeg" && type !== "image/png" && type !== "image/webp") {
      this.#catalog = { ...this.#catalog, error: imageTypeWords() };
      this.#host.repaint();
      return;
    }
    this.#catalog = { ...this.#catalog, busy: true, error: "", notice: "" };
    this.#host.repaint();
    const bytes = new Uint8Array(await file.arrayBuffer());
    const result = await this.#api.productPutImage(opened.id, bytes, type, this.#catalog.altText.trim());
    if (!result.ok) {
      this.#catalog = { ...this.#catalog, busy: false, error: productRefusalWords(result.error, result.status) };
      this.#host.repaint();
      return;
    }
    this.#catalog = {
      ...this.#catalog,
      busy: false,
      notice: `The picture landed: ${result.value.byte_size} bytes.`,
      pickedName: "",
    };
    this.#pickedImage = null;
    this.#host.repaint();
  }

  async #removeImage(): Promise<void> {
    const opened = this.#catalog.opened;
    if (opened === null || this.#catalog.busy) return;
    this.#catalog = { ...this.#catalog, busy: true, error: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.productDeleteImage(opened.id);
    if (!result.ok) {
      this.#catalog = { ...this.#catalog, busy: false, error: productRefusalWords(result.error, result.status) };
      this.#host.repaint();
      return;
    }
    this.#catalog = { ...this.#catalog, busy: false, notice: "The picture was removed." };
    this.#host.repaint();
  }

  // -- the platform keys surface ----------------------------------------------------------------------

  /**
   * The roster read, once per visit. A 404 is the waits state, not an error:
   * the read is contract-marked for this surface and the system does not
   * serve it yet, which is a fact about the system, not a failed read.
   */
  async #loadKeys(): Promise<void> {
    this.#keys = emptyKeys();
    this.#host.repaint();
    const result = await this.#api.platformKeys();
    if (!result.ok) {
      if (result.status === 404) {
        this.#keys = { phase: "waits", error: "", rows: [] };
      } else {
        this.#keys = {
          phase: "error",
          error: `The platform's key roster did not load. ${serverWords(result.error)}`,
          rows: [],
        };
      }
      this.#host.repaint();
      return;
    }
    this.#keys = { phase: "ready", error: "", rows: result.value };
    this.#host.repaint();
  }
}

/**
 * A server error a person reads, for the reads whose refusals are already
 * sentences (the print queue names its paper). A one word token is not a
 * sentence; it becomes plain words. The same rule errorWords carries; apps do
 * not import apps, so it lives here too.
 */
function serverWords(error: string): string {
  const trimmed = error.trim();
  if (trimmed === "" || !trimmed.includes(" ")) return "Try again in a moment.";
  return trimmed;
}

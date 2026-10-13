// The receivables office's controller: the state its screens draw from, the loads, and what each press does. It
// sits beside the receivables desk (ar-desk.ts) the same way the ledger's controller sits beside it: not an element,
// handed a way to repaint, to read and set form fields and to say a message, and asked for a model to draw.
//
// Every write is held to the server: nothing here changes what the screen shows until the core has answered, and
// the screen then shows the core's answer. A refusal reads in words (office-logic.ts), a walled move in words naming
// who may do it. Each write that carries a key sends one key per attempt and the same key again on a retry whose
// answer was lost. No address or number a customer gave is ever put on screen.
import type { BillingAPContactRead, BillingStatementRunResult, Result } from "@hardscapehouse/api-client";
import { OfficeApi } from "./office-api.js";
import { newKey } from "./held-keys.js";
import { AttemptKeys, officeRefusal, validateApContact } from "./office-logic.js";

export interface OfficeHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** Brings the first element matching the selector into view and focuses it. */
  readonly reveal: (selector: string) => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string, key?: string) => string;
  /** Sets a form field's text; the screens bind no value, so a fill goes through here. */
  readonly setField: (name: string, value: string, key?: string) => void;
  /** Says a message in the desk's notice banner (and clears the error). */
  readonly notice: (words: string) => void;
  /** Says a refusal in the desk's error banner (and clears the notice). */
  readonly error: (words: string) => void;
}

export interface OfficeWho {
  readonly userId: string;
  readonly roleCode: string;
  readonly userName: string;
}

export type RunView =
  | { readonly phase: "none" }
  | { readonly phase: "loading" }
  | { readonly phase: "error"; readonly words: string }
  | { readonly phase: "ready"; readonly result: BillingStatementRunResult };

export interface OfficeModel {
  readonly busy: boolean;
  readonly run: RunView;
  /** The AP contact read for each account asked about: the read, or "error" when it could not be read. */
  readonly contacts: ReadonlyMap<string, BillingAPContactRead | "error">;
  /** The account whose contact is being removed and waits for the second press. */
  readonly removing: string | null;
}

export class OfficeDesk {
  readonly #api: OfficeApi;
  readonly #host: OfficeHost;
  readonly #who: OfficeWho;
  readonly #keys = new AttemptKeys(newKey);

  #busy = false;
  #run: RunView = { phase: "none" };
  #contacts = new Map<string, BillingAPContactRead | "error">();
  #removing: string | null = null;
  /** The account whose name field the next paint must fill from its contact. */
  #fillContactFor: string | null = null;

  constructor(api: OfficeApi, host: OfficeHost, who: OfficeWho) {
    this.#api = api;
    this.#host = host;
    this.#who = who;
  }

  get who(): OfficeWho {
    return this.#who;
  }

  model(): OfficeModel {
    return { busy: this.#busy, run: this.#run, contacts: this.#contacts, removing: this.#removing };
  }

  #fail(action: string, result: Extract<Result<unknown>, { ok: false }>): void {
    this.#host.error(officeRefusal(action, result.error, result.status));
  }

  // ---- statement runs ----------------------------------------------------------------------------------------

  /** Reads a run back by its id (the server's stored run, not the answer to the press) and shows it. */
  async showRun(id: string): Promise<void> {
    this.#run = { phase: "loading" };
    this.#host.repaint();
    const got = await this.#api.statementRun(id);
    if (!got.ok) {
      this.#run = { phase: "error", words: officeRefusal("statement-run", got.error, got.status) };
      this.#host.repaint();
      return;
    }
    this.#run = { phase: "ready", result: got.value };
    this.#host.repaint();
    await Promise.all(got.value.statements.map((s) => this.ensureContact(s.customer_id)));
  }

  // ---- AP contacts -------------------------------------------------------------------------------------------

  /** Reads an account's AP contact once; a later call finds it kept. */
  async ensureContact(customerId: string, force = false): Promise<void> {
    if (customerId === "" || (!force && this.#contacts.has(customerId))) return;
    const got = await this.#api.apContact(customerId);
    this.#contacts.set(customerId, got.ok ? got.value : "error");
    if (got.ok) this.#fillContactFor = customerId;
    this.#host.repaint();
  }

  async #saveContact(customerId: string): Promise<void> {
    if (this.#busy) return;
    const check = validateApContact({
      name: this.#host.field("contact-name"),
      email: this.#host.field("contact-email"),
      phone: this.#host.field("contact-phone"),
    });
    if (!check.ok) {
      this.#host.error(check.words);
      this.#host.reveal("[data-error]");
      return;
    }
    this.#busy = true;
    this.#removing = null;
    this.#host.repaint();
    const result = await this.#api.putApContact(customerId, check.contact);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("contact", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#contacts.set(customerId, result.value);
    this.#host.setField("contact-email", "");
    this.#host.setField("contact-phone", "");
    this.#host.notice("The AP contact is saved. Statements, reminders and invoices for this account go to it through the messaging sink.");
    this.#host.reveal("[data-notice]");
    this.#host.repaint();
  }

  async #removeContact(customerId: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.deleteApContact(customerId);
    this.#busy = false;
    this.#removing = null;
    if (!result.ok) {
      this.#fail("contact", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#contacts.set(customerId, result.value);
    this.#host.setField("contact-name", "");
    this.#host.notice("The AP contact is removed. Nothing will be queued for this account until one is tagged.");
    this.#host.reveal("[data-notice]");
    this.#host.repaint();
  }

  // ---- the press ---------------------------------------------------------------------------------------------

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    switch (action) {
      case "contact-save":
        void this.#saveContact(attr("data-customer-id"));
        return true;
      case "contact-remove":
        this.#removing = attr("data-customer-id");
        this.#host.repaint();
        return true;
      case "contact-remove-cancel":
        this.#removing = null;
        this.#host.repaint();
        return true;
      case "contact-remove-confirm":
        void this.#removeContact(attr("data-customer-id"));
        return true;
      default:
        return false;
    }
  }

  /** After each paint: a contact's name fills its field once the contact has been read. */
  afterRender(): void {
    const id = this.#fillContactFor;
    if (id === null) return;
    const read = this.#contacts.get(id);
    this.#fillContactFor = null;
    if (read !== undefined && read !== "error" && read.tagged && read.contact !== undefined) this.#host.setField("contact-name", read.contact.contact_name);
  }

  /** Forgets what was read about accounts: a sign out and a sign in of another user starts clean. */
  clear(): void {
    this.#contacts.clear();
    this.#run = { phase: "none" };
    this.#removing = null;
  }
}

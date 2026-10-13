// The vendors desk's controller: the state the screens draw from, the loads, and what each press
// does. It is not an element; the root owns it, hands it a way to repaint and a way to read the
// form fields, and asks it for the template.
//
// Every write is held to the server: a vendor exists only when the core made it, and after a write
// the screen shows what the core answered and the list it now holds, never what was expected. A
// check the core also makes is made first, so a person reads the sentence beside the field; the
// core's own refusal is shown as it words it. Removing a contact or a code stands behind a confirm
// step naming it. Nothing is stored.
import type { TemplateResult } from "lit";
import type { Product, PurchasingPO, PurchasingVendor, PurchasingVendorContactResult, PurchasingVendorSKU, Result } from "@hardscapehouse/api-client";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { refusalWords } from "../po/po-logic.js";
import { answerLost } from "../purch/purch-logic.js";
import { VendorsApi } from "./vendors-api.js";
import { contactBody, mappingBody, refWords, vendorBody, vendorRows } from "./vendors-logic.js";
import { renderVendors, type Removal, type VendorTab } from "./vendors-views.js";

type Failure = Extract<Result<unknown>, { ok: false }>;

export class VendorsDesk implements AreaDesk {
  readonly #api: VendorsApi;
  readonly #host: DeskHost;

  #phase: "loading" | "ready" | "error" = "loading";
  #vendors: readonly PurchasingVendor[] = [];
  #query = "";
  #showInactive = false;
  #selected: PurchasingVendor | "new" | null = null;
  #tab: VendorTab = "details";
  #savedEmail: string | null = null;
  #clearEmail = false;
  #contact: PurchasingVendorContactResult | null = null;
  #codes: readonly PurchasingVendorSKU[] | null = null;
  #products: readonly Product[] = [];
  #editingCode: string | null = null;
  #removal: Removal = null;
  #removalCode: PurchasingVendorSKU | null = null;
  #lookup: PurchasingPO | null = null;
  #lookupError = "";
  #busy = false;
  #error = "";
  #notice = "";
  // What the screen has already been given to seed, so a field is set once per change and never over a person's typing.
  #seedDetails = "";
  #seedContact = "";
  #seedCode = "";
  #version = 0;

  constructor(api: VendorsApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
  }

  // ---- loads ----------------------------------------------------------------------------------

  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const vendors = await this.#api.list();
    if (!vendors.ok) {
      this.#phase = "error";
      this.#error = refusalWords("load", vendors.error, vendors.status);
      this.#host.repaint();
      return;
    }
    this.#vendors = vendorRows(vendors.value);
    this.#phase = "ready";
    this.#host.repaint();
  }

  async #reloadVendors(): Promise<void> {
    const vendors = await this.#api.list();
    if (vendors.ok) {
      this.#vendors = vendorRows(vendors.value);
      if (this.#selected !== null && this.#selected !== "new") {
        const id = this.#selected.id;
        this.#selected = this.#vendors.find((v) => v.id === id) ?? this.#selected;
      }
    } else this.#fail(vendors);
  }

  async #loadContact(id: string): Promise<void> {
    const got = await this.#api.contact(id);
    if (this.#currentId() !== id) return;
    if (got.ok) this.#contact = got.value;
    else this.#fail(got);
    this.#version += 1;
  }

  async #loadCodes(id: string): Promise<void> {
    const [codes, products] = await Promise.all([this.#api.codes(id), this.#products.length === 0 ? this.#api.products() : Promise.resolve(null)]);
    if (this.#currentId() !== id) return;
    if (codes.ok) this.#codes = codes.value.items ?? [];
    else this.#fail(codes);
    if (products !== null && products.ok) this.#products = products.value.items ?? [];
  }

  #currentId(): string {
    return this.#selected === null || this.#selected === "new" ? "" : this.#selected.id;
  }

  // ---- the press ------------------------------------------------------------------------------

  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const choice = control.getAttribute("data-choice") ?? "";
    const vendorId = control.getAttribute("data-vendor-id") ?? "";
    const productId = control.getAttribute("data-product-id") ?? "";
    switch (action) {
      case "reload":
        void this.load();
        return true;
      case "toggle-inactive":
        this.#showInactive = !this.#showInactive;
        this.#host.repaint();
        return true;
      case "new-vendor":
        this.#select("new");
        return true;
      case "pick-vendor":
        this.#select(this.#vendors.find((v) => v.id === vendorId) ?? null);
        return true;
      case "close-vendor":
        this.#select(null);
        return true;
      case "vendor-tab":
        this.#openTab(choice === "contact" ? "contact" : choice === "codes" ? "codes" : "details");
        return true;
      case "toggle-clear-email":
        this.#clearEmail = !this.#clearEmail;
        this.#host.repaint();
        return true;
      case "save-vendor":
        void this.#saveVendor();
        return true;
      case "toggle-active":
        void this.#toggleActive();
        return true;
      case "save-contact":
        void this.#saveContact();
        return true;
      case "ask-remove-contact":
        this.#removal = "contact";
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "do-remove-contact":
        void this.#removeContact();
        return true;
      case "add-code":
        this.#editingCode = "new";
        this.#seedCode = "";
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "edit-code":
        this.#editingCode = productId;
        this.#seedCode = "";
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "close-code":
        this.#editingCode = null;
        this.#error = "";
        this.#host.repaint();
        return true;
      case "save-code":
        void this.#saveCode();
        return true;
      case "ask-remove-code":
        this.#removalCode = this.#codes?.find((k) => k.product_id === productId) ?? null;
        this.#removal = this.#removalCode === null ? null : "code";
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "do-remove-code":
        void this.#removeCode();
        return true;
      case "confirm-cancel":
        this.#removal = null;
        this.#removalCode = null;
        this.#host.repaint();
        return true;
      case "find-order":
        void this.#findOrder();
        return true;
      default:
        return false;
    }
  }

  typed(name: string, value: string): void {
    if (name !== "v-query") return;
    this.#query = value;
    this.#host.repaint();
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  #fail(result: Failure): void {
    this.#notice = "";
    this.#error = refusalWords("vendor", result.error, result.status);
  }

  /**
   * A write that was refused reads as the core words it; a write whose answer never came, or came as a
   * server fault, may have been made (a vendor create has no key and the core mints the number), so the
   * screen reads the vendors, contact and codes again and says it is not known, never "nothing changed".
   */
  async #failWrite(result: Failure): Promise<void> {
    this.#fail(result);
    if (!answerLost(result)) return;
    const id = this.#currentId();
    await this.#reloadVendors();
    if (id !== "") {
      if (this.#contact !== null) await this.#loadContact(id);
      if (this.#codes !== null) await this.#loadCodes(id);
    }
    this.#error = "No clear answer came back, so it is not known whether the change was made. The vendors have been read again: check them before saving a second time.";
  }

  #select(vendor: PurchasingVendor | "new" | null): void {
    this.#selected = vendor;
    this.#tab = "details";
    this.#savedEmail = null;
    this.#clearEmail = false;
    this.#contact = null;
    this.#codes = null;
    this.#editingCode = null;
    this.#removal = null;
    this.#removalCode = null;
    this.#clearMessages();
    this.#seedDetails = "";
    this.#seedContact = "";
    this.#version += 1;
    this.#host.repaint();
  }

  #openTab(tab: VendorTab): void {
    const id = this.#currentId();
    if (id === "") return;
    this.#tab = tab;
    this.#editingCode = null;
    this.#removal = null;
    this.#clearMessages();
    if (tab === "contact" && this.#contact === null) void this.#loadContact(id).then(() => this.#host.repaint());
    if (tab === "codes" && this.#codes === null) void this.#loadCodes(id).then(() => this.#host.repaint());
    this.#seedDetails = "";
    this.#seedContact = "";
    this.#host.repaint();
  }

  // ---- the writes -----------------------------------------------------------------------------

  async #saveVendor(): Promise<void> {
    const vendor = this.#selected;
    if (vendor === null || this.#busy) return;
    const checked = vendorBody(
      { name: this.#host.field("v-name"), terms: this.#host.field("v-terms"), email: this.#host.field("v-email"), clearEmail: this.#clearEmail },
      vendor === "new" ? null : vendor,
    );
    if (!checked.ok) {
      this.#error = checked.words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = vendor === "new" ? await this.#api.create({ ...checked.body, name: checked.body.name ?? "" }) : await this.#api.update(vendor.id, checked.body);
    if (done.ok) {
      const saved = done.value;
      await this.#reloadVendors();
      const listed = this.#vendors.find((v) => v.id === saved.id) ?? null;
      this.#selected = listed;
      this.#savedEmail = saved.order_email;
      this.#clearEmail = false;
      this.#seedDetails = "";
      this.#version += 1;
      this.#notice = vendor === "new" ? `Added ${saved.name}.\nIts number is ${saved.vendor_no}.` : `Saved changes to ${saved.name}.`;
    } else await this.#failWrite(done);
    this.#busy = false;
    this.#host.repaint();
  }

  async #toggleActive(): Promise<void> {
    const vendor = this.#selected;
    if (vendor === null || vendor === "new" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.update(vendor.id, { is_active: !vendor.is_active });
    if (done.ok) {
      await this.#reloadVendors();
      this.#notice = done.value.is_active ? `${done.value.name} is active again.` : `${done.value.name} is inactive: it stays on its past orders.`;
    } else await this.#failWrite(done);
    this.#busy = false;
    this.#host.repaint();
  }

  async #saveContact(): Promise<void> {
    const id = this.#currentId();
    if (id === "" || this.#busy) return;
    const checked = contactBody({ name: this.#host.field("c-name"), email: this.#host.field("c-email"), phone: this.#host.field("c-phone") });
    if (!checked.ok) {
      this.#error = checked.words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.setContact(id, checked.body);
    if (done.ok) {
      this.#contact = done.value;
      this.#seedContact = "";
      this.#version += 1;
      this.#notice = "The ordering contact is tagged.\nOrders to this vendor go to it.";
    } else await this.#failWrite(done);
    this.#busy = false;
    this.#host.repaint();
  }

  async #removeContact(): Promise<void> {
    const id = this.#currentId();
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.removeContact(id);
    if (done.ok) {
      this.#contact = done.value;
      this.#seedContact = "";
      this.#version += 1;
      this.#notice = "The ordering contact is removed.";
    } else await this.#failWrite(done);
    this.#removal = null;
    this.#busy = false;
    this.#host.repaint();
  }

  async #saveCode(): Promise<void> {
    const id = this.#currentId();
    if (id === "" || this.#editingCode === null || this.#busy) return;
    const fixed = this.#editingCode !== "new";
    const product = fixed ? undefined : this.#products.find((p) => `${p.sku} ${p.name}` === this.#host.field("m-product"));
    const checked = mappingBody({
      productId: fixed ? this.#editingCode : (product?.id ?? ""),
      code: this.#host.field("m-code"),
      unit: this.#host.field("m-unit"),
      factor: this.#host.field("m-factor"),
    });
    if (!checked.ok) {
      this.#error = checked.words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.setCode(id, checked.body);
    if (done.ok) {
      await this.#loadCodes(id);
      this.#editingCode = null;
      this.#notice = "The vendor code is saved.";
    } else await this.#failWrite(done);
    this.#busy = false;
    this.#host.repaint();
  }

  async #removeCode(): Promise<void> {
    const id = this.#currentId();
    const code = this.#removalCode;
    if (id === "" || code === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.removeCode(id, code.product_id);
    if (done.ok) {
      await this.#loadCodes(id);
      this.#notice = "The vendor code is removed.";
    } else await this.#failWrite(done);
    this.#removal = null;
    this.#removalCode = null;
    this.#busy = false;
    this.#host.repaint();
  }

  async #findOrder(): Promise<void> {
    if (this.#busy) return;
    const ref = this.#host.field("v-ref");
    const words = refWords(ref);
    this.#lookup = null;
    if (words !== "") {
      this.#lookupError = words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#lookupError = "";
    this.#host.repaint();
    const found = await this.#api.orderByRef(ref);
    if (found.ok) this.#lookup = found.value;
    else this.#lookupError = refusalWords("vendor", found.error, found.status);
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the template ---------------------------------------------------------------------------

  /** After a render: starting values for the details, the contact and the code editor, once per change. */
  seed(set: (name: string, value: string) => void): void {
    const vendor = this.#selected;
    if (vendor === null) return;
    if (this.#tab === "details") {
      const key = `d:${vendor === "new" ? "new" : vendor.id}:${this.#version}`;
      if (this.#seedDetails !== key) {
        set("v-name", vendor === "new" ? "" : vendor.name);
        set("v-terms", vendor === "new" ? "" : String(vendor.payment_terms_days));
        set("v-email", "");
        this.#seedDetails = key;
      }
    }
    if (this.#tab === "contact" && this.#contact !== null) {
      const key = `c:${this.#currentId()}:${this.#version}`;
      if (this.#seedContact !== key) {
        set("c-name", this.#contact.contact.contact_name);
        set("c-email", this.#contact.contact.email);
        set("c-phone", this.#contact.contact.phone);
        this.#seedContact = key;
      }
    }
    if (this.#tab === "codes" && this.#editingCode !== null) {
      const key = `m:${this.#currentId()}:${this.#editingCode}`;
      if (this.#seedCode !== key) {
        const existing = this.#codes?.find((k) => k.product_id === this.#editingCode);
        set("m-code", existing?.vendor_sku ?? "");
        set("m-unit", existing?.vendor_uom ?? "");
        set("m-factor", existing?.qty_per_vendor_uom === undefined || existing.qty_per_vendor_uom === 0 ? "" : String(existing.qty_per_vendor_uom));
        this.#seedCode = key;
      }
    }
  }

  render(): TemplateResult {
    return renderVendors({
      phase: this.#phase,
      vendors: this.#vendors,
      query: this.#query,
      showInactive: this.#showInactive,
      selected: this.#selected,
      tab: this.#tab,
      savedEmail: this.#savedEmail,
      clearEmail: this.#clearEmail,
      contact: this.#contact,
      codes: this.#codes,
      products: this.#products,
      editingCode: this.#editingCode,
      removal: this.#removal,
      removalCode: this.#removalCode,
      orderLookup: this.#lookup,
      lookupError: this.#lookupError,
      busy: this.#busy,
      error: this.#phase === "error" || this.#error !== "" ? this.#error : "",
      notice: this.#notice,
    });
  }
}

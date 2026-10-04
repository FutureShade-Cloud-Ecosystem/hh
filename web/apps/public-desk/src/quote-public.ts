// The customer's quote page, reached by an opaque share-link token in the URL,
// BEFORE any authentication: no session, no nav shell, no toast host, so every
// message is surfaced inline. It never attaches a credential: the page's
// client is built with an empty base URL and no header provider at all, and
// the token in the link is the credential.
//
// A port of the older product's customer quote page, never from scratch: the
// structure, the states and the words carry over, mapped onto this
// repository's design system tokens. The acceptance is a TYPED NAME
// ATTESTATION, not a signature: there is deliberately no drawing pad here, and
// what an acceptance records is stated on the page in plain words, so the
// customer knows exactly what they are giving.
//
// The page is light theme, pinned in index.html before any stylesheet: a
// customer document must look identical to everyone and must print correctly,
// so there is no theme preference to read.
import { css, html, nothing, type TemplateResult } from "lit";
import { state } from "lit/decorators.js";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles, button } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient } from "@hardscapehouse/api-client";
import {
  acceptanceAmountPhrase,
  acceptanceRefusal,
  showsImages,
  totalsBlock,
  type QuoteMoney,
} from "./_quote-public.js";
import { formatMoney, quoteStatusPhrase, withFullStop } from "./format.js";
import { envelopeOnlyFetch, PublicQuoteClient, serverWords, type AcceptanceRef, type QuoteViewRef } from "./quote-client.js";

const LINK_GONE =
  "This link is no longer valid. Quote links expire, and can also be turned off by our team. Please contact us for an up-to-date link.";
const LINK_GONE_SHORT = "This link is no longer valid. Please contact us for an up-to-date link.";
const NOT_FOUND = "We could not find this quote. Please check the link you were sent.";
const LOAD_FAILED = "We could not load this quote right now. Please try again shortly.";
const COULD_NOT_REACH = "We could not reach the server. Please try again.";
const NOT_RECORDED = "We could not record your acceptance. Please try again.";

export class QuotePublicPage extends HHElement {
  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
        /* The reference's finish: one reading column, centred, at the width
           the reference page finishes at. */
        max-width: 780px;
        margin: 0 auto;
        padding: var(--hh-s-24) var(--hh-s-16) var(--hh-s-64);
        font-family: var(--hh-font-ui);
        color: var(--hh-text);
      }
      h1 {
        font-size: 1.3rem;
        margin: 0 0 var(--hh-s-4);
      }
      /* Full text ink, NOT the dimmed one: this line sits on the page rather
         than on a card, and it is load bearing information, not decoration. */
      .sub {
        color: var(--hh-text);
        font-size: 0.88rem;
        margin-bottom: var(--hh-s-20);
      }
      .card {
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        padding: var(--hh-s-16) var(--hh-s-20);
        background: var(--hh-card);
        margin-bottom: var(--hh-s-16);
      }
      table {
        width: 100%;
        border-collapse: collapse;
        font-size: 0.88rem;
      }
      /* The phone guard's last resort: if the table's own minimum still cannot
         fit (an unusually wide face on some visitor's machine), it scrolls
         INSIDE the card rather than pushing the card past the viewport. */
      .tablewrap {
        overflow-x: auto;
      }
      /* The reading column: the item names the line and must never be crushed
         to nothing by the figure columns' minimums. */
      th.item,
      td.item {
        min-width: 6rem;
      }
      /* The phone fold: at the design system's small breakpoint the UoM column
         (redundant with the item's own words for a customer) folds away, so
         the thumbnail column plus four columns fit a 390px card. CSS mirrors
         the px of --hh-bp-sm, the sheet's own convention. */
      @media (max-width: 480px) {
        th.uom,
        td.uom {
          display: none;
        }
      }
      th {
        text-align: left;
        color: var(--hh-text-dim);
        font-weight: 600;
        border-bottom: 1px solid var(--hh-line);
        padding: var(--hh-s-8) var(--hh-s-4);
      }
      td {
        padding: var(--hh-s-8) var(--hh-s-4);
        border-bottom: 1px solid var(--hh-line);
      }
      td.num,
      th.num {
        text-align: right;
      }
      /* The row stripe, ported: the design system's row pair on the body's
         rows, header and totals excluded by the child combinator, zero
         specificity so any row state wins by existing. */
      :where(tbody > tr) {
        background: var(--hh-row);
      }
      :where(tbody > tr:nth-child(even)) {
        background: var(--hh-row-alt);
      }
      .totals {
        margin-top: var(--hh-s-12);
        display: flex;
        flex-direction: column;
        align-items: flex-end;
        gap: var(--hh-s-4);
        font-size: 0.9rem;
      }
      .totals .grand {
        font-size: 1.05rem;
        font-weight: 700;
      }
      .totals .taxnote {
        color: var(--hh-text-dim);
        font-size: 0.78rem;
        font-weight: 400;
        margin-top: var(--hh-s-2);
      }
      label {
        display: block;
        font-size: 0.86rem;
        margin-bottom: var(--hh-s-4);
        font-weight: 600;
      }
      input[type="text"] {
        font: inherit;
        box-sizing: border-box;
        width: 100%;
        max-width: 22rem;
        padding: var(--hh-s-8);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-control);
        background: var(--hh-card);
        color: var(--hh-text);
      }
      .check {
        display: flex;
        align-items: flex-start;
        gap: var(--hh-s-8);
        margin: var(--hh-s-16) 0;
        font-size: 0.88rem;
        line-height: 1.45;
      }
      /* The attestation is a sentence, not a field title: regular weight. */
      .check-label {
        font-weight: 400;
      }
      /* Shadow roots do not inherit the document's focus rule: the accept
         button must show the same ring as every staff surface, and this page
         is often driven by keyboard. */
      button:focus-visible,
      input:focus-visible,
      textarea:focus-visible,
      [tabindex]:focus-visible {
        outline: 2px solid var(--hh-focus-ring);
        outline-offset: 2px;
      }
      /* The button itself is the design system's primitive (.btn-primary, and its
         disabled swap) from baseStyles; only the label's weight is this page's. */
      .btn {
        font-weight: 600;
      }
      .err {
        color: var(--hh-danger);
        font-size: 0.9rem;
        margin-top: var(--hh-s-12);
      }
      .note {
        color: var(--hh-text-dim);
        font-size: 0.78rem;
        margin-top: var(--hh-s-12);
        line-height: 1.5;
      }
      .card-h2 {
        font-size: 1rem;
        margin: 0 0 var(--hh-s-12);
      }
      .ok h2 {
        font-size: 1.05rem;
        margin: 0 0 var(--hh-s-8);
      }
      /* The success mark rides the heading's words (colour never encodes
         state alone): the design system's done-marker path in the success
         ink, sized to the text it sits beside. */
      .okmark {
        width: 1.05em;
        height: 1.05em;
        color: var(--hh-success);
        vertical-align: -0.14em;
        margin-right: var(--hh-s-8);
      }
      .ok .sub {
        margin-bottom: var(--hh-s-12);
      }
      .gone {
        font-size: 0.95rem;
        line-height: 1.55;
      }
      /* The refusal's own anatomy: the dead link is a DELIBERATE page, so it
         carries a heading like every other card and clears the viewport top
         with room of its own, not the host padding alone. */
      .refusal {
        margin-top: var(--hh-s-24);
      }
      .refusal h2,
      .closed h2 {
        margin: 0 0 var(--hh-s-8);
      }
      .refusal .gone,
      .closed .gone {
        margin: 0;
      }
      .thumb {
        width: 36px;
        height: 36px;
        object-fit: cover;
        border-radius: var(--hh-radius-control);
        border: 1px solid var(--hh-line);
        display: block;
      }
      /* The slot a thumbnail paints into: the cell holds its 36px box before
         the image lands, so the row does not shift. */
      .thumbslot {
        display: block;
        width: 36px;
        height: 36px;
      }
      .discnote {
        font-size: 0.76rem;
        color: var(--hh-text-dim);
      }
      .termsfacts {
        border-top: 1px solid var(--hh-line);
        margin-top: var(--hh-s-12);
      }
      .termsfacts p {
        margin: var(--hh-s-8) 0 0;
      }
    `,
  ];

  @state()
  private quote: QuoteViewRef | null = null;
  @state()
  private accepted: AcceptanceRef | null = null;
  @state()
  private error = "";
  @state()
  private fatal = "";
  @state()
  private busy = false;
  @state()
  private typedName = "";
  @state()
  private affirmed = false;
  /** Line thumbnails fetched through the image read, by image reference. */
  @state()
  private thumbs: Map<string, string> = new Map();

  private readonly client: PublicQuoteClient;
  private token = "";
  private autoPrint = false;
  /**
   * Object URLs minted for thumbnails, keyed by image reference. They live as
   * long as the page does: a reload after a refused accept reuses them and
   * fetches only the references it has not seen, so no image ever points at a
   * revoked URL. They are all revoked together when the page goes.
   */
  private thumbUrls: Map<string, string> = new Map();
  /** Bumped when the page goes, so a fetch still running knows its URLs are orphans. */
  private epoch = 0;

  constructor() {
    super();
    // Same origin, same origin relative calls, and no header provider at all:
    // a customer on a share link holds no credential to attach.
    this.client = new PublicQuoteClient(new ApiClient({ baseUrl: "", fetch: envelopeOnlyFetch }));
  }

  connectedCallback(): void {
    super.connectedCallback();
    const params = new URLSearchParams(location.search);
    this.token = params.get("t") ?? "";
    // The staff print action opens this page and asks the browser to print it
    // once the quote has rendered. Guarded: a failed load never opens an
    // empty print dialog.
    this.autoPrint = params.get("print") === "1";
    // The design system's position gate holds the template allow table at
    // zero event and property bindings, so the controls stay static and this
    // root delegates: the kits' own data-action pattern, and id-keyed reads
    // for the two inputs.
    this.renderRoot.addEventListener("input", this.onNameInput);
    this.renderRoot.addEventListener("change", this.onAffirmChange);
    this.renderRoot.addEventListener("click", this.onActionClick);
    void this.load();
  }

  disconnectedCallback(): void {
    super.disconnectedCallback();
    this.renderRoot.removeEventListener("input", this.onNameInput);
    this.renderRoot.removeEventListener("change", this.onAffirmChange);
    this.renderRoot.removeEventListener("click", this.onActionClick);
    this.revokeThumbs();
  }

  private onNameInput = (event: Event): void => {
    const target = event.target as HTMLInputElement;
    if (target.id === "name") this.typedName = target.value;
  };

  private onAffirmChange = (event: Event): void => {
    const target = event.target as HTMLInputElement;
    if (target.id === "affirm") this.affirmed = target.checked;
  };

  private onActionClick = (event: Event): void => {
    const action = (event.target as HTMLElement).closest("[data-action]");
    if (action !== null && action.getAttribute("data-action") === "accept") void this.accept();
  };

  /** Fire the one-shot print once the loaded quote has painted. */
  updated(): void {
    if (this.autoPrint && this.quote) {
      this.autoPrint = false;
      try {
        window.print();
      } catch {
        // printing refused: the page itself is the fallback
      }
    }
    this.paintThumbs();
  }

  /**
   * The thumbnails paint outside the templates: an object URL is a blob
   * scheme, which the design system's address rule (one helper, http and
   * https and scheme free only) refuses to bind, so the slot renders in the
   * template and the image element is built here, one per slot, idempotent.
   */
  private paintThumbs(): void {
    const slots = this.renderRoot.querySelectorAll<HTMLSpanElement>("[data-thumb]");
    for (const slot of slots) {
      const ref = slot.getAttribute("data-thumb");
      const url = ref !== null && ref !== "" ? (this.thumbs.get(ref) ?? null) : null;
      const existing = slot.querySelector("img");
      if (url !== null && existing !== null && existing.getAttribute("src") !== url) {
        existing.src = url;
      } else if (url !== null && existing === null) {
        const img = document.createElement("img");
        img.className = "thumb";
        img.src = url;
        img.alt = "";
        img.loading = "lazy";
        img.width = 36;
        img.height = 36;
        slot.append(img);
      } else if (url === null && existing !== null) {
        existing.remove();
      }
    }
  }

  private revokeThumbs(): void {
    this.epoch += 1;
    for (const url of this.thumbUrls.values()) URL.revokeObjectURL(url);
    this.thumbUrls = new Map();
    this.thumbs = new Map();
  }

  /**
   * Load the quote. A REFRESH (after a refused accept) never replaces the page
   * the customer is looking at: only a dead link does, any other failure
   * leaves the quote and the typed name where they are.
   */
  private async load(refresh = false): Promise<void> {
    if (!this.token) {
      this.fatal = "This link is missing its access code. Please use the full link you were sent.";
      return;
    }
    const result = await this.client.viewQuote(this.token);
    if (!result.ok) {
      if (refresh && result.status !== 410 && result.status !== 404) return;
      // Gone, not found: the link EXISTED and died, or it never did. Say
      // which, and say what to do next.
      this.fatal = result.status === 410 ? LINK_GONE : result.status === 404 ? NOT_FOUND : LOAD_FAILED;
      return;
    }
    this.quote = result.value;
    void this.fetchThumbs(this.quote);
  }

  private async fetchThumbs(q: QuoteViewRef): Promise<void> {
    if (!showsImages(q)) return;
    const refs = new Set(q.lines.map((l) => l.imageRef).filter((ref): ref is string => ref !== null));
    const epoch = this.epoch;
    for (const ref of refs) {
      if (this.thumbUrls.has(ref)) continue;
      const result = await this.client.productImage(ref);
      // The page went while the image was in flight: nothing may keep a URL
      // that the page's own revoke has already passed over.
      if (epoch !== this.epoch || !result.ok || this.thumbUrls.has(ref)) continue;
      // The bytes the image read answers are a fresh ArrayBuffer view; the
      // type is only wider than the platform's BlobPart.
      const url = URL.createObjectURL(new Blob([result.value as unknown as BlobPart]));
      this.thumbUrls.set(ref, url);
      // Each thumbnail paints as it lands, not all together at the end.
      this.thumbs = new Map(this.thumbUrls);
    }
  }

  private async accept(): Promise<void> {
    if (this.busy) return;
    const refusal = acceptanceRefusal(this.typedName, this.affirmed);
    if (refusal !== null) {
      this.error = refusal;
      return;
    }
    this.busy = true;
    this.error = "";
    try {
      const result = await this.client.acceptQuote(this.token, this.typedName.trim());
      if (!result.ok) {
        if (result.status === 410) {
          this.fatal = LINK_GONE_SHORT;
          return;
        }
        // Nothing answered: say so plainly and keep the form and the name.
        if (result.status === undefined) {
          this.error = COULD_NOT_REACH;
          return;
        }
        // The server's refusal is the informative one: an already accepted
        // quote names its state. Show its words when the answer was the JSON
        // error envelope, plain words otherwise (a proxy's page is never
        // shown), and refresh the page's figures from the source.
        const words = serverWords(result);
        this.error = words ?? NOT_RECORDED;
        if (words !== null) await this.load(true);
        return;
      }
      this.accepted = result.value;
    } finally {
      this.busy = false;
    }
  }

  private when(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("en-CA");
  }

  /**
   * The ONE place the totals block is drawn. Both the quote itself and the
   * acceptance echo render through here, from the same pure decision, so the
   * two surfaces cannot drift apart on the question of whether HST is already
   * in the number.
   */
  private renderTotals(m: QuoteMoney): TemplateResult {
    const block = totalsBlock(m);
    return html`
      <div class="totals">
        ${block.rows.map((r) => (r.grand ? html`<span class="grand">${r.label}</span>` : html`<span>${r.label}</span>`))}
        ${block.note ? html`<span class="taxnote">${block.note}</span>` : nothing}
      </div>
    `;
  }

  /**
   * The terms the quote carries, spoken to the customer: the delivery, the
   * deposit ask, and the staff notes. Every row is conditional: an untouched
   * quote prints none of this.
   */
  private renderTermsFacts(q: QuoteViewRef): typeof nothing | TemplateResult {
    const delivery = q.fulfillment === "DELIVERY";
    const deposit = q.depositRequired === true && (q.depositCents ?? 0) > 0;
    const notes = (q.customerNotes ?? "").trim();
    if (!delivery && !deposit && !notes) return nothing;
    return html`<div class="termsfacts">
      ${delivery
        ? html`<p class="note">
            <b>Delivery</b> to ${q.deliveryAddress || "the address on file"}${q.requestedDate
              ? html` · requested ${q.requestedDate}`
              : nothing}${(q.deliveryFeeCents ?? 0) > 0
              ? html` · delivery fee ${formatMoney(q.deliveryFeeCents ?? 0)} (included in the total)`
              : nothing}
          </p>`
        : nothing}
      ${deposit
        ? html`<p class="note">
            <b>Deposit</b>: a ${q.depositPct ?? 0}% deposit of ${formatMoney(q.depositCents ?? 0)} is required to
            schedule this work.
          </p>`
        : nothing}
      ${notes ? html`<p class="note"><b>Notes</b>: ${notes}</p>` : nothing}
    </div>`;
  }

  /** The success mark: the design system's done-marker check, success ink. */
  private renderOkMark(): TemplateResult {
    return html`<svg
      class="okmark"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="3"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      role="presentation"
    >
      <path d="M5 12l5 5 9-10" />
    </svg>`;
  }

  private renderAccepted(a: AcceptanceRef): TemplateResult {
    // The confirmation ECHOES the totals, so the customer's own record of what
    // they agreed to does not depend on their having read the page carefully
    // before clicking.
    return html`
      <div class="card ok">
        <h2>${this.renderOkMark()}Thank you: quote ${a.quoteNo} accepted</h2>
        <p class="sub">${withFullStop(`Accepted by ${a.typedName} on ${this.when(a.acceptedAt)}`)}</p>
        ${this.renderTotals(a)}
        <p class="note">
          We have recorded your acceptance and our team will be in touch. Please keep this page or the confirmation for
          your records.
        </p>
      </div>
    `;
  }

  private renderAcceptForm(q: QuoteViewRef): typeof nothing | TemplateResult {
    if (this.accepted) return nothing;
    if (!q.acceptable) {
      // The customer reads this alone, with no staff beside them to translate,
      // so the state is SAID, in the same six states said mid-sentence: under
      // a heading, so the closed door reads deliberate rather than broken. An
      // ACCEPTED quote is not a refusal at all: it is the customer's own
      // earlier action, acknowledged under the success mark.
      const acceptedElsewhere = q.status === "ACCEPTED";
      return html`<div class="card closed">
        <h2>${acceptedElsewhere ? this.renderOkMark() : nothing}${acceptedElsewhere ? "Already accepted" : "Acceptance closed"}</h2>
        <p class="gone">
          ${q.expired
            ? `This quote expired on ${q.validUntil} and can no longer be accepted online. Please contact us for an updated quote.`
            : `This quote is ${quoteStatusPhrase(q.status)} and is not awaiting acceptance.`}
        </p>
      </div>`;
    }
    return html`
      <div class="card">
        <h2 class="card-h2">Accept this quote</h2>
        <label for="name">Your full name</label>
        <input id="name" type="text" autocomplete="name" />
        <div class="check">
          <input id="affirm" type="checkbox" />
          <label for="affirm" class="check-label">
            ${withFullStop(
              `I accept this quote for ${acceptanceAmountPhrase(q)} and confirm I am authorised to do so on behalf of ${q.customerName}`,
            )}
          </label>
        </div>
        ${button({ label: this.busy ? "Recording…" : "Accept quote", action: "accept", disabled: this.busy })}
        ${this.error ? html`<p class="err" role="alert">${this.error}</p>` : nothing}
        <p class="note">
          Accepting records your typed name, the date and time, and the link you used. This is an electronic acceptance:
          we do not collect a handwritten signature.
        </p>
      </div>
    `;
  }

  render(): TemplateResult {
    if (this.fatal) {
      return html`<div class="card refusal">
        <h2>Quote link unavailable</h2>
        <p class="gone">${this.fatal}</p>
      </div>`;
    }
    const q = this.quote;
    if (!q) return html`<p class="sub">Loading your quote…</p>`;
    const withImages = showsImages(q);
    return html`
      <h1>Quote #${q.quoteNo}</h1>
      <div class="sub">
        Prepared for ${q.customerName} · as of ${this.when(q.asOf)}
        ${q.validUntil ? html` · ${q.expired ? "expired" : "valid until"} ${q.validUntil}` : nothing}
      </div>
      <div class="card">
        <div class="tablewrap">
          <table>
            <thead>
              <tr>
                ${withImages ? html`<th></th>` : nothing}
                <th class="item">Item</th>
                <th class="num">Qty</th>
                <th class="uom">UoM</th>
                <th class="num">Unit</th>
                <th class="num">Amount</th>
              </tr>
            </thead>
            <tbody>
              ${q.lines.map(
                (l) => html`<tr>
                  ${withImages
                    ? html`<td><span class="thumbslot" data-thumb=${l.imageRef}></span></td>`
                    : nothing}
                  <td class="item">
                    ${l.description}
                    ${(l.discPct ?? 0) > 0 ? html`<div class="discnote">${l.discPct}% discount applied</div>` : nothing}
                  </td>
                  <td class="num">${l.qtyEntered}</td>
                  <td class="uom">${l.uomEntered}</td>
                  <td class="num">${formatMoney(l.unitPriceCents)}</td>
                  <td class="num">${formatMoney(l.lineTotalCents)}</td>
                </tr>`,
              )}
            </tbody>
          </table>
        </div>
        ${this.renderTermsFacts(q)}
        ${this.renderTotals(q)}
      </div>
      ${this.accepted ? this.renderAccepted(this.accepted) : this.renderAcceptForm(q)}
    `;
  }
}

defineElement("pd-quote-public", QuotePublicPage);

declare global {
  interface HTMLElementTagNameMap {
    "pd-quote-public": QuotePublicPage;
  }
}

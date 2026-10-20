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
import { productTileUrl } from "@hardscapehouse/design-system/src/assets/product-tiles/index.js";
import {
  acceptanceAmountPhrase,
  acceptanceRefusal,
  acceptHint,
  acceptReady,
  cardSurchargeCents,
  offersCardPayment,
  optionalLineNos,
  quoteMoneyExcludingOptional,
  repHasContact,
  repMailtoHref,
  showsImages,
  statusBadgeKind,
  statusBadgeLabel,
  totalsBlock,
  type QuoteMoney,
  type StatusBadgeKind,
} from "./_quote-public.js";
import { createAcceptKeys } from "./accept-key.js";
import { readDealerName } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { formatMoney, plainDay, plainWhen, quoteNumberLabel, quoteStatusPhrase, withFullStop } from "./format.js";
import {
  envelopeOnlyFetch,
  isDeadLinkAnswer,
  isDefiniteRefusal,
  isKeyReuseRefusal,
  PublicQuoteClient,
  validateQuoteView,
  type AcceptanceRef,
  type QuoteLineRef,
  type QuoteViewRef,
} from "./quote-client.js";

// The staff preview's band: said once, plainly, above the page the customer will see.
const PREVIEW_BAND = "Preview: this is what your customer will see. Nothing has been sent.";
const PREVIEW_REFUSED = "This preview could not be shown.";
const LINK_GONE =
  "This link is no longer valid. Quote links expire, and can also be turned off by our team. Please contact us for an up-to-date link.";
const LINK_GONE_SHORT = "This link is no longer valid. Please contact us for an up-to-date link.";
const NOT_FOUND = "We could not find this quote. Please check the link you were sent.";
const LOAD_FAILED = "We could not load this quote right now. Please try again shortly.";
// Said ONLY after the server's own definite refusal. Every other failure is an
// unknown outcome: the retry carries the same idempotency key, so it is safe.
const NOT_RECORDED = "Your acceptance was not recorded.";
// "With the same name": the key is the token plus the typed name, so a new name is a
// new acceptance. The name field is locked while the outcome is unknown.
const UNCONFIRMED =
  "We could not confirm whether your acceptance was recorded. Trying again with the same name is safe: it will not be recorded twice.";
const NAME_LOCKED =
  "Your name is kept as you first entered it until we know whether your acceptance was recorded, so a retry counts as the same acceptance. Reload this page to change it; a different name may record a second acceptance.";
// The core words its refusals for staff ("convert it at the counter", "ask an administrator"), so
// the customer only ever reads these sentences, chosen by status, never the server's text.
const CANNOT_ACCEPT = "This quote cannot be accepted right now. Please contact us.";
const QUANTITIES_UNCONFIRMED = "Please confirm that you have reviewed the quantities on this quote.";
const CHECK_FIELDS = "Please check your full name and the confirmation, then try again.";
const REFUSED_GENERIC = "We could not accept this quote right now. Please contact us.";
const REPEAT_NOTE =
  "This quote was already accepted. Accepting again through this link with the same name confirms the earlier acceptance; a different name records another acceptance.";
const KEY_REUSED =
  "This quote was already accepted with a different name on this attempt, so nothing more was recorded.";

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
        /* The document's accent, one family at a time (decision log rows 1047 item 7 and 1063): a
           quote reads in the quote blue, so every accent below goes through these aliases and the
           focus ring is the quote twin. The press fill is ONE value (no hover step), and the paper
           copy keeps its colours through the print dialog's default of dropping backgrounds. */
        --doc-ink: var(--hh-quote);
        --doc-fill: var(--hh-quote-fill);
        --doc-fill-on: var(--hh-quote-fill-on);
        --doc-link: var(--hh-quote-link);
        --hh-focus-ring: var(--doc-ink);
        -webkit-print-color-adjust: exact;
        print-color-adjust: exact;
      }
      /* A converted quote is the ORDER's document: the same page, re-pointed to the gold family
         (gold as ink, the gold fill with its near-black ink), and nothing on it reads quote blue. */
      :host([data-doc="order"]) {
        --doc-ink: var(--hh-accent-text);
        --doc-fill: var(--hh-accent);
        --doc-fill-on: var(--hh-accent-ink);
        --doc-link: var(--hh-accent-text);
      }
      h1 {
        font-size: 1.3rem;
        margin: 0 0 var(--hh-s-4);
        color: var(--doc-ink);
      }
      /* The order's band: the gold fill under the near-black accent ink, the one pair the gold rule
         allows (11.23:1). The title rides it; the quote has no band on this page. */
      .orderband {
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
        border-radius: var(--hh-radius-card);
        padding: var(--hh-s-12) var(--hh-s-16);
        margin-bottom: var(--hh-s-12);
      }
      .orderband h1 {
        color: var(--hh-accent-ink);
        margin: 0;
      }
      .orderband .from {
        font-weight: 600;
      }
      /* The confirmation line a converted quote carries beside its order title (FROM-PRINCIPAL 29):
         the same words the accepted page's card uses, riding the gold band so the one statement says
         both what this document became and that the customer's decision is made. */
      .orderband .orderconfirmed {
        margin: var(--hh-s-4) 0 0;
        font-size: 0.88rem;
        font-weight: 600;
      }
      a {
        color: var(--doc-link);
      }
      /* The dealer's header band, the page's first element in every state: the
         one mark of WHO is speaking, the name carried from the app's own data
         (src/dealer.ts) over a hairline, so a printed or screenshotted page
         says whose quote it is before anything else. */
      header.dealer {
        display: flex;
        align-items: baseline;
        border-bottom: 2px solid var(--doc-ink);
        padding: 0 0 var(--hh-s-12);
        margin-bottom: var(--hh-s-20);
      }
      header.dealer .dealer-name {
        font-size: 1.05rem;
        font-weight: 700;
        letter-spacing: 0.01em;
      }
      /* Round 2 (FROM-PRINCIPAL 9), the header rule's print twin. On screen the rule rides the
         --doc-ink alias and renders the family; the principal's live check found the PAPER copy
         with a black rule, the currentColor a border falls back to when a print path re-resolves
         the alias chain and loses it. The print half therefore states the family itself, as an
         ordinary longhand inside the print media block: the quote blue for a quote, the gold ink
         for the converted order. Pinned by test/print-rule.test.mjs (the shipped source) and the
         print leg in test/page.pw.ts (the computed colour under print emulation). */
      @media print {
        header.dealer {
          border-bottom-color: var(--hh-quote);
        }
        :host([data-doc="order"]) header.dealer {
          border-bottom-color: var(--hh-accent-text);
        }
      }
      /* Full text ink, NOT the dimmed one: this line sits on the page rather
         than on a card, and it is load bearing information, not decoration. */
      .previewband {
        border: 1px solid var(--hh-line);
        border-left-width: 4px;
        border-radius: var(--hh-radius-card);
        background: var(--hh-card);
        padding: var(--hh-s-12) var(--hh-s-16);
        margin-bottom: var(--hh-s-16);
        font-size: 0.9rem;
        font-weight: 600;
      }
      .sub {
        color: var(--hh-text);
        font-size: 0.88rem;
        margin-bottom: var(--hh-s-20);
      }
      /* The status badge: a small pill riding the meta line, fixed colour by status, the
         wording the customer reads (Awaiting acceptance, Accepted, Expired, Withdrawn).
         The chip is NEVER colour alone: its text names the state, so a colour-blind reader
         still reads the state and a high-contrast reader still reads the pill as a pill.
         Mobile: the pill sits in its own row and the rest wraps under it. */
      .statusbadge {
        display: inline-flex;
        align-items: center;
        gap: var(--hh-s-4);
        padding: var(--hh-s-4) var(--hh-s-12);
        border-radius: var(--hh-radius-pill);
        font-size: 0.78rem;
        font-weight: 700;
        letter-spacing: 0.02em;
        line-height: 1.2;
        border: 1px solid transparent;
      }
      .statusbadge-info {
        background: color-mix(in srgb, var(--hh-info) 12%, var(--hh-card));
        color: var(--hh-info);
        border-color: color-mix(in srgb, var(--hh-info) 30%, var(--hh-line));
      }
      .statusbadge-success {
        background: color-mix(in srgb, var(--hh-success) 14%, var(--hh-card));
        color: var(--hh-success);
        border-color: color-mix(in srgb, var(--hh-success) 30%, var(--hh-line));
      }
      .statusbadge-accent {
        /* The CONVERTED status rides the document's family (--doc-fill, which is the
           gold fill when the host's data-doc is "order"), so the chip cannot drift
           away from the band it lives under. The sinks rule on this app says the gold
           fill is named only by the order family and its band; the badge reads the
           alias, not the raw token, to keep that pair. */
        background: color-mix(in srgb, var(--doc-fill) 18%, var(--hh-card));
        color: var(--doc-ink);
        border-color: color-mix(in srgb, var(--doc-ink) 30%, var(--hh-line));
      }
      .statusbadge-danger {
        background: color-mix(in srgb, var(--hh-danger) 14%, var(--hh-card));
        color: var(--hh-danger);
        border-color: color-mix(in srgb, var(--hh-danger) 30%, var(--hh-line));
      }
      /* The meta row: one row holding the badge and the prepared-for line, wrapping on phone. */
      .metaline {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: var(--hh-s-8);
        margin-bottom: var(--hh-s-12);
        font-size: 0.88rem;
        color: var(--hh-text);
      }
      .metaline .validuntil {
        color: var(--hh-text-dim);
      }
      /* The compact facts (rep card, delivery card, notes card): each one is a card with a
         heading and a body, the same card family the rest of the page uses, with a tighter
         top margin so two cards stacked read as one block. The phone wrap is automatic: the
         cards are full-width. */
      .facts {
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-12);
        margin-bottom: var(--hh-s-16);
      }
      .facts .card {
        margin: 0;
      }
      .factrow {
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-4);
        font-size: 0.9rem;
      }
      .factrow .label {
        color: var(--hh-text-dim);
        font-size: 0.78rem;
        font-weight: 600;
      }
      .factrow .value {
        color: var(--hh-text);
        overflow-wrap: anywhere;
      }
      .replist {
        list-style: none;
        padding: 0;
        margin: 0;
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-4);
      }
      .replist a {
        color: var(--doc-link);
        overflow-wrap: anywhere;
      }
      /* The deposit & acceptance card: the heading, then the deposit figure, then the
         payment radios, then the existing quantities/name/affirm stack, then the buttons. */
      .depositline {
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-4);
        padding: var(--hh-s-8) var(--hh-s-12);
        margin: var(--hh-s-8) 0 var(--hh-s-12);
        border-radius: var(--hh-radius-control);
        background: color-mix(in srgb, var(--hh-info) 8%, var(--hh-card));
        font-size: 0.9rem;
      }
      .depositline .label {
        color: var(--hh-text-dim);
        font-size: 0.78rem;
        font-weight: 700;
        text-transform: uppercase;
        letter-spacing: 0.05em;
      }
      .depositline .value {
        font-weight: 600;
      }
      .paymentrow {
        display: flex;
        flex-wrap: wrap;
        gap: var(--hh-s-8);
        margin: var(--hh-s-12) 0;
      }
      .paymentrow .payopt {
        flex: 1 1 12rem;
        min-width: 0;
        display: flex;
        align-items: flex-start;
        gap: var(--hh-s-8);
        padding: var(--hh-s-8) var(--hh-s-12);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-control);
        background: var(--hh-card);
        cursor: pointer;
      }
      .paymentrow .payopt:has(input:checked) {
        background: color-mix(in srgb, var(--hh-info) 10%, var(--hh-card));
        border-color: color-mix(in srgb, var(--hh-info) 40%, var(--hh-line));
      }
      .paymentrow .payopt input[type="radio"] {
        flex: none;
        width: 1.1rem;
        height: 1.1rem;
        margin-top: 0.15rem;
        accent-color: var(--hh-info);
      }
      .paymentrow .payopt .paylabel {
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-2);
        font-size: 0.88rem;
        font-weight: 600;
        margin: 0;
      }
      .paymentrow .payopt .paysub {
        color: var(--hh-text-dim);
        font-size: 0.78rem;
        font-weight: 400;
        line-height: 1.4;
      }
      /* The secondary actions stack under the primary accept: print and ask-a-question,
         each a full-width row on phone and a side-by-side row on desktop. */
      .actionstack {
        display: flex;
        flex-wrap: wrap;
        gap: var(--hh-s-8);
        margin-top: var(--hh-s-12);
      }
      .actionstack button.printbtn {
        flex: 1 1 12rem;
        min-width: 0;
      }
      /* The optional add-on row: same row shape as the others, with a checkbox at the
         left, the description in the middle, and the amount at the right. An excluded
         optional line dims and slashes, so the customer sees what is NOT included. */
      tr.optionalrow.excluded td {
        color: var(--hh-text-dim);
        text-decoration: line-through;
      }
      td.optionalcell {
        vertical-align: middle;
      }
      td.optionalcell input[type="checkbox"] {
        width: 1.05rem;
        height: 1.05rem;
        margin: 0;
        accent-color: var(--hh-info);
        cursor: pointer;
      }
      .optionallabel {
        display: inline-block;
        margin-left: var(--hh-s-8);
        font-size: 0.74rem;
        font-weight: 600;
        color: var(--hh-text-dim);
        letter-spacing: 0.03em;
      }
      @media (max-width: 480px) {
        .metaline {
          gap: var(--hh-s-4);
        }
        .metaline .statusbadge {
          flex: 1 1 auto;
        }
        .paymentrow .payopt {
          flex-basis: 100%;
        }
        .actionstack a.asklink,
        .actionstack button.printbtn {
          flex-basis: 100%;
        }
        .actionstack button.btn-secondary {
          flex-basis: 100%;
        }
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
      @media (max-width: 480px) {
        th.uom,
        td.uom {
          display: none;
        }
        /* The figures stay whole on a phone: tighter cell padding, and the Amount column holds the right edge while the
           rest of the row scrolls inside its own box. The words stay the page's body size, never smaller. */
        th,
        td {
          padding-left: var(--hh-s-2);
          padding-right: var(--hh-s-2);
        }
        th.item,
        td.item {
          min-width: 3.5rem;
          overflow-wrap: anywhere;
        }
        th.amount,
        td.amount {
          position: sticky;
          right: 0;
          background: var(--hh-card);
        }
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
        color: var(--doc-ink);
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
      /* The dealer's notice that the quantities are the customer's to confirm: an information callout,
         not a field. A tinted ground, a heavy rule and an icon in the info ink, no hairline box, so it
         cannot be mistaken for the inputs below and reads the same on screen and on paper. */
      .qtynotice {
        display: flex;
        align-items: flex-start;
        gap: var(--hh-s-8);
        margin: var(--hh-s-12) 0 0;
        padding: var(--hh-s-8) var(--hh-s-12);
        border: 0;
        border-left: 4px solid var(--hh-info);
        border-radius: var(--hh-radius-control);
        background: color-mix(in srgb, var(--hh-info) 11%, var(--hh-card));
        color: var(--hh-text);
        font-size: 0.88rem;
        line-height: 1.45;
        overflow-wrap: anywhere;
      }
      .noticeicon {
        flex: none;
        width: 1.1rem;
        height: 1.1rem;
        margin-top: 0.1rem;
        color: var(--hh-info);
      }
      .noticetext {
        min-width: 0;
      }
      /* The accept card's ticks, the quantities box and the confirmation alike: one row design, one
         type, so the quantities tick reads as part of the card and not a line added above the name. A
         ticked row takes the info tint, the same ground the notice speaks in. */
      .tick {
        display: flex;
        align-items: flex-start;
        gap: var(--hh-s-8);
        margin: var(--hh-s-12) 0;
        padding: var(--hh-s-8) var(--hh-s-12);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-control);
        background: var(--hh-card);
      }
      .tick:has(input:checked) {
        background: color-mix(in srgb, var(--hh-info) 8%, var(--hh-card));
      }
      .tick input[type="checkbox"] {
        flex: none;
        width: 1.1rem;
        height: 1.1rem;
        margin: 0.1rem 0 0;
        accent-color: var(--hh-info);
      }
      .tick label {
        margin: 0;
        font-size: 0.9rem;
        font-weight: 500;
        line-height: 1.45;
        overflow-wrap: anywhere;
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
         disabled swap) from baseStyles; this page re-points its fill to the document's family
         (the quote fill, with its own ink, one value with no hover step) and sets the label's
         weight. Equal specificity with the primitive, so the disabled swap still wins. */
      .btn {
        font-weight: 600;
      }
      .btn-primary,
      .btn-primary:hover {
        background: var(--doc-fill);
        color: var(--doc-fill-on);
        border-color: var(--doc-fill-on);
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
      /* The line's image cell, EVERY line the same while images are on: the
         cell is always the one visible box (the till's line-thumb shape), and
         a line without a photo shows the drawn no-photo mark in it, never an
         empty box (an empty box is indistinguishable from an image still
         loading) and never a missing cell. */
      .thumbslot {
        display: grid;
        place-items: center;
        width: 36px;
        height: 36px;
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-control);
        background: var(--hh-card);
        overflow: hidden;
      }
      .thumb,
      .tile {
        width: 100%;
        height: 100%;
        object-fit: cover;
        display: block;
      }
      /* The no-photo mark: the stacked-stone silhouette the till's thumbnail
         component draws, in the dimmed ink (an absence, so it must not
         compete with the item name beside it). */
      .thumbmark {
        width: 60%;
        height: 60%;
        color: var(--hh-text-dim);
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

  /** The dealer named in the header band, from the tenant config; empty until the read lands. */
  @state()
  private dealerName = "";

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
  /** A failed accept whose re-read proved the acceptance landed: the page ends on the accepted state. */
  @state()
  private landed = false;
  /** The server said this acceptance's key was used for a different typed name. */
  @state()
  private keyReused = false;
  @state()
  private typedName = "";
  /**
   * The first attempt's name while its outcome is UNKNOWN, else null. The key is the token plus
   * the typed name, so only this name keeps a retry the same acceptance: the field is read only
   * and every retry sends this name and its key. Cleared when the outcome becomes known.
   */
  @state()
  private lockedName: string | null = null;
  @state()
  private affirmed = false;
  /** The separate confirmation that the customer reviewed the quantities. */
  @state()
  private quantitiesConfirmed = false;
  /** Line thumbnails fetched through the image read, by image reference. */
  @state()
  private thumbs: Map<string, string> = new Map();
  /**
   * The customer's toggle for each OPTIONAL add-on line, by line number: true
   * when the customer EXCLUDED the line, false when they kept it. Every
   * optional line starts INCLUDED (so a customer who never touches the
   * toggles agrees to the full quote) and any unrecognised number is treated
   * as INCLUDED (so a re-render after a contract bump does not silently
   * hide new optional lines from the customer's view).
   */
  @state()
  private optionalExcluded: Set<number> = new Set();
  /** "ACH" (default) or "card" when the dealer accepts cards with a surcharge. */
  @state()
  private paymentMethod: "ach" | "card" = "ach";

  private readonly client: PublicQuoteClient;
  private readonly acceptKeys = createAcceptKeys();
  private token = "";
  private autoPrint = false;
  /** The staff print action's copy: the quote on paper, with no accept card (nothing on paper is tickable). */
  private printCopy = false;
  /** True once a host has handed the page its quote: the page then never reads the link or loads. */
  private hosted = false;
  /** A host handed the page something that is not a well formed preview: the page says so and shows nothing else. */
  @state()
  private previewRefused = false;
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
    void readDealerName().then((name) => {
      this.dealerName = name;
    });
    const params = new URLSearchParams(location.search);
    this.token = params.get("t") ?? "";
    // The staff print action opens this page and asks the browser to print it
    // once the quote has rendered. Guarded: a failed load never opens an
    // empty print dialog.
    this.autoPrint = params.get("print") === "1";
    this.printCopy = this.autoPrint;
    // The design system's position gate holds the template allow table at
    // zero event and property bindings, so the controls stay static and this
    // root delegates: the kits' own data-action pattern, and id-keyed reads
    // for the two inputs. The optional-add-on checkboxes ride the same
    // change delegate (so the design system's position gate holds).
    this.renderRoot.addEventListener("input", this.onNameInput);
    this.renderRoot.addEventListener("change", this.onAffirmChange);
    this.renderRoot.addEventListener("change", this.onOptionalChange);
    this.renderRoot.addEventListener("change", this.onPaymentChange);
    this.renderRoot.addEventListener("click", this.onActionClick);
    if (!this.hosted) void this.load();
  }

  /**
   * Render a quote the HOST already holds, instead of loading one by link token. This is how the Quote
   * Desk shows the staff preview: the page itself holds no session and reads no credential, so the host
   * fetches the contract's customer view read with its own session and hands over the answer as it came
   * off the wire. The page trusts nothing it is handed: it validates the answer itself
   * (validateQuoteView), and a hosted page is a preview by construction, so anything that is not a
   * well formed view with the preview flag true is refused with a plain state. A hosted page never
   * renders an accept control, whatever the view carries. The thumbnails still ride the public image read.
   */
  showQuote(value: unknown): void {
    this.hosted = true;
    this.fatal = "";
    const view = validateQuoteView(value);
    if (view === null || !view.preview) {
      this.quote = null;
      this.previewRefused = true;
      return;
    }
    this.previewRefused = false;
    this.quote = view;
    void this.fetchThumbs(view);
  }

  disconnectedCallback(): void {
    super.disconnectedCallback();
    this.renderRoot.removeEventListener("input", this.onNameInput);
    this.renderRoot.removeEventListener("change", this.onAffirmChange);
    this.renderRoot.removeEventListener("change", this.onOptionalChange);
    this.renderRoot.removeEventListener("change", this.onPaymentChange);
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
    if (target.id === "qty-confirm") this.quantitiesConfirmed = target.checked;
  };

  private onOptionalChange = (event: Event): void => {
    const target = event.target as HTMLInputElement;
    if (target.dataset["lineNo"] === undefined) return;
    const lineNo = Number(target.dataset["lineNo"]);
    if (!Number.isInteger(lineNo)) return;
    const next = new Set(this.optionalExcluded);
    if (target.checked) next.delete(lineNo);
    else next.add(lineNo);
    this.optionalExcluded = next;
  };

  private onPaymentChange = (event: Event): void => {
    const target = event.target as HTMLInputElement;
    if (target.name !== "payment") return;
    if (target.value === "ach" || target.value === "card") this.paymentMethod = target.value;
  };

  private onActionClick = (event: Event): void => {
    const action = (event.target as HTMLElement).closest("[data-action]");
    if (action === null) return;
    const kind = action.getAttribute("data-action");
    if (kind === "accept") void this.accept();
    else if (kind === "print") void this.printNow();
    else if (kind === "ask") void this.askRep();
  };

  /**
   * Fire the browser's print dialog from the visible "Print or save PDF" button. The
   * staff `?print=1` link calls the same browser path; the button shares the same
   * outcome, so a customer's print and a page-stored print copy cannot drift.
   * Guarded: the page is the fallback if the browser refuses.
   */
  private async printNow(): Promise<void> {
    try {
      window.print();
    } catch {
      // the page itself is the fallback; no-op keeps the handler quiet
    }
  }

  /**
   * Open the customer's default mail client with the rep at the "to:" address
   * and the quote number in the subject line. The handler builds the mailto:
   * URL on click (the position gate forbids a binding inside an address
   * attribute, and hhAddress refuses non-http schemes, so the link is opened
   * via window.open). The customer's mail client handles the address; the
   * page never holds an open mail draft.
   */
  private async askRep(): Promise<void> {
    const q = this.quote;
    if (q === null) return;
    const href = repMailtoHref(q.quoteNo, q.repEmail);
    try {
      window.open(href, "_self");
    } catch {
      // the customer's browser refused; the rep face is the same address
    }
  }

  /**
   * The name lock is applied here, not bound in the template (the design system's template
   * gate allows no property bindings): the field reads only, and shows the locked name.
   */
  private syncNameLock(): void {
    const input = this.renderRoot.querySelector<HTMLInputElement>("#name");
    if (input === null) return;
    const locked = this.lockedName !== null;
    if (input.readOnly !== locked) input.readOnly = locked;
    if (this.lockedName !== null && input.value !== this.lockedName) input.value = this.lockedName;
  }

  private syncOptionalAndPayment(): void {
    for (const input of Array.from(this.renderRoot.querySelectorAll<HTMLInputElement>("input.optionalcell"))) {
      const lineNo = Number(input.dataset["lineNo"]);
      if (!Number.isInteger(lineNo)) continue;
      const shouldBeChecked = !this.optionalExcluded.has(lineNo);
      if (input.checked !== shouldBeChecked) input.checked = shouldBeChecked;
    }
    for (const radio of Array.from(this.renderRoot.querySelectorAll<HTMLInputElement>('input[type="radio"][name="payment"]'))) {
      const shouldBeChecked = radio.value === this.paymentMethod;
      if (radio.checked !== shouldBeChecked) radio.checked = shouldBeChecked;
    }
  }

  /**
   * The disabled Accept button is described by the hint beside it. The design system's button takes no
   * description, and the template gate allows no attribute bindings, so the tie is applied here, like the
   * name lock: present while the hint is, gone with it.
   */
  private syncAcceptHint(): void {
    const button = this.renderRoot.querySelector<HTMLButtonElement>('[data-action="accept"]');
    if (button === null) return;
    const hinted = this.renderRoot.querySelector("#accept-hint") !== null;
    if (hinted && button.getAttribute("aria-describedby") !== "accept-hint") button.setAttribute("aria-describedby", "accept-hint");
    if (!hinted && button.hasAttribute("aria-describedby")) button.removeAttribute("aria-describedby");
  }

  /** The document's family follows the quote's state: a CONVERTED quote reads as the order. */
  willUpdate(): void {
    if (this.quote?.status === "CONVERTED") this.setAttribute("data-doc", "order");
    else this.removeAttribute("data-doc");
  }

  /** Fire the one-shot print once the loaded quote has painted. */
  updated(): void {
    this.syncNameLock();
    this.syncOptionalAndPayment();
    this.syncAcceptHint();
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
  /**
   * The drawn tile for a line that carries no photo (decision log row 1057): the design system's
   * tile for the product's category, in the page's theme. The public line carries no category word
   * of its own, so the tile is read from the line's description by the same resolver the till uses
   * (an unknown name resolves to no tile and the line keeps the no-photo mark; nothing is guessed).
   */
  private tileFor(line: QuoteLineRef): string | null {
    if (line.imageRef) return null;
    const theme = document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark";
    return productTileUrl(line.description, theme);
  }

  private paintThumbs(): void {
    const tiles = this.renderRoot.querySelectorAll<HTMLSpanElement>("[data-tile]");
    for (const slot of tiles) {
      const name = slot.getAttribute("data-tile") ?? "";
      const theme = document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark";
      const url = productTileUrl(name, theme);
      const existing = slot.querySelector("img.tile");
      if (url === null) {
        existing?.remove();
      } else if (existing === null) {
        const img = document.createElement("img");
        img.className = "tile";
        img.src = url;
        img.alt = "";
        img.width = 36;
        img.height = 36;
        slot.append(img);
      } else if (existing.getAttribute("src") !== url) {
        existing.setAttribute("src", url);
      }
    }
    const slots = this.renderRoot.querySelectorAll<HTMLSpanElement>("[data-thumb]");
    for (const slot of slots) {
      const ref = slot.getAttribute("data-thumb");
      const url = ref !== null && ref !== "" ? (this.thumbs.get(ref) ?? null) : null;
      const existing = slot.querySelector<HTMLImageElement>("img.thumb");
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
  private async load(refresh = false, holdGone = false): Promise<boolean | "gone"> {
    if (!this.token) {
      this.fatal = "This link is missing its access code. Please use the full link you were sent.";
      return false;
    }
    const result = await this.client.viewQuote(this.token);
    if (!result.ok) {
      if (refresh && result.status !== 410 && result.status !== 404) return false;
      // A dead link on the re-read of an unknown outcome is not shown yet: the caller learns the
      // outcome first (the core replays a keyed accept through a dead link).
      if (holdGone && result.status === 410) return "gone";
      // Gone, not found: the link EXISTED and died, or it never did. Say
      // which, and say what to do next.
      this.fatal = result.status === 410 ? LINK_GONE : result.status === 404 ? NOT_FOUND : LOAD_FAILED;
      return false;
    }
    this.quote = result.value;
    void this.fetchThumbs(this.quote);
    return true;
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
    const refusal = acceptanceRefusal(this.lockedName ?? this.typedName, this.affirmed, this.quantitiesConfirmed);
    if (refusal !== null) {
      this.error = refusal;
      return;
    }
    this.busy = true;
    this.error = "";
    const before = this.quote?.status;
    try {
      // While an outcome is unknown the first attempt's name is the only name ever sent, so the
      // key is the same one and the server answers a repeat with the first outcome.
      const typedName = this.lockedName ?? this.typedName.trim();
      const key = await this.acceptKeys.keyFor(this.token, typedName);
      // The customer's optional add-on choices: the list of EXCLUDED line numbers, in the
      // server's own order. An empty list is honest (a quote with no optional lines) and the
      // client drops the field when empty, so the wire shape matches the contract.
      const excluded = [...this.optionalExcluded].sort((a, b) => a - b);
      const result = await this.client.acceptQuote(this.token, typedName, key, excluded);
      if (!result.ok) {
        if (result.status === 410) {
          this.fatal = LINK_GONE_SHORT;
          return;
        }
        if (isKeyReuseRefusal(result)) {
          // The server holds an acceptance under this very key with another
          // typed name: the quote WAS accepted. Re-read, then end on the
          // accepted state whatever the re-read says.
          await this.load(true);
          if (this.fatal !== "") return;
          this.keyReused = true;
          this.landed = true;
          this.error = "";
          return;
        }
        if (isDefiniteRefusal(result)) {
          // The server's own refusal, in its envelope: this attempt recorded nothing, and the
          // outcome is known, so the name unlocks. The words are the page's own, by status.
          this.lockedName = null;
          if (result.status === 409) {
            // The quote may have been accepted by an earlier attempt or another tab: ask it.
            await this.load(true);
            if (this.fatal !== "") return;
            const now = this.quote;
            if (now !== null && (now.status === "ACCEPTED" || now.status === "CONVERTED")) {
              this.landed = true;
              this.error = "";
              return;
            }
            this.error = CANNOT_ACCEPT;
            return;
          }
          const sentence =
            result.status === 400
              ? CHECK_FIELDS
              : result.status === 422
                ? QUANTITIES_UNCONFIRMED
                : result.status === 404
                  ? NOT_FOUND
                  : REFUSED_GENERIC;
          this.error = `${sentence} ${NOT_RECORDED}`;
          return;
        }
        // Anything else may hide a recorded acceptance (a proxy's page after
        // the core committed, a body lost on the way back, a request that
        // timed out here and commits there later), so the outcome is UNKNOWN.
        // A re-read showing SENT does not prove nothing was recorded, so the
        // page never says it; the retry carries the same key and name, so it is safe.
        this.lockedName = typedName;
        const reread = await this.load(true, true);
        if (reread === "gone") {
          await this.learnThroughDeadLink(typedName, key);
          return;
        }
        if (this.fatal !== "") return;
        const now = this.quote;
        if (reread && now !== null && before === "SENT" && (now.status === "ACCEPTED" || now.status === "CONVERTED")) {
          // The acceptance landed. With multi-accept on the core keeps an
          // accepted quote acceptable, so the page ends on the accepted
          // state itself rather than offering the form again.
          this.landed = true;
          this.error = "";
          return;
        }
        this.error = UNCONFIRMED;
        return;
      }
      this.accepted = result.value;
    } finally {
      this.busy = false;
    }
  }

  /**
   * The re-read of an unknown outcome found the link dead. The core replays a keyed accept through
   * a dead link (a 200 is the first outcome, read only; a 410 means nothing was recorded under this
   * key), so the same keyed accept is sent once to learn which.
   */
  private async learnThroughDeadLink(typedName: string, key: string): Promise<void> {
    const result = await this.client.acceptQuote(this.token, typedName, key);
    if (result.ok) {
      this.accepted = result.value;
      return;
    }
    // Only the core's own 410 proves nothing was recorded under this key. Anything else (a proxy,
    // a timeout, a lost answer) leaves the outcome unknown, and the customer is told so.
    if (isDeadLinkAnswer(result)) {
      this.fatal = LINK_GONE;
      return;
    }
    this.error = UNCONFIRMED;
  }

  private when(iso: string): string {
    return plainWhen(iso);
  }

  /**
   * The ONE place the totals block is drawn. Both the quote itself and the
   * acceptance echo render through here, from the same pure decision, so the
   * two surfaces cannot drift apart on the question of whether the tax is already
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
    // The terms facts speak to the customer: the deposit ask and the staff notes.
    // Delivery is no longer here: it now rides the job-site card above the totals
    // (Change 9 WALK-C9), so the two surfaces cannot disagree on the same fact.
    const deposit = q.depositRequired === true && (q.depositCents ?? 0) > 0;
    const notes = (q.customerNotes ?? "").trim();
    if (!deposit && !notes) return nothing;
    return html`<div class="termsfacts">
      ${deposit
        ? html`<p class="note">
            <b>Deposit</b>: a ${q.depositPct ?? 0}% deposit of ${formatMoney(q.depositCents ?? 0)} is required to
            schedule this work.
          </p>`
        : nothing}
      ${notes ? html`<p class="note"><b>Notes</b>: ${notes}</p>` : nothing}
    </div>`;
  }

  /**
   * The status chip that rides the meta line beside the quote number. The
   * chip's text names the state ("Awaiting acceptance", "Expired") so colour
   * is never the only carrier; the kind picks the token the chip's pair was
   * measured for, and the print half re-states the colour so the paper copy
   * cannot drop the chip's family.
   */
  private renderStatusBadge(q: QuoteViewRef): TemplateResult {
    const effectiveStatus = q.expired && q.status === "SENT" ? "EXPIRED" : q.status;
    const kind = statusBadgeKind(effectiveStatus);
    return html`<span class="statusbadge statusbadge-${kind}" data-status=${effectiveStatus}>${statusBadgeLabel(effectiveStatus)}</span>`;
  }

  /**
   * The job-site / delivery card: where the material is going and when.
   * The card is conditional: a pickup carries no address, and a delivery
   * without a date shows just the address. The same words the older terms
   * facts used ("Delivery to <address>") ride the new card so an existing
   * customer reading the page still sees the same phrase.
   */
  private renderJobSiteCard(q: QuoteViewRef): typeof nothing | TemplateResult {
    const delivery = q.fulfillment === "DELIVERY";
    const address = (q.deliveryAddress ?? "").trim();
    const hasAny = delivery || address !== "";
    if (!hasAny) return nothing;
    if (delivery) {
      return html`<div class="card" data-fact="job-site">
        <h2 class="card-h2">Delivery</h2>
        <div class="factrow">
          <span class="value">Delivery to ${address !== "" ? address : "the address on file"}</span>
          ${q.requestedDate ? html`<span class="label">Requested ${plainDay(q.requestedDate)}</span>` : nothing}
          ${(q.deliveryFeeCents ?? 0) > 0
            ? html`<span class="label">delivery fee ${formatMoney(q.deliveryFeeCents ?? 0)} (included in the total)</span>`
            : nothing}
        </div>
      </div>`;
    }
    return html`<div class="card" data-fact="job-site">
      <h2 class="card-h2">Pickup</h2>
      <div class="factrow">
        <span class="value">${address !== "" ? `Pickup at ${address}` : "Pickup at our yard"}</span>
      </div>
    </div>`;
  }

  /**
   * The rep contact card: the rep's name, email and phone as a customer
   * reads them. The card is conditional: it only renders when the dealer set
   * a rep name (the only required field; an email alone does not earn a card
   * because there is no one to address). The "Ask a question" CTA rides the
   * action stack in the accept card so it sits with the other actions; this
   * card carries the contact facts.
   */
  private renderRepCard(q: QuoteViewRef): typeof nothing | TemplateResult {
    if (!repHasContact(q)) return nothing;
    const name = (q.repName ?? "").trim();
    const email = (q.repEmail ?? "").trim();
    const phone = (q.repPhone ?? "").trim();
    return html`<div class="card" data-fact="rep">
      <h2 class="card-h2">Your rep</h2>
      <div class="factrow">
        <span class="value">${name}</span>
        ${email !== "" ? html`<span class="value">${email}</span>` : nothing}
        ${phone !== "" ? html`<span class="value">${phone}</span>` : nothing}
      </div>
    </div>`;
  }

  /**
   * The payment method radio row: ACH first (the cheaper option for the customer
   * and the dealer's preferred rail), card with a surcharge line when the dealer
   * configured one. The card row's sub-line names the surcharge as a percentage
   * and a money figure, so the customer sees the cost before they tick. The
   * radios carry NO checked attribute binding (the design system's position gate
   * forbids property bindings in templates): the browser's own radio-group
   * behaviour defaults the first option to checked on first render, and the
   * change delegate picks up the new value the moment the customer switches.
   */
  private renderPaymentOptions(q: QuoteViewRef): TemplateResult {
    const cardOption = offersCardPayment(q);
    const surchargeCents = cardSurchargeCents(q.totalCents, q.cardSurchargePct);
    const surchargePct = q.cardSurchargePct ?? 0;
    return html`<div class="paymentrow" role="radiogroup" aria-label="Payment method">
      <label class="payopt">
        <input type="radio" name="payment" value="ach" checked />
        <span class="paylabel">
          ACH transfer
          <span class="paysub">No surcharge; the deposit clears in one to three business days.</span>
        </span>
      </label>
      ${cardOption
        ? html`<label class="payopt">
            <input type="radio" name="payment" value="card" />
            <span class="paylabel">
              Card
              <span class="paysub">
                ${Number(surchargePct.toFixed(2))}% surcharge (${formatMoney(surchargeCents)} on this deposit).
              </span>
            </span>
          </label>`
        : nothing}
    </div>`;
  }

  /**
   * The deposit ask the customer reads above the radio row: the percentage
   * and the cents figure, in the dealer's own words. The card surcharge is
   * paid on top of this deposit; the card option's sub-line names it.
   */
  private renderDepositLine(q: QuoteViewRef): typeof nothing | TemplateResult {
    if (q.depositRequired !== true || (q.depositCents ?? 0) <= 0) return nothing;
    return html`<div class="depositline">
      <span class="label">Deposit due</span>
      <span class="value">
        ${formatMoney(this.adjustedDepositCents(q))} (${q.depositPct ?? 0}% of the accepted total)
      </span>
    </div>`;
  }

  /**
   * The deposit the customer is being asked to pay, accounting for any optional
   * add-on exclusions. The percentage rides the quote, but the base is the
   * customer's adjusted total, so the customer never pays a deposit on lines
   * they excluded. A quote without optional lines returns the server's deposit
   * figure unchanged.
   */
  private adjustedDepositCents(q: QuoteViewRef): number {
    if (q.depositRequired !== true || (q.depositCents ?? 0) <= 0) return 0;
    const adjusted = this.adjustedMoney(q);
    const serverTotal = q.totalCents;
    const serverDeposit = q.depositCents ?? 0;
    if (serverTotal <= 0 || adjusted.totalCents === serverTotal) return serverDeposit;
    // Proportional: the deposit's relationship to the total holds for the
    // customer's view. A line-by-line deposit policy could be done here too,
    // but the contract is "a percentage of the accepted total" and that is
    // what the page displays.
    return Math.round((adjusted.totalCents * serverDeposit) / serverTotal);
  }

  /**
   * The quote's money as the customer sees it: full when no optional add-on
   * line is excluded, recomputed when at least one is. The helper is pure,
   * so the same call drives the totals block and the deposit line.
   */
  private adjustedMoney(q: QuoteViewRef): QuoteMoney {
    const excludedLineCents: number[] = [];
    for (const line of q.lines) {
      if (line.optional && this.optionalExcluded.has(line.lineNo)) excludedLineCents.push(line.lineTotalCents);
    }
    if (excludedLineCents.length === 0) return q;
    const m: QuoteMoney = {
      subtotalCents: q.subtotalCents,
      taxCents: q.taxCents,
      totalCents: q.totalCents,
      taxLabel: q.taxLabel,
    };
    return quoteMoneyExcludingOptional(m, excludedLineCents);
  }

  /**
   * The secondary actions: "Ask a question" (a mailto: link to the rep) and
   * "Print or save PDF" (the browser's print dialog). Both stack under the
   * primary Accept and are full-width on phone. The "Ask a question" link
   * is a plain anchor, not a button: the mailto: scheme is a customer-issued
   * action, so the page never holds an open mail draft.
   */
  private renderActionStack(q: QuoteViewRef): TemplateResult {
    // The "Ask a question" is a design-system secondary button whose click
    // opens the rep's mail client: the mailto URL is built in the click handler
    // (window.open is not a credential sink) so the page never carries a
    // binding inside an address attribute. The rep card's contact facts sit
    // above this stack; the button is the action the customer takes.
    const askLabel = (q.repEmail ?? "").trim() !== "" ? "Ask a question" : "Email us about this quote";
    return html`<div class="actionstack">
      ${button({ label: askLabel, kind: "secondary", action: "ask" })}
      ${button({ label: "Print or save PDF", kind: "secondary", action: "print" })}
    </div>`;
  }

  /**
   * The no-photo mark in a line's image cell: the till's line-thumb convention
   * (the product thumbnail component's drawn stone-stack fallback), ported.
   * The cell is always the same box; a line with a photo shows the photo, a
   * line without one shows this mark, so "no photo on file" is unmistakable
   * and never reads as an image that has not loaded yet.
   */
  private renderThumbMark(): TemplateResult {
    return html`<svg class="thumbmark" viewBox="0 0 24 24" fill="none" aria-hidden="true" role="presentation">
      <rect x="2" y="14" width="20" height="5" rx="1" stroke="currentColor" stroke-width="1.5" />
      <rect x="4" y="9" width="16" height="5" rx="1" stroke="currentColor" stroke-width="1.5" />
      <rect x="7" y="4" width="10" height="5" rx="1" stroke="currentColor" stroke-width="1.5" />
    </svg>`;
  }

  /** The information mark of the notice callout: a ringed i, drawn in the info ink. */
  private renderNoticeIcon(): TemplateResult {
    return html`<svg
      class="noticeicon"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      role="presentation"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v5" />
      <path d="M12 8h.01" />
    </svg>`;
  }

  /** The success mark: the design system's done-marker check, success ink. */
  private renderOkMark(): TemplateResult {    return html`<svg
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
        ${a.quantitiesConfirmed ? html`<p class="sub">You confirmed the quantities on this quote.</p>` : nothing}
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
    // A preview offers no way to accept and says nothing about acceptance: no form, no typed name,
    // and no "closed" card either, because the band above already says what this page is.
    if (q.preview || this.hosted || this.printCopy) return nothing;
    if (!q.acceptable || this.landed) {
      // A CONVERTED quote shows no closed card either (the review's observation B): its document is
      // titled as the sales order and the confirmation line on that band says what happened, so a door
      // marked "not awaiting acceptance" would speak to a decision already made. (A CONVERTED quote
      // the dealer left acceptable under multi accept falls past here to the form and its repeat note.)
      if (q.status === "CONVERTED") return nothing;
      // The customer reads this alone, with no staff beside them to translate,
      // so the state is SAID, in the same six states said mid-sentence: under
      // a heading, so the closed door reads deliberate rather than broken. An
      // ACCEPTED quote is not a refusal at all: it is the customer's own
      // earlier action, acknowledged under the success mark.
      const acceptedElsewhere = q.status === "ACCEPTED" || this.landed;
      return html`<div class="card closed">
        <h2>${acceptedElsewhere ? this.renderOkMark() : nothing}${acceptedElsewhere ? "Already accepted" : "Acceptance closed"}</h2>
        ${this.keyReused ? html`<p class="gone">${KEY_REUSED}</p>` : nothing}
        <p class="gone">
          ${q.expired && !acceptedElsewhere
            ? `This quote expired on ${plainDay(q.validUntil ?? "")} and can no longer be accepted online. Please contact us for an updated quote.`
            : `This quote is ${quoteStatusPhrase(q.status)} and is not awaiting acceptance.`}
        </p>
      </div>`;
    }
    const hint = acceptHint(this.lockedName ?? this.typedName, this.affirmed, this.quantitiesConfirmed);
    const adjusted = this.adjustedMoney(q);
    return html`
      <div class="card">
        <h2 class="card-h2">Accept this quote</h2>
        ${q.status === "ACCEPTED" || q.status === "CONVERTED" ? html`<p class="note">${REPEAT_NOTE}</p>` : nothing}
        ${this.renderDepositLine(q)}
        <div class="tick">
          <input id="qty-confirm" type="checkbox" />
          <label for="qty-confirm">I have reviewed the quantities on this quote and they are what I want.</label>
        </div>
        <label for="name">Your full name</label>
        <input id="name" type="text" autocomplete="name" />
        ${this.lockedName !== null ? html`<p class="note">${NAME_LOCKED}</p>` : nothing}
        <div class="tick">
          <input id="affirm" type="checkbox" />
          <label for="affirm">
            ${withFullStop(
              `I accept this quote for ${acceptanceAmountPhrase(adjusted)} and confirm I am authorised to do so on behalf of ${q.customerName}`,
            )}
          </label>
        </div>
        ${this.renderPaymentOptions(q)}
        ${button({ label: this.busy ? "Recording…" : "Accept quote", action: "accept", disabled: this.busy || !acceptReady(this.lockedName ?? this.typedName, this.affirmed, this.quantitiesConfirmed) })}
        ${hint !== null ? html`<p class="note" id="accept-hint">${hint}</p>` : nothing}
        ${this.error ? html`<p class="err" role="alert">${this.error}</p>` : nothing}
        <p class="note">
          Accepting records your typed name, the date and time, and the link you used. This is an electronic acceptance:
          we do not collect a handwritten signature.
        </p>
        ${this.renderActionStack(q)}
      </div>
    `;
  }

  render(): TemplateResult {
    // The dealer's header band opens EVERY state of the page: whose quote
    // this is does not depend on the quote having loaded (or on the link
    // being alive).
    return html`<header class="dealer"><span class="dealer-name">${this.dealerName}</span></header>${this.renderBody()}`;
  }

  /**
   * The title: a quote reads "Quote #<number>"; once converted it reads as the order, "Sales order
   * <number>, converted from #<quote number>", on the gold band. A core that sent no order number is
   * never given one: the page keeps the quote's number and says only that it became an order.
   * Beside that line the band carries the confirmation the accepted page already uses
   * (FROM-PRINCIPAL 29): the decision is made, so the page confirms it rather than asking again.
   */
  private renderTitle(q: QuoteViewRef): TemplateResult {
    if (q.status !== "CONVERTED") return html`<h1>Quote ${quoteNumberLabel(q.quoteNo)}</h1>`;
    const confirmed = html`<p class="orderconfirmed">You confirmed the quantities on this quote.</p>`;
    if (q.orderNo === null) {
      return html`<div class="orderband"><h1>Quote ${quoteNumberLabel(q.quoteNo)}<span class="from">, now an order</span></h1>${confirmed}</div>`;
    }
    return html`<div class="orderband">
      <h1>Sales order ${q.orderNo}<span class="from">, converted from ${quoteNumberLabel(q.quoteNo)}</span></h1>
      ${confirmed}
    </div>`;
  }

  private renderBody(): TemplateResult {
    if (this.previewRefused) {
      return html`<div class="card refusal">
        <h2>Preview unavailable</h2>
        <p class="gone">${PREVIEW_REFUSED}</p>
      </div>`;
    }
    if (this.fatal) {
      return html`<div class="card refusal">
        <h2>Quote link unavailable</h2>
        <p class="gone">${this.fatal}</p>
      </div>`;
    }
    const q = this.quote;
    if (!q) return html`<p class="sub">Loading your quote…</p>`;
    const withImages = showsImages(q);
    const adjusted = this.adjustedMoney(q);
    return html`
      ${q.preview ? html`<div class="previewband" role="status">${PREVIEW_BAND}</div>` : nothing}
      ${this.renderTitle(q)}
      <!-- The status badge rides the meta line beside the prepared-for / last-updated words.
           A CONVERTED quote carries no valid-until (the document is the order, the line below
           already says so); an expired quote names itself expired, so the badge and the
           sub-line agree. -->
      <div class="sub metaline">
        ${this.renderStatusBadge(q)}
        <span
          >Prepared for ${q.customerName} · last updated ${this.when(q.asOf)}${q.validUntil && q.status !== "CONVERTED"
            ? html` · ${q.expired ? "expired" : "valid until"} ${plainDay(q.validUntil)}`
            : nothing}</span
        >
      </div>
      <div class="facts">
        ${this.renderJobSiteCard(q)}
        ${this.renderRepCard(q)}
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
                <th class="num amount">Amount</th>
              </tr>
            </thead>
            <tbody>
              ${q.lines.map((l) => {
                const isOptional = l.optional === true;
                const excluded = isOptional && this.optionalExcluded.has(l.lineNo);
                return html`<tr class="${isOptional ? `optionalrow${excluded ? " excluded" : ""}` : ""}">
                  ${withImages
                    ? html`<td>
                        <span
                          class="thumbslot"
                          data-thumb=${l.imageRef}
                          data-tile=${this.tileFor(l) === null ? nothing : l.description}
                          >${l.imageRef || this.tileFor(l) !== null ? nothing : this.renderThumbMark()}</span
                        >
                      </td>`
                    : nothing}
                  <td class="item">
                    ${l.description}
                    ${(l.discPct ?? 0) > 0 ? html`<div class="discnote">${l.discPct}% discount applied</div>` : nothing}
                    ${isOptional
                      ? html`<div class="tick optionalcell-row">
                          <input
                            class="optionalcell"
                            type="checkbox"
                            checked
                            data-line-no=${l.lineNo}
                            aria-label=${`Include ${l.description}`}
                          />
                          <span class="optionallabel">Optional add-on ${excluded ? "(excluded)" : "(included)"}</span>
                        </div>`
                      : nothing}
                  </td>
                  <td class="num">${l.qtyEntered}</td>
                  <td class="uom">${l.uomEntered}</td>
                  <td class="num">${formatMoney(l.unitPriceCents)}</td>
                  <td class="num amount">${formatMoney(l.lineTotalCents)}</td>
                </tr>`;
              })}
            </tbody>
          </table>
        </div>
        <!-- The pre-acceptance notice asks the customer to review before accepting (FROM-PRINCIPAL
             29); on a converted quote the acceptance is done, so the notice and its box go. -->
        ${q.quantityNotice && q.status !== "CONVERTED"
          ? html`<div class="qtynotice" role="note">${this.renderNoticeIcon()}<span class="noticetext">${q.quantityNotice}</span></div>`
          : nothing}
        ${this.renderTermsFacts(q)}
        ${this.renderTotals(adjusted)}
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

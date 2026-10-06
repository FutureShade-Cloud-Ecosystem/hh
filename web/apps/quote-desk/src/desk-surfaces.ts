// The desk host (decision log rows 955 to 957): the navigation bar under the shared header and the surface the
// address names. It parses the hash with the surface table, applies the role wall, and mounts exactly one surface:
// Quotes and the quote page (the builder), Approvals and its request detail, Pricing, Settings, or the clean
// "no access" page for a route the role cannot see. Back and forward are the browser's: every walk is a hash.
//
// The Approvals tab carries the pending count of margin requests, read on arrival, on every route change, on every
// quote-changed announcement (a decision anywhere), on the tab regaining focus, and on a slow poll, so a request a
// rep files while the manager is on another surface shows up. Only the Sales manager reads the queue.
import { LitElement, css, html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import "./desk-nav.js";
import "./no-access.js";
import "./approvals-queue.js";
import "./approval-detail.js";
import "./pricing-surface.js";
import "./settings-page.js";
import "./quote-builder.js";
import {
  canSee,
  DESK_TAB_LABEL,
  deskTabOf,
  parseSurfaceRoute,
  QUOTE_TAB_LABEL,
  roleOf,
  type SurfaceRoute,
} from "./surface-route.js";
import { MARGIN_POLICY, isDecidable } from "./_approval-copy.js";
import type { QdClient } from "./qd-client.js";

/** How often the pending count is read again while nothing else asks for it. */
const COUNT_POLL_MS = 30_000;

@customElement("qd-desk-surfaces")
export class QdDeskSurfaces extends LitElement {
  @property({ attribute: false }) client: QdClient | null = null;
  @property({ attribute: "role-code" }) roleCode = "";
  @property({ attribute: "branch-id" }) branchId = "";

  @state() private route: SurfaceRoute = parseSurfaceRoute(window.location.hash);
  @state() private pending = 0;
  private countSeq = 0;
  private poll: number | null = null;

  static styles = css`
    :host {
      display: block;
    }
    [data-hidden="true"] {
      display: none;
    }
    qd-desk-nav {
      padding: 0 var(--hh-s-16, 16px);
      border-bottom: 1px solid var(--hh-line);
    }
  `;

  override connectedCallback(): void {
    super.connectedCallback();
    window.addEventListener("hashchange", this.onHash);
    window.addEventListener("quote-changed", this.onChanged);
    document.addEventListener("visibilitychange", this.onChanged);
    this.poll = window.setInterval(this.onChanged, COUNT_POLL_MS);
    this.route = parseSurfaceRoute(window.location.hash);
    void this.loadCount();
  }

  override disconnectedCallback(): void {
    window.removeEventListener("hashchange", this.onHash);
    window.removeEventListener("quote-changed", this.onChanged);
    document.removeEventListener("visibilitychange", this.onChanged);
    if (this.poll !== null) window.clearInterval(this.poll);
    this.poll = null;
    super.disconnectedCallback();
  }

  private readonly onHash = (): void => {
    this.route = parseSurfaceRoute(window.location.hash);
    void this.loadCount();
  };

  /** A decision or an edit anywhere: the queue and the settings re-read, and so does the count. */
  private readonly onChanged = (): void => {
    this.renderRoot.querySelectorAll<HTMLElement & { reload?: () => void }>("qd-approvals-queue, qd-settings-page").forEach((el) => el.reload?.());
    void this.loadCount();
  };

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("client") || changed.has("roleCode")) void this.loadCount();
  }

  private async loadCount(): Promise<void> {
    const client = this.client;
    if (client === null || roleOf(this.roleCode) !== "manager") {
      this.pending = 0;
      return;
    }
    const seq = ++this.countSeq;
    const result = await client.listApprovals("PENDING");
    if (seq !== this.countSeq || !result.ok) return;
    this.pending = result.value.filter((row) => row.policy_type === MARGIN_POLICY && isDecidable(row.status)).length;
  }

  protected override updated(): void {
    // The wiring pass: the surfaces receive their facts as every shared component does.
    const feed = (selector: string, facts: Record<string, unknown>): void => {
      const el = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>(selector);
      if (el !== null) Object.assign(el, facts);
    };
    const role = roleOf(this.roleCode);
    feed("qd-desk-nav", { deskRole: role, active: deskTabOf(this.route), pending: this.pending });
    feed("qd-no-access", { surface: this.surfaceName(this.route) });
    feed("qd-approval-detail", { requestId: this.route.kind === "approval" ? this.route.id : "" });
    feed("qd-quote-builder", { client: this.client, route: this.route, deskRole: role });
    feed("qd-approvals-queue", { client: this.client });
    feed("qd-approval-detail", { client: this.client });
    feed("qd-pricing-surface", { client: this.client });
    feed("qd-settings-page", { client: this.client, branchId: this.branchId });
  }

  /** What the no access page calls the surface the route names. */
  private surfaceName(route: SurfaceRoute): string {
    if (route.kind === "quote") return `The ${QUOTE_TAB_LABEL[route.tab]} tab`;
    return `The ${DESK_TAB_LABEL[deskTabOf(route)]} page`;
  }

  override render() {
    const role = roleOf(this.roleCode);
    const route = this.route;
    const allowed = canSee(role, route);
    const onQuotes = route.kind === "quotes" || route.kind === "compose" || route.kind === "quote";
    const surface = !allowed
      ? html`<qd-no-access></qd-no-access>`
      : route.kind === "approvals"
        ? html`<qd-approvals-queue></qd-approvals-queue>`
        : route.kind === "approval"
          ? html`<qd-approval-detail></qd-approval-detail>`
          : route.kind === "pricing"
            ? html`<qd-pricing-surface></qd-pricing-surface>`
            : route.kind === "settings"
              ? html`<qd-settings-page></qd-settings-page>`
              : nothing;
    return html`
      <qd-desk-nav></qd-desk-nav>
      ${surface}
      <!-- The builder stays mounted while another surface shows (its quote's late answers must still land), and
           shows only on the quote routes the role may see. -->
      <qd-quote-builder data-hidden=${onQuotes && allowed ? "false" : "true"}></qd-quote-builder>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-desk-surfaces": QdDeskSurfaces;
  }
}

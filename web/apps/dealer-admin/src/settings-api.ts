// The deployment settings surface's one door to the core: the dealer's
// brand, the card rail's configuration, the counter's policy and its
// history, and the walkthrough's policy and step copy, over the generated
// api-client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the screens use. A field the projection
// does not name is dropped at this door and never reaches a screen: the card
// rail's read carries no credential, because the core never echoes one and
// neither does this app.
//
// THE WRITES GO TO THE SERVER AND NEVER QUEUE. The card rail's configuration
// and the counter's policy are money and policy writes: they are awaited
// Result calls against the core, this app holds no outbox and no offline
// store, and nothing about them is ever deferred.
//
// Port provenance: the counter policy's knobs are the original app's
// pos-counter-settings.ts (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md), rebuilt on HH's generated client and narrowed to what this
// app's screens use.
import { ApiClient, staffPayment, staffPos, staffSettings, staffTour, type Result } from "@hardscapehouse/api-client";

/** The card rail's status, exactly what the contract returns and no more. */
export interface PaymentsConfig {
  readonly configured: boolean;
  readonly merchant_id: string;
  readonly processor: string;
  readonly currency: string;
  readonly simulated: boolean;
}

/** The counter's policy, the org wide default the form reads and writes. */
export interface CounterPolicy {
  readonly branch_id: string;
  readonly cod_enabled: boolean;
  readonly restocking_fee_bps: number;
  readonly notes: string;
  readonly line_presentation: string;
  readonly quick_chips_enabled: boolean;
  readonly workspace_split_pct: number;
  readonly workspace_arrangement: string;
  readonly lookup_presentation: string;
  readonly margin_visibility: string;
  readonly balance_visibility: string;
  readonly supplier_deposit_cancel: string;
  readonly quick_chip_set: readonly string[];
}

/** One policy history row: what changed, from what to what, by who, when. */
export interface PolicyChange {
  readonly field: string;
  readonly before: string;
  readonly after: string;
  readonly who: string;
  readonly when: string;
}

/** One step the copy editor can pick, as the read carries it. */
export interface TourStepRow {
  readonly step_key: string;
  readonly line: string;
}

/** One chapter of the walkthrough, its steps in the server's order. */
export interface TourChapterRow {
  readonly chapter_key: string;
  readonly title: string;
  readonly promise: string;
  readonly route: string;
  readonly steps: readonly TourStepRow[];
}

/** The walkthrough's read: the chapters and the server's own can_edit. */
export interface TourView {
  readonly chapters: readonly TourChapterRow[];
  readonly can_edit: boolean;
}

/** The dealer's brand, exactly what the contract returns and no more. */
export interface TenantBrand {
  readonly tenant_id: string;
  readonly display_name: string;
  readonly logo_attachment_id: string | null;
  readonly primary_color: string | null;
  readonly footer_text: string;
}

/** One step copy edit: the chapter, the step, and the three fields the platform gate owns. */
export interface TourStepEdit {
  readonly chapter_key: string;
  readonly step_key: string;
  readonly line: string;
  readonly route: string;
  readonly anchor: string;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const bool = (value: unknown): boolean => (typeof value === "boolean" ? value : false);
const maybeText = (value: unknown): string | null => (typeof value === "string" && value !== "" ? value : null);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

/** Every call the deployment settings surface makes. */
export class SettingsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // -- the brand ------------------------------------------------------------------------------------

  /** The dealer's brand record. Contract only today: the core answers the bare 404 of an
   * unregistered route, and the screen's waits state is the honest answer to it. */
  async brand(tenantId: string): Promise<Result<TenantBrand>> {
    const answer = await staffSettings.getTenantBrand(this.#client, tenantId);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        tenant_id: text(r.tenant_id),
        display_name: text(r.display_name),
        logo_attachment_id: maybeText(r.logo_attachment_id),
        primary_color: maybeText(r.primary_color),
        footer_text: text(r.footer_text),
      },
    };
  }

  /** Save the dealer's brand record, whole. */
  async putBrand(tenantId: string, brand: TenantBrand): Promise<Result<TenantBrand>> {
    const answer = await staffSettings.putTenantBrand(this.#client, tenantId, { ...brand });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        tenant_id: text(r.tenant_id),
        display_name: text(r.display_name),
        logo_attachment_id: maybeText(r.logo_attachment_id),
        primary_color: maybeText(r.primary_color),
        footer_text: text(r.footer_text),
      },
    };
  }

  // -- the card rail ---------------------------------------------------------------------------------

  /** The card rail's status: configured, merchant, processor, currency, and the test mode mark. */
  async paymentsConfig(): Promise<Result<PaymentsConfig>> {
    const answer = await staffPayment.paymentGetConfig(this.#client);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        configured: bool(r.configured),
        merchant_id: text(r.merchant_id),
        processor: text(r.processor),
        currency: text(r.currency),
        simulated: bool(r.simulated),
      },
    };
  }

  /**
   * Configure the card rail. The body carries the three fields the screen
   * owns and nothing else: no credential, no surcharge switch (the core
   * refuses the enable with its own sentence, and no tender path computes
   * one), no base url. The write goes to the server and never queues.
   */
  async putPaymentsConfig(form: { processor: string; merchant_id: string; currency: string }): Promise<Result<PaymentsConfig>> {
    const answer = await staffPayment.paymentPutConfig(this.#client, {
      processor: form.processor,
      merchant_id: form.merchant_id,
      currency: form.currency,
    });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        configured: bool(r.configured),
        merchant_id: text(r.merchant_id),
        processor: text(r.processor),
        currency: "",
        simulated: false,
      },
    };
  }

  // -- the counter's policy ---------------------------------------------------------------------------

  /** The counter's policy, the org wide default. */
  async counterPolicy(): Promise<Result<CounterPolicy>> {
    const answer = await staffPos.posGetPolicy(this.#client);
    if (!answer.ok) return answer;
    return { ok: true, value: toCounterPolicy(answer.value) };
  }

  /** Set the counter's policy, whole. A policy write goes to the server and never queues. */
  async putCounterPolicy(policy: CounterPolicy): Promise<Result<CounterPolicy>> {
    const answer = await staffPos.posPutPolicy(this.#client, {
      branch_id: policy.branch_id,
      cod_enabled: policy.cod_enabled,
      restocking_fee_bps: policy.restocking_fee_bps,
      notes: policy.notes,
      line_presentation: policy.line_presentation as never,
      quick_chips_enabled: policy.quick_chips_enabled,
      workspace_split_pct: policy.workspace_split_pct,
      workspace_arrangement: policy.workspace_arrangement as never,
      lookup_presentation: policy.lookup_presentation as never,
      margin_visibility: policy.margin_visibility as never,
      balance_visibility: policy.balance_visibility as never,
      supplier_deposit_cancel: (policy.supplier_deposit_cancel === "KEPT" ? "KEPT" : "CREDIT_BACK") as never,
      quick_chip_set: [...policy.quick_chip_set],
    });
    if (!answer.ok) return answer;
    return { ok: true, value: toCounterPolicy(answer.value) };
  }

  /** The policy's history: one row per changed knob, before to after. */
  async counterPolicyHistory(): Promise<Result<readonly PolicyChange[]>> {
    const answer = await staffPos.posPolicyHistory(this.#client, { limit: 50 });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return {
      ok: true,
      value: list(page.items).map((raw) => {
        const r = bag(raw);
        return {
          field: text(r.field),
          before: text(r.before),
          after: text(r.after),
          who: text(r.who),
          when: text(r.when),
        };
      }),
    };
  }

  // -- the walkthrough ---------------------------------------------------------------------------------

  /** The walkthrough as this caller reads it: the chapters, and the server's own can_edit. */
  async tour(): Promise<Result<TourView>> {
    const answer = await staffTour.tourGet(this.#client);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        chapters: list(r.chapters).map((raw) => {
          const c = bag(raw);
          return {
            chapter_key: text(c.chapter_key),
            title: text(c.title),
            promise: text(c.promise),
            route: text(c.route),
            steps: list(c.steps).map((s) => {
              const step = bag(s);
              return { step_key: text(step.step_key), line: text(step.line) };
            }),
          };
        }),
        can_edit: bool(r.can_edit),
      },
    };
  }

  /** Flip the demo switch. The platform gate answers a dealer's administrator in its own words. */
  async putTourPolicy(branchId: string, demoMode: boolean): Promise<Result<true>> {
    const answer = await staffTour.tourPutPolicy(this.#client, { branch_id: branchId, demo_mode: demoMode });
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** Save one step's copy. The platform gate owns the answer here too. */
  async putTourStepCopy(edit: TourStepEdit): Promise<Result<true>> {
    const answer = await staffTour.tourPutStepCopy(this.#client, { ...edit });
    return answer.ok ? { ok: true, value: true } : answer;
  }
}

function toCounterPolicy(raw: unknown): CounterPolicy {
  const r = bag(raw);
  return {
    branch_id: text(r.branch_id),
    cod_enabled: bool(r.cod_enabled),
    restocking_fee_bps: num(r.restocking_fee_bps),
    notes: text(r.notes),
    line_presentation: text(r.line_presentation),
    quick_chips_enabled: bool(r.quick_chips_enabled),
    workspace_split_pct: num(r.workspace_split_pct),
    workspace_arrangement: text(r.workspace_arrangement),
    lookup_presentation: text(r.lookup_presentation),
    margin_visibility: text(r.margin_visibility),
    balance_visibility: text(r.balance_visibility),
    supplier_deposit_cancel: text(r.supplier_deposit_cancel),
    quick_chip_set: list(r.quick_chip_set).filter((c): c is string => typeof c === "string"),
  };
}

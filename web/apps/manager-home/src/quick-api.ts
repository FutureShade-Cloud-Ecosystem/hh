// Manager Home's quick-actions door: the caller's resolved quick actions, over
// the generated api-client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the bar's screens use. A field the
// projection does not name is dropped at this door and never reaches a
// screen. The server has already resolved the role gate and the admin's
// visibility config before this door is called; the client re-checks none of
// it (quick-logic.ts carries that rule).
//
// Port provenance: the wire vocabulary is the original app's quick-action
// registry (repository hardscapeos_dibbits, read only; see PROVENANCE.md),
// rebuilt on HH's generated client and narrowed to the bar this slice builds.
import { ApiClient, staffDashboard, type Result } from "@hardscapehouse/api-client";

// -- the caller's resolved quick actions --------------------------------------------------------

export interface QuickAction {
  readonly action_id: string;
  readonly label: string;
  readonly glyph: string;
  /** Tooltip AND accessible name in the core's one string, so the two can never drift. */
  readonly title: string;
  /** The shell unit key the core names (e.g. "sales"), not a second router. */
  readonly unit: string;
  /** The shell screen key the core names (e.g. "pos"). */
  readonly screen: string;
  /** What the destination is asked to do on arrival (e.g. "pos.new_ticket"). */
  readonly intent: string;
  /** The one-line explanation an admin reads next to the toggle. */
  readonly lead: string;
}

/** GET /api/v1/quick-actions. */
export interface QuickActionsMine {
  /** The caller's resolved job shape, carried for the admin surface and for diagnosis. */
  readonly profile: string;
  readonly actions: readonly QuickAction[];
}

/**
 * One action as the ADMIN surface sees it: the catalog entry plus the resolved
 * switch and a per-shape answer for EVERY job shape, so an unstored shape
 * shows the default it is actually getting rather than an empty cell. `stored`
 * is false when nothing has ever been saved; the view says "default", not
 * "chosen".
 */
export interface QuickActionConfig extends QuickAction {
  readonly enabled: boolean;
  readonly profiles: Readonly<Record<string, boolean>>;
  readonly stored: boolean;
}

/** GET /api/v1/quick-actions/config (admin). */
export interface QuickConfigAnswer {
  readonly actions: readonly QuickActionConfig[];
  /** The closed set of job shapes, in the core's own order. */
  readonly profiles: readonly string[];
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);
const boolMap = (value: unknown): Readonly<Record<string, boolean>> => {
  const out: Record<string, boolean> = {};
  for (const [key, v] of Object.entries(bag(value))) out[key] = v === true;
  return out;
};

export function toQuickAction(raw: unknown): QuickAction {
  const r = bag(raw);
  return {
    action_id: text(r.action_id),
    label: text(r.label),
    glyph: text(r.glyph),
    title: text(r.title),
    unit: text(r.unit),
    screen: text(r.screen),
    intent: text(r.intent),
    lead: text(r.lead),
  };
}

export function toQuickActionsMine(raw: unknown): QuickActionsMine {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    actions: list(r.actions).map(toQuickAction),
  };
}

export function toQuickActionConfig(raw: unknown): QuickActionConfig {
  const r = bag(raw);
  return {
    ...toQuickAction(raw),
    enabled: r.enabled === true,
    profiles: boolMap(r.profiles),
    stored: r.stored === true,
  };
}

export function toQuickConfigAnswer(raw: unknown): QuickConfigAnswer {
  const r = bag(raw);
  return {
    actions: list(r.actions).map(toQuickActionConfig),
    profiles: list(r.profiles).map((p) => text(p)).filter((p) => p !== ""),
  };
}

/**
 * The quick-actions calls this app makes: the caller's resolved registry rows,
 * and the admin's config surface (the core decides who gets the config; a
 * refused caller carries the refusal, its own words, to the screen).
 */
export class QuickApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /**
   * The actions this caller may see, resolved server side against the role
   * gate and the admin's configuration, in catalog order. An empty list is the
   * honest answer for "you see none of these".
   */
  async mine(): Promise<Result<QuickActionsMine>> {
    const answer = await staffDashboard.dashboardGetQuickActions(this.#client);
    return answer.ok ? { ok: true, value: toQuickActionsMine(answer.value) } : answer;
  }

  /** Every registered action with its resolved switch and per-shape matrix (admin). */
  async config(): Promise<Result<QuickConfigAnswer>> {
    const answer = await staffDashboard.dashboardGetQuickActionConfig(this.#client);
    return answer.ok ? { ok: true, value: toQuickConfigAnswer(answer.value) } : answer;
  }

  /**
   * Edit one action's visibility (admin). The answer is the action as it now
   * resolves, which the view draws in place of its draft; a refusal arrives in
   * the core's own words.
   */
  async putConfig(action: string, enabled: boolean, profiles: Readonly<Record<string, boolean>>): Promise<Result<QuickActionConfig>> {
    const answer = await staffDashboard.dashboardPutQuickActionConfig(this.#client, action, {
      enabled,
      profiles: { ...profiles },
    });
    return answer.ok ? { ok: true, value: toQuickActionConfig(answer.value) } : answer;
  }
}

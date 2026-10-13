// The role configuration surface's one door to the core: the per surface,
// per job shape curation reads and writes (the settings kit's substrate),
// over the generated api-client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the screens use. A field the projection
// does not name is dropped at this door and never reaches a screen. Nothing
// here sums, converts or derives a figure.
//
// THE TWO LEVELS THE KIT CARRIES. The dealer's curation of a job shape
// (PUT/DELETE /items, admin gated in the kit's service) and the caller's own
// override and workspace row (PUT/DELETE /my-items and /my-workspace,
// self-service by design: hiding a column is not an admin act). The reset is
// always a DELETE; a write with an empty set is refused by the core in its
// own words.
//
// Port provenance: the editor's shape follows the original app's
// quote-surface-settings.ts items half, the DIB-507 screen settings page
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md), rebuilt on
// HH's generated client and narrowed to what this app's screens use.
import { ApiClient, staffSettings, type Result } from "@hardscapehouse/api-client";
import type { ConfigItem } from "./config-logic.js";

/** One cell of the grid: the curation rows and the caller's own answer. */
export interface CellConfig {
  readonly surface_id: string;
  readonly profile: string;
  readonly items_profile: string;
  readonly role_items: readonly ConfigItem[];
  readonly user_items: readonly ConfigItem[];
  readonly workspace: WorkspaceView;
}

/** The caller's resolved workspace row, as the read carries it. */
export interface WorkspaceView {
  readonly split_pct: number;
  readonly arrangement: string;
  readonly primary_collapsed: boolean;
  readonly secondary_collapsed: boolean;
  readonly cost_hidden: boolean;
  readonly source: string;
}

/** The body a my-workspace save carries; the key makes a replay read back. */
export interface WorkspaceSave {
  readonly split_pct: number;
  readonly arrangement: string;
  readonly primary_collapsed: boolean;
  readonly secondary_collapsed: boolean;
  readonly cost_hidden: boolean;
  readonly open_ref: string;
  readonly idempotency_key: string;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const bool = (value: unknown): boolean => (typeof value === "boolean" ? value : false);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

function toItem(raw: unknown): ConfigItem {
  const r = bag(raw);
  return { kind: text(r.kind), id: text(r.id), position: num(r.position), visible: bool(r.visible) };
}

function toWorkspace(raw: unknown): WorkspaceView {
  const r = bag(raw);
  return {
    split_pct: num(r.split_pct),
    arrangement: text(r.arrangement),
    primary_collapsed: bool(r.primary_collapsed),
    secondary_collapsed: bool(r.secondary_collapsed),
    cost_hidden: bool(r.cost_hidden),
    source: text(r.source),
  };
}

/** Every call the role configuration surface makes. */
export class ConfigApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /**
   * One cell: the dealer's curation of one job shape on one surface, plus
   * the caller's own override and workspace row. Asking for a job shape
   * other than the caller's own is the read half of an admin act; the core
   * refuses anyone else in its own words.
   */
  async surfaceConfig(surface: string, profile: string): Promise<Result<CellConfig>> {
    const answer = await staffSettings.settingsGetSurfaceConfig(this.#client, surface, { profile: profile as never });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        surface_id: text(r.surface_id),
        profile: text(r.profile),
        items_profile: text(r.items_profile),
        role_items: list(r.role_items).map(toItem),
        user_items: list(r.user_items).map(toItem),
        workspace: toWorkspace(r.workspace),
      },
    };
  }

  /** Save the dealer's curation of one job shape on one surface, whole. */
  async putRoleItems(surface: string, profile: string, items: readonly ConfigItem[]): Promise<Result<true>> {
    const answer = await staffSettings.settingsPutRoleItems(this.#client, surface, {
      profile: profile as never,
      items: items.map((it) => ({ ...it, kind: it.kind as never })),
    });
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** Reset one kind to the level beneath the dealer: the reset IS a DELETE. */
  async resetRoleItems(surface: string, profile: string, kind: string): Promise<Result<true>> {
    const answer = await staffSettings.settingsDeleteRoleItems(this.#client, surface, {
      profile: profile as never,
      kind: kind as never,
    });
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** Save the caller's own override for one surface, whole. */
  async putMyItems(surface: string, items: readonly ConfigItem[]): Promise<Result<true>> {
    const answer = await staffSettings.settingsPutMyItems(this.#client, surface, {
      items: items.map((it) => ({ ...it, kind: it.kind as never })),
    });
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** Delete the caller's own override; reset IS a DELETE. */
  async resetMyItems(surface: string): Promise<Result<true>> {
    const answer = await staffSettings.settingsDeleteMyItems(this.#client, surface);
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** Save the caller's own workspace row; the key makes a replay read back. */
  async putMyWorkspace(surface: string, save: WorkspaceSave): Promise<Result<WorkspaceView>> {
    const answer = await staffSettings.settingsPutMyWorkspace(this.#client, surface, {
      ...save,
      arrangement: save.arrangement as never,
    });
    if (!answer.ok) return answer;
    return { ok: true, value: toWorkspace(answer.value) };
  }

  /** Delete the caller's own workspace row; reset IS a DELETE. */
  async resetMyWorkspace(surface: string): Promise<Result<true>> {
    const answer = await staffSettings.settingsDeleteMyWorkspace(this.#client, surface);
    return answer.ok ? { ok: true, value: true } : answer;
  }
}

// Dealer Admin's one door to the core: the staff register's reads and the
// two writes the staff surface owns (assign a role, request a password
// reset), over the generated api-client, answering a Result and never
// throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the screens use. A field the projection
// does not name is dropped at this door and never reaches a screen. Nothing
// here sums, converts or derives a figure.
//
// THE UNREGISTERED SURFACE. The whole staff identity admin family (the list,
// the roles catalog, the role write, the reset link) is contract-only today:
// the core's parity ledger holds it for the auth lane. The door is still the
// client's own operations, so the day the core registers them the app asks
// the real routes; until then every read answers the transport's refusal and
// the screens show the server's own words.
//
// Port provenance: the shapes are the original app's staff-roles.ts and
// staff-reset.ts reads (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md), rebuilt on HH's generated client and narrowed to what this
// app's screens use.
import {
  ApiClient,
  staffBranches,
  staffIdentity,
  staffSettings,
  type Result,
} from "@hardscapehouse/api-client";
import { asPlatformView, type PlatformHotkeyView } from "./hotkey-logic.js";

// -- the staff register ---------------------------------------------------------------------------

export interface StaffMemberRow {
  readonly user_id: string;
  readonly email: string;
  readonly display_name: string;
  readonly role_code: string;
  readonly role_name: string;
  readonly tier: number;
  readonly scope: string;
  readonly branch_ids: readonly string[];
}

export interface RoleRow {
  readonly code: string;
  readonly name: string;
  readonly tier: number;
  readonly scope: string;
}

/** The answer a role write carries: the server's own status words and the role it landed. */
export interface AssignOutcome {
  readonly status: string;
  readonly user_id: string;
  readonly role_code: string;
}

/** The single use link the reset request mints, as the route returns it. */
export interface ResetLink {
  readonly reset_url: string;
  readonly expires_at: string;
}

// -- the branches ---------------------------------------------------------------------------------

export interface BranchRow {
  readonly id: string;
  readonly code: string;
  readonly name: string;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

export function toStaffMemberRow(raw: unknown): StaffMemberRow {
  const r = bag(raw);
  return {
    user_id: text(r.user_id),
    email: text(r.email),
    display_name: text(r.display_name),
    role_code: text(r.role_code),
    role_name: text(r.role_name),
    tier: num(r.tier),
    scope: text(r.scope),
    branch_ids: list(r.branch_ids).filter((b): b is string => typeof b === "string"),
  };
}

export function toRoleRow(raw: unknown): RoleRow {
  const r = bag(raw);
  return {
    code: text(r.code),
    name: text(r.name),
    tier: num(r.tier),
    scope: text(r.scope),
  };
}

export function toBranchRow(raw: unknown): BranchRow {
  const r = bag(raw);
  return { id: text(r.id), code: text(r.code), name: text(r.name) };
}

/** Every call the staff and hotkeys surfaces make. */
export class DealerApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The staff register, the caller's grants applied server side. */
  async staff(): Promise<Result<readonly StaffMemberRow[]>> {
    const answer = await staffIdentity.identityListStaff(this.#client, { limit: 200 });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toStaffMemberRow) };
  }

  /** The roles catalog: every role a dealer's staff can hold, with its tier and reach. */
  async roles(): Promise<Result<readonly RoleRow[]>> {
    const answer = await staffIdentity.identityListRoles(this.#client, { limit: 200 });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toRoleRow) };
  }

  /** Assign one staff member a role from the catalog. A refusal arrives in the core's words. */
  async assignRole(id: string, roleCode: string): Promise<Result<AssignOutcome>> {
    const answer = await staffIdentity.identityAssignRole(this.#client, id, { role_code: roleCode });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { status: text(r.status), user_id: text(r.user_id), role_code: text(r.role_code) } };
  }

  /**
   * Request the single use reset link for one staff member. The link goes to
   * the person out of band; there is no email in the demo.
   */
  async requestPasswordReset(id: string): Promise<Result<ResetLink>> {
    const answer = await staffIdentity.identityGeneratePasswordReset(this.#client, id);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { reset_url: text(r.reset_url), expires_at: text(r.expires_at) } };
  }

  /** The dealer's branches, for the grant chips' names. */
  async branches(): Promise<Result<readonly BranchRow[]>> {
    const answer = await staffBranches.branchesList(this.#client);
    if (!answer.ok) return answer;
    return { ok: true, value: list(answer.value).map(toBranchRow) };
  }

  // -- the platform key rosters ------------------------------------------------------------------

  /**
   * The platform hotkey rosters, normalised at this door. A body this build
   * cannot read yields empty rosters and canEdit false (the safe answer to
   * "may I" when nobody said is no).
   */
  async platformHotkeys(): Promise<Result<PlatformHotkeyView>> {
    const answer = await staffSettings.settingsGetPlatformHotkeys(this.#client);
    if (!answer.ok) return answer;
    return { ok: true, value: asPlatformView(answer.value) };
  }

  /**
   * Save one surface's roster whole. Two panels means two writes,
   * deliberately: editing the counter's keys must not be able to touch the
   * app-wide map, and a single combined write is the shape in which that
   * could happen by accident.
   */
  async putPlatformHotkeys(surface: string, keys: readonly { action: string; binding: string; position: number }[]): Promise<Result<true>> {
    const answer = await staffSettings.settingsPutPlatformHotkeys(this.#client, surface, { keys: keys.map((k) => ({ ...k })) });
    return answer.ok ? { ok: true, value: true } : answer;
  }
}

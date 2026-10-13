// The demo adapter: this app's AccountSurface for the auth package's seam
// (web/packages/auth/src/client.ts), over the core's two demo routes. The auth
// package is unchanged: it injects the surface, this class is that injection.
//
// What the demo means here, per the plan's demo sign-in (row 480): a person
// taps a role card, the app asks `POST /api/v1/demo/session` with the flow
// name and nothing else, and the mint answers a token and the staff user
// block. The adapter holds that mint in memory and answers the seam's
// operations from it; the app hands the mint to AuthController.adopt, whose
// custody keeps the token in private memory and nothing anywhere else. There
// is no platform session behind the demo: the mint lives only in this tab,
// so a reload resumes nothing and the entry page comes back.
//
// Everything is validated, never cast: the mint's user block is the staff
// login user block (core/api/openapi.yaml, IdentityUser), read field by field,
// and the token's expiry is read from its own exp claim, because the mint
// response carries no expiry field. A mint that lacks a token, a usable user
// block, or a readable expiry is refused as data, like every other refusal in
// this app.
import { err, ok, type ApiClient, type DemoFlow, type DemoSessionResponse, type Result } from "@hardscapehouse/api-client";
import { staffDemo } from "@hardscapehouse/api-client";
import type { AccountSurface, AccountUser, ExchangeResult } from "@hardscapehouse/auth";

/** A mint answer in custody's own shape: exactly what AuthController.adopt takes. */
export interface MintedSession {
  token: string;
  tokenExpiresAtMs: number;
  user: AccountUser;
  /**
   * The role code the mint's user block states (IdentityUser requires it):
   * the server's own word about the account, carried for the receipt's
   * display wording. Nothing gates a request on it.
   */
  roleCode: string;
  /**
   * The rest of the mint's IdentityUser block, validated field by field:
   * role_name, tier and scope. The ported surface's session facts read these
   * (the role's name for the chrome, the tier and scope for the
   * platform-admin gate's shape), so the block is carried whole rather than
   * re-asked for.
   */
  identity: { roleName: string; tier: number; scope: string };
}

/**
 * A minted approver credential: the token for the one return call that may
 * carry it, plus the approver's display name for the receipt. This is not a
 * session: the adapter stores nothing of it, custody never sees it, and the
 * caller drops the token after the one call.
 */
export interface ApproverCredential {
  token: string;
  approverName: string;
  expiresAtMs: number;
}

/** The demo routes' listing and minting, plus the seam the auth package injects. */
export class DemoAccount implements AccountSurface {
  readonly #client: ApiClient;
  #mint: MintedSession | null = null;

  constructor(options: { client: ApiClient }) {
    this.#client = options.client;
  }

  /**
   * The role cards: the flows route's rows, or the refusal. An error here is
   * the no-demo page's whole story: a default-build core or a non-demo box
   * answers nothing, so there are no cards. Staff rows only: the contractor
   * flows (class "tenant") belong to the Pro apps' front door, and a row with
   * no class is a staff row.
   */
  async listFlows(): Promise<Result<DemoFlow[]>> {
    const result = await staffDemo.demoListFlows(this.#client);
    if (!result.ok) return result;
    return ok(result.value.flows.filter((row) => row.class !== "tenant"));
  }

  /** Mint a session for one flow name from the cards. */
  async chooseFlow(flow: string): Promise<Result<MintedSession>> {
    const result = await staffDemo.demoCreateSession(this.#client, { flow });
    if (!result.ok) return result;
    const minted = validateMint(result.value, Date.now());
    if (minted === null) {
      return err("the mint's answer is not a usable session: no token, no usable user block, or no readable expiry in the token");
    }
    this.#mint = minted;
    return ok({ ...minted });
  }

  /**
   * The approver's credential for a return above the amount (D49): the same
   * demo mint, asked for the approver flow's card in the Manager approval
   * dialog. This mint is NOT adopted: the clerk's session is never replaced,
   * and this adapter stores nothing of the answer. The token rides back to
   * the caller, which holds it in memory for the one call and drops it.
   */
  async mintApproverCredential(flow: string): Promise<Result<ApproverCredential>> {
    const result = await staffDemo.demoCreateSession(this.#client, { flow });
    if (!result.ok) return result;
    const minted = validateMint(result.value, Date.now());
    if (minted === null) {
      return err("the approver mint's answer is not a usable session: no token, no usable user block, or no readable expiry in the token");
    }
    return ok({ token: minted.token, approverName: minted.user.name, expiresAtMs: minted.tokenExpiresAtMs });
  }

  // The seam's remaining operations, answered from the held mint. The two
  // platform sign-ins refuse: the demo has no password and no OAuth flow
  // behind its cards, and a fake of either would sign nobody in.
  async createEmailPasswordSession(): Promise<AccountUser> {
    throw new Error("the demo signs in by tapping a role; there is no password sign-in behind the demo cards");
  }

  async createOAuth2Session(): Promise<void> {
    throw new Error("the demo signs in by tapping a role; there is no platform OAuth flow behind the demo cards");
  }

  async getSession(): Promise<AccountUser> {
    const mint = this.#mint;
    if (mint === null) {
      throw new Error("no demo session to resume: the mint lives only in this tab's memory, tap a role again");
    }
    return { ...mint.user };
  }

  // The demo has no exchange route to ask: the held mint IS the session the
  // demo core issued, so the exchange answers it back in the exchange's shape.
  async exchange(): Promise<ExchangeResult> {
    const mint = this.#mint;
    if (mint === null) return { ok: false, code: "no_session" };
    return {
      ok: true,
      answer: {
        token: mint.token,
        expiresAtMs: mint.tokenExpiresAtMs,
        user: {
          user_id: mint.user.userId,
          email: mint.user.email,
          display_name: mint.user.name,
          role_code: mint.roleCode,
          role_name: mint.identity.roleName,
          tier: mint.identity.tier,
          scope: mint.identity.scope,
        },
      },
    };
  }

  async deleteSession(): Promise<void> {
    this.#mint = null;
  }
}

/** Field-by-field, never a cast: the staff login user block this app needs. */
function userFromMint(raw: unknown): {
  user: AccountUser;
  roleCode: string;
  identity: { roleName: string; tier: number; scope: string };
} | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  const userId = record["user_id"];
  const email = record["email"];
  const name = record["display_name"];
  const roleCode = record["role_code"];
  const roleName = record["role_name"];
  const tier = record["tier"];
  const scope = record["scope"];
  if (typeof userId !== "string" || userId === "") return null;
  if (typeof email !== "string") return null;
  if (typeof name !== "string") return null;
  if (typeof roleCode !== "string" || roleCode === "") return null;
  if (typeof roleName !== "string") return null;
  if (typeof tier !== "number" || !Number.isInteger(tier)) return null;
  if (typeof scope !== "string" || scope === "") return null;
  return {
    user: { userId, email, name },
    roleCode,
    identity: { roleName, tier, scope },
  };
}

/**
 * The mint response carries no expiry field, so the token's own exp claim is
 * the one source: a JSON Web Token's middle segment, decoded. A token that is
 * not JWT shaped, or whose exp is missing or unreadable, refuses: custody
 * requires an expiry, and adopting a token with an unknown life is exactly the
 * confident wrong answer this codebase does not give.
 */
function expiryFromToken(token: string, now: number): number | null {
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  let payload: unknown;
  try {
    payload = JSON.parse(decodeBase64Url(parts[1]));
  } catch {
    return null;
  }
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) return null;
  const exp = (payload as Record<string, unknown>)["exp"];
  if (typeof exp !== "number" || !Number.isFinite(exp)) return null;
  const expiresAtMs = exp * 1000;
  return expiresAtMs > now ? expiresAtMs : null;
}

function decodeBase64Url(segment: string): string {
  const padded = segment.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

function validateMint(response: DemoSessionResponse, now: number): MintedSession | null {
  const token = response.token;
  if (typeof token !== "string" || token === "") return null;
  const mintedUser = userFromMint(response.user);
  if (mintedUser === null) return null;
  const tokenExpiresAtMs = expiryFromToken(token, now);
  if (tokenExpiresAtMs === null) return null;
  return {
    token,
    tokenExpiresAtMs,
    user: mintedUser.user,
    roleCode: mintedUser.roleCode,
    identity: mintedUser.identity,
  };
}

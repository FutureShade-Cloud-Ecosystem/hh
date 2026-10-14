// The host's pure state: the door (the front door's sign in and role picker,
// a page under the host) or the desk (the chrome around one hosted app).
// Sign in lands the role on its own app; Switch role drops the session and
// returns to the door. Pins and every per user store belong to the person and
// are never touched here: they wait for them (subject binding, the auth
// shell contract).
import { landingFor } from "./landing.js";
import { switcherFor, type WorkApp } from "./switcher.js";

/** The signed in person as the host holds it, in memory only. */
export interface HostSession {
  /** Where it came from: the front door's demo mint, or the shell's Console sign in. */
  readonly source: "persona" | "console";
  readonly userId: string;
  readonly email: string;
  readonly name: string;
  readonly roleCode: string;
  /** The role's words: the card's label for a persona, the role name for Console. */
  readonly roleLabel: string;
  readonly roleName: string;
  readonly token: string;
  readonly tokenExpiresAtMs: number;
  /** The demo flow id of a persona; the role code for a Console session. */
  readonly flow: string;
  readonly tier: number;
  readonly scope: string;
}

export type HostState =
  | { readonly phase: "door" }
  | {
      readonly phase: "desk";
      readonly session: HostSession;
      /** What the role's switcher holds, in the profile's order. */
      readonly apps: readonly WorkApp[];
      /** The hosted app, or null when the role holds none. */
      readonly currentAppId: string | null;
    };

export function initialState(): HostState {
  return { phase: "door" };
}

/** Sign in: the role's switcher from the profile's apps, and its landing. */
export function signedIn(_state: HostState, session: HostSession, apps: readonly WorkApp[]): HostState {
  const held = switcherFor(apps, session.roleCode);
  return { phase: "desk", session, apps: held, currentAppId: landingFor(session.roleCode, held.map((a) => a.id)) };
}

/** Open an app the role holds; anything else changes nothing. */
export function openApp(state: HostState, appId: string): HostState {
  if (state.phase !== "desk") return state;
  if (!state.apps.some((a) => a.id === appId)) return state;
  if (state.currentAppId === appId) return state;
  return { ...state, currentAppId: appId };
}

/** Switch role: the session is dropped and the door is back, from any app. */
export function switchRole(_state: HostState): HostState {
  return { phase: "door" };
}

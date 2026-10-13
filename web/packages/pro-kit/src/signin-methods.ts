// What the sign in card shows for a dealer's allowed methods, as plain data the element renders and a node
// test reads. Decision row 1592: contractors sign in by email link and Google, the boards win; row 1524: the
// methods are a per dealer set of four. The board's states (HH: Pro, shell-signin-phone-brand and
// build_signin_states.py): the demo set, every method, one method only, a method the dealer refuses.
//
// The words are the board's, and none names the machinery behind a sign in.
import type { Result } from "@hardscapehouse/api-client";
import type { TenantProfile, TenantSession } from "./session.js";
import { ROLE_LABELS, TENANT_ROLES } from "./capabilities.js";

export type SignInMethod = "email_link" | "google" | "microsoft" | "futurebuild";

/** What the card hands the app for a way other than email and password ("pro-signin-method"). */
export interface SignInMethodChoice {
  method: SignInMethod | "persona";
  email?: string;
  roleLabel?: string;
}

/** What the card hands the app for FutureBuild's own way ("pro-signin-submit"). */
export interface SignInSubmit {
  email: string;
  password: string;
}

/** Run whichever way the card chose through the session: the one place the two events meet. */
export function signInFromCard(session: TenantSession, input: SignInSubmit | SignInMethodChoice): Promise<Result<TenantProfile>> {
  if ("password" in input) return session.signIn(input.email, input.password);
  if (input.method === "persona") return session.signInWith({ method: "persona", roleLabel: input.roleLabel ?? "" });
  return session.signInWith({ method: input.method, email: input.email });
}

/** The demo dealer's set: the Pro boards' own state (email link, then Google). */
export const DEMO_METHODS: readonly SignInMethod[] = ["email_link", "google"];

/** The buttons' words. FutureBuild's own sign in (email and password) is the card's plain Sign in. */
export const METHOD_LABELS: Readonly<Record<SignInMethod, string>> = {
  email_link: "Email me a sign in link",
  google: "Continue with Google",
  microsoft: "Continue with Microsoft",
  futurebuild: "Sign in",
};

// How a method reads inside a sentence.
const IN_A_SENTENCE: Readonly<Record<SignInMethod, string>> = {
  email_link: "your email link",
  google: "Google",
  microsoft: "Microsoft",
  futurebuild: "your FutureBuild password",
};

const KNOWN: readonly SignInMethod[] = ["email_link", "futurebuild", "google", "microsoft"];

/**
 * The role the front door's contractor card asked for: its card opens the app at `?as=<role code>`, and in
 * the demo build the app signs that persona in itself (a Pro app keeps its bearer in memory, so the front
 * door cannot hand it one). Only the five contractor role codes ask for anything; the answer is the role's
 * label, the word the demo adapter picks a persona by.
 */
export function demoRoleFromAddress(search: string | null | undefined): string | null {
  if (typeof search !== "string") return null;
  const asked = new URLSearchParams(search).get("as");
  const role = TENANT_ROLES.find((r) => r === asked);
  return role === undefined ? null : ROLE_LABELS[role];
}

/** Where another Pro app opens: in the demo build it carries the signed in role, so the hop does not sign the person out. */
export function otherAppAddress(path: string, role: string | null | undefined, demo: boolean): string {
  const known = TENANT_ROLES.find((r) => r === role);
  return demo && known !== undefined ? `${path}?as=${known}` : path;
}

export interface SignInPlanInput {
  /** The methods the dealer allows; unknown and repeated entries fall away. */
  methods: readonly string[];
  /** A method the person tried that the dealer does not allow, or null. */
  refused?: string | null;
  /** The dealer's name from its config; empty reads as "Your dealer". */
  dealer: string;
}

export interface SignInButton {
  method: SignInMethod;
  label: string;
  primary: boolean;
}

export interface SignInPlan {
  /** The board state: some (the demo set), every, one, refused. */
  state: "some" | "every" | "one" | "refused";
  hint: string;
  /** The line under the Google button; empty when Google is not allowed. */
  googleNote: string;
  /** The refusal sentence, or null. */
  refusal: string | null;
  /** Whether the card asks for an email and for a password. */
  email: boolean;
  password: boolean;
  /** An "or" rule between the email buttons and the provider buttons. */
  divider: boolean;
  buttons: readonly SignInButton[];
}

function allowedOf(methods: readonly string[]): SignInMethod[] {
  return KNOWN.filter((m) => methods.includes(m));
}

function joinWords(parts: readonly string[]): string {
  return parts.length <= 1 ? (parts[0] ?? "") : `${parts.slice(0, -1).join(", ")} or ${parts[parts.length - 1]}`;
}

export function signInPlan(input: SignInPlanInput): SignInPlan {
  const dealer = input.dealer === "" ? "Your dealer" : input.dealer;
  // The card lists the email ways first, then the providers, in the board's order.
  const order: readonly SignInMethod[] = ["email_link", "futurebuild", "google", "microsoft"];
  const allowed = order.filter((m) => allowedOf(input.methods).includes(m));
  const buttons: SignInButton[] = allowed.map((method, index) => ({ method, label: METHOD_LABELS[method], primary: index === 0 }));
  const refusedMethod = KNOWN.find((m) => m === input.refused);
  const refusal =
    refusedMethod === undefined
      ? null
      : `${dealer} does not allow sign in with ${IN_A_SENTENCE[refusedMethod].replace("your FutureBuild password", "FutureBuild")}. Use ${joinWords(allowed.map((m) => IN_A_SENTENCE[m]))}.`;
  const state: SignInPlan["state"] = refusal !== null ? "refused" : allowed.length === 1 ? "one" : allowed.length === KNOWN.length ? "every" : "some";
  const hint =
    allowed.length === 0
      ? `${dealer} allows no way to sign in yet. Ask the dealer.`
      : state === "every"
        ? `${dealer} chooses the methods its contractors use.`
        : state === "one"
          ? "This dealer allows one method."
          : "Use the email your contractor account knows.";
  const emailWays = allowed.filter((m) => m === "email_link" || m === "futurebuild").length;
  const providers = allowed.filter((m) => m === "google" || m === "microsoft").length;
  return {
    state,
    hint,
    googleNote: allowed.includes("google") ? "Google works where your Google email matches your contractor account email." : "",
    refusal,
    email: emailWays > 0,
    password: allowed.includes("futurebuild"),
    divider: emailWays > 0 && providers > 0,
    buttons,
  };
}

// The set password page's pure parts and its own small state: the link it
// reads, the query it takes back out of the address bar, the password rules it
// checks before it asks, and the words every answer becomes. Nothing here
// touches the DOM, so the node suite asserts every word as text.
//
// THE WORDS ARE THE SCREEN'S, the same rule the sign in screen obeys: a refusal
// is said in plain words, never in a status code, and the offline words are the
// sign in screen's own.
import type { PasswordRecoverySurface } from "@hardscapehouse/auth/appwrite-adapter";
import { SIGN_IN_WORDS } from "./signin.js";

/** Where the page lives: the path the console's recovery link points at. */
export const SET_PASSWORD_PATH = "/set-password";

/** True for the page's own path, with or without its trailing slash. */
export function isSetPasswordPath(pathname: string): boolean {
  return pathname.replace(/\/+$/, "") === SET_PASSWORD_PATH;
}

/** The link's two fields: the user the secret belongs to, and the secret itself. */
export interface RecoveryLink {
  userId: string;
  secret: string;
}

/** Read the link out of a query string. Both fields or nothing: half a link is no link. */
export function readRecoveryLink(search: string): RecoveryLink | null {
  let params: URLSearchParams;
  try {
    params = new URLSearchParams(search);
  } catch {
    return null;
  }
  const userId = (params.get("userId") ?? "").trim();
  const secret = (params.get("secret") ?? "").trim();
  if (userId === "" || secret === "") return null;
  return { userId, secret };
}

/** The window the query is taken out of: only the two parts the page needs of it. */
export interface RecoveryWindow {
  readonly location: { readonly pathname: string; readonly search: string };
  readonly history: { replaceState(state: unknown, title: string, url: string): void };
}

/**
 * Take the secret out of the address bar, in place: the query goes, the path
 * stays. A secret left in the address bar is a secret in the browser's history
 * and in every referrer the page hands on.
 */
export function scrubRecoveryQuery(target: RecoveryWindow): void {
  target.history.replaceState(null, "", target.location.pathname);
}

/** The shortest password the console takes (FB Console is Appwrite 2.1.0: 8 to 256 characters). */
export const PASSWORD_MIN_LENGTH = 8;

/** Whether a password is long enough to be sent. */
export function passwordAcceptable(password: string): boolean {
  return password.length >= PASSWORD_MIN_LENGTH;
}

/** Whether the two entries are the same password. */
export function confirmMatches(password: string, confirm: string): boolean {
  return password === confirm;
}

/**
 * The words of every refusal, in the dealer's voice: sentence case, the
 * decision first, no exclamation marks, no status codes, no dashes.
 */
export const RECOVERY_WORDS: Readonly<Record<string, string>> = {
  no_link: "This page needs the link from your email.",
  mismatch: "The two passwords do not match. Type them again.",
  too_short: "Use at least 8 characters.",
  expired: "This link has expired or was already used. Ask for a new one.",
  password_refused: "That password will not do. Choose a different one and try again.",
  rate_limited: "Too many attempts. Wait a moment, then try again.",
  unavailable: "Setting a password is unavailable right now. Try again in a moment.",
  offline: SIGN_IN_WORDS["offline"],
};

/**
 * The console's own rules about a password, said in plain words, keyed on the
 * strings the pinned console sends (FB Console is Appwrite 2.1.0). Its two
 * named rules arrive on the wire as password_recently_used and
 * password_personal_data; every other refusal this route can give about the
 * password (a length outside 8 to 256, the project's own policy, a breached
 * password) arrives as one anonymous 400 typed general_argument_invalid,
 * because a failed validator is not a named refusal and the console does not
 * separate them, so one neutral sentence answers all three.
 */
export const CONSOLE_PASSWORD_WORDS: Readonly<Record<string, string>> = {
  password_recently_used: "Choose a password you have not used here before.",
  password_personal_data: "Do not use your name, email or user ID in the password.",
  general_argument_invalid: "That password will not do. Choose a different one and try again.",
};

/**
 * What the status line says: the console's own rule when it named one, else the
 * refusal's words. An unknown refusal is never silent: it falls back to the
 * "try again in a moment" words.
 */
export function recoveryNotice(code: string, consoleType: string | null = null): string {
  if (consoleType !== null && Object.hasOwn(CONSOLE_PASSWORD_WORDS, consoleType)) {
    return CONSOLE_PASSWORD_WORDS[consoleType] as string;
  }
  return Object.hasOwn(RECOVERY_WORDS, code) ? (RECOVERY_WORDS[code] as string) : (RECOVERY_WORDS["unavailable"] as string);
}

/** The words of the success state. */
export const RECOVERY_DONE_WORDS: Readonly<{ title: string; body: string; cta: string }> = {
  title: "Your password is set",
  body: "Sign in with your email and the new password.",
  cta: "Sign in",
};

/**
 * The rule the page shows under the fields, before anything is typed: the
 * console's own minimum. It is shown, not only enforced, so a person learns
 * the rule while they type rather than after they press.
 */
export const RECOVERY_RULE_WORD = `At least ${PASSWORD_MIN_LENGTH} characters.`;

/** What the page is doing: waiting, saving, saved, or refused. */
export type RecoveryPhase = "idle" | "saving" | "saved" | "refused";

export interface SetPasswordOptions {
  /** The console's recovery route. */
  recovery: PasswordRecoverySurface;
  /** The link out of the address bar, already read. */
  link: RecoveryLink | null;
  /** The page changed and wants to be drawn again. */
  onChange: () => void;
}

/**
 * The set password page's own state, held in one place: the link, the phase and
 * the words. It never throws: the screen wants words, not an exception.
 */
export class SetPasswordPage {
  readonly #recovery: PasswordRecoverySurface;
  readonly #onChange: () => void;
  readonly #link: RecoveryLink | null;
  #phase: RecoveryPhase;
  #notice: string;
  #field: "password" | "confirm" | null = null;

  constructor(options: SetPasswordOptions) {
    this.#recovery = options.recovery;
    this.#onChange = options.onChange;
    this.#link = options.link;
    this.#phase = options.link === null ? "refused" : "idle";
    this.#notice = options.link === null ? (RECOVERY_WORDS["no_link"] as string) : "";
  }

  get phase(): RecoveryPhase {
    return this.#phase;
  }

  /** What the status line says; empty when there is nothing to say. */
  get notice(): string {
    return this.#notice;
  }

  /**
   * Which field the last refusal was about, so the screen can mark the right
   * one the way the sign in screen marks the password. Null when there is
   * nothing to mark.
   */
  get field(): "password" | "confirm" | null {
    return this.#field;
  }

  get busy(): boolean {
    return this.#phase === "saving";
  }

  get saved(): boolean {
    return this.#phase === "saved";
  }

  /** Whether the page has a link at all: without one there is nothing to set. */
  get hasLink(): boolean {
    return this.#link !== null;
  }

  /**
   * Ask the console to set the password. The two entries must match and the
   * password must be long enough before anything is sent: the console is never
   * asked to refuse what the page can see by itself.
   */
  async submit(password: string, confirm: string): Promise<void> {
    // The state is the guard, not the button's next render: a press while the
    // page is saving or after it has saved returns at once, so a second submit
    // can never overwrite a finished or in flight recovery with a later
    // answer.
    if (this.#phase === "saving" || this.#phase === "saved") return;
    if (this.#link === null) {
      this.#refuse("no_link", null, null);
      return;
    }
    if (!confirmMatches(password, confirm)) {
      this.#refuse("mismatch", null, "confirm");
      return;
    }
    if (!passwordAcceptable(password)) {
      this.#refuse("too_short", null, "password");
      return;
    }
    this.#phase = "saving";
    this.#notice = "";
    this.#field = null;
    this.#onChange();
    const answer = await this.#recovery.completeRecovery(this.#link.userId, this.#link.secret, password);
    if (answer.ok) {
      this.#phase = "saved";
      this.#notice = "";
      this.#onChange();
      return;
    }
    this.#refuse(answer.code, answer.consoleType, "password");
  }

  #refuse(code: string, consoleType: string | null = null, field: "password" | "confirm" | null = null): void {
    this.#phase = "refused";
    this.#notice = recoveryNotice(code, consoleType);
    this.#field = field;
    this.#onChange();
  }
}

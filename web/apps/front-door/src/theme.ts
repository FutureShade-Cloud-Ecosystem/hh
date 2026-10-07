// The front door's theme: the Counter Till's Theme control in the front door's terms. Three choices
// in a cycle (Auto, Light, Brand): Auto follows the system's light setting and follows it live, Light
// and Brand pin the choice. Light is the box's default, so the first screen comes up light whatever
// the operating system prefers; Auto is one press away. The mechanism is the token sheet's own
// contract: a data-theme attribute on the document root, where the sheet's brand half is the default
// (no attribute) and the light half is the opt-in data-theme="light".
//
// The choice is CARRIED to the till and the Quote Desk on the same origin: it rests in this tab's
// session storage under THEME_KEY, through the auth package's throw-safe mirror (the same accessor the
// demo handoff rides), and each app reads it at boot in a demo build. The front door writes only what
// the person chose, never a credential; Sign out leaves it alone (a theme is not part of a session).
import { ThrowSafeMirror } from "@hardscapehouse/auth";

export type ThemeChoice = "auto" | "light" | "dark";

export const THEME_ORDER: readonly ThemeChoice[] = ["auto", "light", "dark"];

export const THEME_LABEL: Record<ThemeChoice, string> = {
  auto: "Auto",
  light: "Light",
  dark: "Brand",
};

/** The session storage key the three apps share for the person's choice (JSON: "auto", "light" or "dark"). */
export const THEME_KEY = "hh-demo.theme.v1";

/** The choice that follows this one in the cycle (Auto, Light, Brand, Auto). */
export function themeAfter(choice: ThemeChoice): ThemeChoice {
  const index = THEME_ORDER.indexOf(choice);
  return THEME_ORDER[(index + 1) % THEME_ORDER.length] as ThemeChoice;
}

/** The root's data-theme value for a choice, given whether the system prefers light; null is the brand default. */
export function themeAttribute(choice: ThemeChoice, systemLight: boolean): "light" | null {
  if (choice === "light") return "light";
  if (choice === "dark") return null;
  return systemLight ? "light" : null;
}

/** The control's accessible name: what the press will do, not what is on. */
export function themeControlLabel(choice: ThemeChoice): string {
  return `Theme: ${THEME_LABEL[choice]}. Press for ${THEME_LABEL[themeAfter(choice)]}.`;
}

/** The polite status a press announces. */
export function themeStatusText(choice: ThemeChoice): string {
  return choice === "auto" ? "Theme is now Auto, following the system." : `Theme is now ${THEME_LABEL[choice]}.`;
}

/** A stored value read as a choice; anything that is not one of the three is no choice at all. */
export function parseChoice(raw: unknown): ThemeChoice | null {
  return raw === "auto" || raw === "light" || raw === "dark" ? raw : null;
}

const LIGHT_QUERY = "(prefers-color-scheme: light)";

/** Holds the choice, applies it to the document root, follows the system while Auto, carries it in the tab. */
export class ThemeController {
  readonly #mirror = new ThrowSafeMirror(THEME_KEY);
  #choice: ThemeChoice = parseChoice(this.#mirror.read()) ?? "light";
  #query: MediaQueryList | null = null;
  readonly #onSystemChange = (): void => this.apply();
  readonly #onChange: () => void;

  constructor(onChange: () => void) {
    this.#onChange = onChange;
  }

  get choice(): ThemeChoice {
    return this.#choice;
  }

  start(): void {
    this.#query = typeof window.matchMedia === "function" ? window.matchMedia(LIGHT_QUERY) : null;
    this.#query?.addEventListener("change", this.#onSystemChange);
    this.apply();
  }

  stop(): void {
    this.#query?.removeEventListener("change", this.#onSystemChange);
    this.#query = null;
  }

  cycle(): void {
    this.#choice = themeAfter(this.#choice);
    this.#mirror.write(this.#choice);
    this.apply();
  }

  apply(): void {
    const attr = themeAttribute(this.#choice, this.#query?.matches === true);
    const root = document.documentElement;
    if (attr === null) root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", attr);
    this.#onChange();
  }
}

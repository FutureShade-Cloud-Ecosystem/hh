// Ported from the Counter Till's theme choice (web/apps/counter-till/src/
// theme.ts, itself the reference's account-menu Theme control: app-shell.ts
// toggleTheme, themeAfter), carried inside this app the same way the demo
// banner is: each micro-app holds its own copy and imports nothing from a
// sibling app. Three choices, a cycle rather than a toggle: Auto follows the
// system's light setting (and follows it live), Light and Brand pin the
// choice. The brand sheet (the dark half) is the tokens' own default (the absence of the
// attribute) and the light sheet is the opt-in `data-theme="light"` on the
// document root, the same contract the design system's sheet states.
//
// The choice rests in this tab's memory in a default build. The reference persists it in
// local storage; this app's committed sink gate (test/sinks.test.mjs) refuses
// every storage path (custody is memory per tab), so a reload and a sign-out
// start on Auto in a default build, and Auto is the system's own answer. Said
// here so no one adds a storage call to "finish" the port. In a DEMO build the
// choice is carried between the front door, the till and this desk in the tab's
// session storage (hh-demo.theme.v1), through demo-account.ts, the one module
// that touches storage and that a default build swaps for a stub, so a reload
// keeps it, and the demo's default is Light.

import { readSharedTheme, writeSharedTheme } from "./demo-account.js";

export type ThemeChoice = "auto" | "light" | "dark";

export const THEME_ORDER: readonly ThemeChoice[] = ["auto", "light", "dark"];

export const THEME_LABEL: Record<ThemeChoice, string> = {
  auto: "Auto",
  light: "Light",
  dark: "Brand",
};

/** The choice that follows this one in the cycle (Auto, Light, Brand, Auto). */
export function themeAfter(choice: ThemeChoice): ThemeChoice {
  const index = THEME_ORDER.indexOf(choice);
  return THEME_ORDER[(index + 1) % THEME_ORDER.length];
}

/**
 * The value of the root's `data-theme` attribute for a choice, given whether
 * the system prefers light; null is the sheet's dark default (no attribute).
 */
export function themeAttribute(choice: ThemeChoice, systemLight: boolean): "light" | null {
  if (choice === "light") return "light";
  if (choice === "dark") return null;
  return systemLight ? "light" : null;
}

/** The control's accessible name: what the press will do, not what is on. */
export function themeControlLabel(choice: ThemeChoice): string {
  return `Theme: ${THEME_LABEL[choice]}. Press for ${THEME_LABEL[themeAfter(choice)]}.`;
}

/** The polite status a press announces: the state that took effect. */
export function themeStatusText(choice: ThemeChoice): string {
  return choice === "auto" ? "Theme is now Auto, following the system." : `Theme is now ${THEME_LABEL[choice]}.`;
}

const LIGHT_QUERY = "(prefers-color-scheme: light)";

/** Holds the choice, applies it to the document root, and follows the system while Auto. */
export class ThemeController {
  // A demo build opens on what the tab carries (the front door and the till write it), else on Light; a default build
  // opens on Auto and carries nothing (the reader resolves the compiled out stub there).
  #choice: ThemeChoice = readSharedTheme() ?? (import.meta.env.MODE === "demo" ? "light" : "auto");
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
    writeSharedTheme(this.#choice);
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

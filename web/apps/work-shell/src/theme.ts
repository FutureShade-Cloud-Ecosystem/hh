// The host follows the theme the person chose in the front door or in a hosted
// app: the choice rests in this tab's session storage under one key the whole
// demo shares (web/apps/front-door/src/theme.ts), read through the auth
// package's throw-safe mirror. Light is the default; Auto follows the system.
// The host only reads: the controls live in the pages it hosts.
import { ThrowSafeMirror } from "@hardscapehouse/auth";

export const THEME_KEY = "hh-demo.theme.v1";

export type ThemeChoice = "auto" | "light" | "dark";

/** A stored value read as a choice; anything that is not one of the three is no choice at all. */
export function parseChoice(raw: unknown): ThemeChoice | null {
  return raw === "auto" || raw === "light" || raw === "dark" ? raw : null;
}

/** The root's data-theme value for a choice, given whether the system prefers light; null is the brand default. */
export function themeAttribute(choice: ThemeChoice, systemLight: boolean): "light" | null {
  if (choice === "light") return "light";
  if (choice === "dark") return null;
  return systemLight ? "light" : null;
}

/** Apply the shared choice to the document root. */
export function applyTheme(root: Element, systemLight: boolean): void {
  const choice = parseChoice(new ThrowSafeMirror(THEME_KEY).read()) ?? "light";
  const attr = themeAttribute(choice, systemLight);
  if (attr === null) root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", attr);
}

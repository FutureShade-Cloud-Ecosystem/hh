// The front door's theme: light is the box's default, and the mechanism is
// the token sheet's own contract, the same one the Counter Till's theme.ts
// uses: a data-theme attribute on the document root, where the sheet's dark
// half is the default (no attribute) and the light half is the opt-in
// data-theme="light". Dark stays reachable through the same attribute, which
// makes it the second opt-in; the header carries no theme control yet, the
// D3 polish walk decides where one goes.
//
// The choice is applied once at boot and never stored: the front door always
// comes up light, the way the box's first screen should.

export type ThemeChoice = "light" | "dark";

/** The value of the root's data-theme attribute for a choice; null is the sheet's dark default. */
export function themeAttribute(choice: ThemeChoice): "light" | null {
  return choice === "light" ? "light" : null;
}

/** Apply the choice to the document root, the sheet's own contract. */
export function applyTheme(choice: ThemeChoice = "light"): void {
  const attr = themeAttribute(choice);
  const root = document.documentElement;
  if (attr === null) root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", attr);
}

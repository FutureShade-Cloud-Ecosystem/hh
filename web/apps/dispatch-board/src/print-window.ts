// The manifest's print window. The document is the core's own render; nothing here composes markup.
// It rides a same-origin blob address into a window the click opened, and the print dialog is that
// window's own, called once it has loaded (the same carriage the counter's receipt uses, kept here
// so the board does not carry the whole point of sale bundle for twenty lines). The blob holds no
// credential: the fetch already presented the bearer, and the address names bytes, not a route.

/** The slice of a browser window this flow touches, so it is testable without one. */
export interface PrintWindow {
  readonly location: { assign(url: string): void };
  addEventListener(type: "load", listener: () => void): void;
  focus?(): void;
  print?(): void;
  close?(): void;
}

export interface PrintDeps {
  toUrl(html: string): string;
  revoke(url: string): void;
  later(run: () => void, ms: number): void;
}

/** What a blocked window says. The flow stops here, before it asks the core for anything. */
export const BLOCKED_WINDOW_WORDS = "The browser blocked the manifest window. Allow pop-ups for this board, then print again.";

export type PrintOutcome = { readonly ok: true } | { readonly ok: false; readonly message: string };

/** The browser's own parts. */
export const browserPrintDeps: PrintDeps = {
  toUrl: (html) => URL.createObjectURL(new Blob([html], { type: "text/html;charset=utf-8" })),
  revoke: (url) => URL.revokeObjectURL(url),
  later: (run, ms) => void window.setTimeout(run, ms),
};

/** Opens the blank window the click is allowed to open. Null when the browser blocked it. */
export function openPrintWindow(): PrintWindow | null {
  return window.open("", "_blank") as unknown as PrintWindow | null;
}

/** Carries the document into the window and prints it once the window has it. A blocked window is a sentence. */
export function printHtmlInto(win: PrintWindow | null, html: string, deps: PrintDeps): PrintOutcome {
  if (win === null) {
    return { ok: false, message: BLOCKED_WINDOW_WORDS };
  }
  try {
    const url = deps.toUrl(html);
    win.addEventListener("load", () => {
      win.focus?.();
      win.print?.();
      // The document is in the window's own history from here; the address's bytes are spent.
      deps.later(() => deps.revoke(url), 60_000);
    });
    win.location.assign(url);
    return { ok: true };
  } catch {
    win.close?.();
    return { ok: false, message: "The manifest window could not be opened. Try again, or print it from the run's page." };
  }
}

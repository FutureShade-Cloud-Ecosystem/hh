// Which side the Items pane is on in the till's two-pane view: one boolean, kept per browser so the
// choice survives a reload. This file holds that flag and nothing else (the sink gate allows the
// storage word here and in no other file), and a blocked or full store never stops the till
// rendering: every access is wrapped and falls back to Items on the left.

const KEY = "hh-till-panes-swapped";

/** True when the person swapped the panes; false when unset, unreadable, or never swapped. */
export function readPanesSwapped(): boolean {
  try {
    return globalThis.localStorage?.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

/** Remember the choice; a blocked store is ignored. */
export function writePanesSwapped(swapped: boolean): void {
  try {
    globalThis.localStorage?.setItem(KEY, swapped ? "1" : "0");
  } catch {
    /* blocked or full: the panes still swap for this page view */
  }
}

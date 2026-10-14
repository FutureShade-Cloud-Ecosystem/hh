// The framed flag: how a desk app knows the Work shell host is framing it, so it
// shows no header band of its own (row 1520: one header band, the shell's).
//
// The host adds one query parameter to the address of each desk app frame it
// opens (FRAMED_PARAM=FRAMED_BY_WORK_SHELL). An app reads it ONCE, at its first
// render, through framedByShell(), and the answer never changes for the life of
// the page. The flag is not a guess from window.parent: it must be present
// and exactly the one value, and it is honoured only when the page really is
// inside a frame whose parent is at the page's own origin. A page opened at the
// top level, or framed by another origin, ignores the parameter and keeps its
// own band, so a link built by an outsider enables nothing. The flag only
// hides chrome: it grants no access, no role and no data.
//
// The host and a framed app also share one message, the chrome action: the
// shell's account menu carries Theme and Feedback for the app it frames, and
// asks the frame to act on them with one message scoped to the page's own
// origin, which the app accepts only from its own parent window.

/** The query parameter the host adds to a desk app frame's address. */
export const FRAMED_PARAM = 'hh-framed';

/** The one value that means "framed by the Work shell". */
export const FRAMED_BY_WORK_SHELL = 'work-shell';

/** The message type the host sends a framed app to act on one of the chrome controls it carries. */
export const CHROME_ACTION_MESSAGE = 'hh-work-shell:chrome-action';

/** The message type a framed app sends its parent: the list of the shell's controls it can act on. */
export const CHROME_CONTROLS_MESSAGE = 'hh-work-shell:chrome-controls';

/** The controls the shell carries for a framed app, by the action names the apps already use. */
export type ChromeAction = 'theme' | 'feedback';

const CHROME_ACTIONS: readonly string[] = ['theme', 'feedback'];

/** Whether a query string carries the flag: the parameter, exactly once, with exactly the one value. */
export function framedFlagFromSearch(search: string): boolean {
  let params: URLSearchParams;
  try {
    params = new URLSearchParams(search);
  } catch {
    return false;
  }
  const values = params.getAll(FRAMED_PARAM);
  return values.length === 1 && values[0] === FRAMED_BY_WORK_SHELL;
}

/** The slice of a window the framed decision reads. */
export interface FramedWindow {
  readonly location: { readonly search: string; readonly origin: string };
  readonly parent: unknown;
}

/**
 * The framed decision for one window: the flag is present and valid, the page
 * sits inside another window, and that parent is at this page's own origin
 * (reading a cross origin parent's location throws, which reads as not framed).
 */
export function readFramedByShell(win: FramedWindow): boolean {
  if (!framedFlagFromSearch(win.location.search)) return false;
  if (win.parent === (win as unknown)) return false;
  try {
    const parent = win.parent as { location: { origin: string } };
    return parent.location.origin === win.location.origin;
  } catch {
    return false;
  }
}

let framedAtBoot: boolean | null = null;

/** The decision for this page, read once on first call and kept (false where there is no window). */
export function framedByShell(): boolean {
  if (framedAtBoot !== null) return framedAtBoot;
  if (typeof window === 'undefined' || typeof window.location?.search !== 'string') return false;
  try {
    framedAtBoot = readFramedByShell(window);
  } catch {
    framedAtBoot = false;
  }
  return framedAtBoot;
}

/**
 * The control a message asks a framed app to press, or null: only the exact
 * message type, from this window's own parent, at this page's own origin, and
 * only for a control the shell carries.
 */
export function chromeActionFrom(
  event: { readonly origin: string; readonly source: unknown; readonly data: unknown },
  win: { readonly parent: unknown; readonly location: { readonly origin: string } },
): ChromeAction | null {
  if (event.source === null || event.source !== win.parent) return null;
  if (event.origin !== win.location.origin) return null;
  const data = event.data;
  if (data === null || typeof data !== 'object') return null;
  const { type, action } = data as { type?: unknown; action?: unknown };
  if (type !== CHROME_ACTION_MESSAGE) return null;
  return typeof action === 'string' && CHROME_ACTIONS.includes(action) ? (action as ChromeAction) : null;
}

/** A framed app tells its parent which of the shell's controls it can act on, scoped to its own origin. */
export function announceChromeControls(
  win: {
    readonly parent: { postMessage(message: unknown, targetOrigin: string): void };
    readonly location: { readonly origin: string };
  },
  actions: readonly ChromeAction[],
): void {
  win.parent.postMessage({ type: CHROME_CONTROLS_MESSAGE, actions: [...actions] }, win.location.origin);
}

/**
 * The controls a frame's list names, or null: the exact type, from the host's
 * own content frame, at the host's own origin. Names the shell does not carry,
 * and repeats, are dropped.
 */
export function acceptChromeControls(
  event: { readonly origin: string; readonly source: unknown; readonly data: unknown },
  host: { readonly origin: string; readonly contentWindow: unknown },
): ChromeAction[] | null {
  if (host.contentWindow === null || host.contentWindow === undefined) return null;
  if (event.origin !== host.origin || event.source !== host.contentWindow) return null;
  const data = event.data;
  if (data === null || typeof data !== 'object') return null;
  const { type, actions } = data as { type?: unknown; actions?: unknown };
  if (type !== CHROME_CONTROLS_MESSAGE || !Array.isArray(actions)) return null;
  const kept: ChromeAction[] = [];
  for (const name of actions) {
    if (typeof name === 'string' && CHROME_ACTIONS.includes(name) && !kept.includes(name as ChromeAction)) kept.push(name as ChromeAction);
  }
  return kept;
}

/** The address a host gives a desk app frame: the app's path with the flag added. */
export function framedAddress(path: string): string {
  const hash = path.indexOf('#');
  const head = hash === -1 ? path : path.slice(0, hash);
  const tail = hash === -1 ? '' : path.slice(hash);
  const joiner = head.includes('?') ? '&' : '?';
  return `${head}${joiner}${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}${tail}`;
}

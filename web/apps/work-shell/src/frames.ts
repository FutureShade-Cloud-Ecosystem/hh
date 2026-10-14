// The frame guard. A desk app hosted in the content frame ends its own walk
// (Switch role, Sign out, no session) with window.location.assign("/"). At the
// shell's origin root that is the host itself, loaded INSIDE the frame. A host
// that finds itself framed mounts nothing: it asks its parent to switch role
// with one message, scoped to its own origin, and the parent accepts it only
// from its own content frame.

/** The one message type a framed host sends its parent. */
export const SWITCH_ROLE_MESSAGE = "hh-work-shell:switch-role";

interface WindowLike {
  parent: unknown;
}

/** True when this window sits inside another window. */
export function isFramed(win: WindowLike): boolean {
  return win.parent !== win;
}

/** Ask the parent host to switch role. The target origin is this window's own. */
export function announceSwitchRole(win: {
  parent: { postMessage(message: unknown, targetOrigin: string): void };
  location: { origin: string };
}): void {
  win.parent.postMessage({ type: SWITCH_ROLE_MESSAGE }, win.location.origin);
}

/**
 * Whether a message event is the content frame's Switch role request: the
 * exact type, from the frame's own window, at the host's own origin.
 */
export function acceptSwitchRole(
  event: { origin: string; source: unknown; data: unknown },
  host: { origin: string; contentWindow: unknown },
): boolean {
  if (host.contentWindow === null || host.contentWindow === undefined) return false;
  if (event.origin !== host.origin) return false;
  if (event.source !== host.contentWindow) return false;
  const data = event.data;
  return data !== null && typeof data === "object" && (data as { type?: unknown }).type === SWITCH_ROLE_MESSAGE;
}

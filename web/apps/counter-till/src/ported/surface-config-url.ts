// Ported from hardscapeos_dibbits master 87d7941b app/src/surface-config-url.ts; adapted for HH.

// surface-config-url.ts: the ONE place the app builds a settings-kit config URL.
//
// WHY THIS FILE EXISTS. There were nine hand-rolled copies of
// `/api/v1/surfaces/${id}/config?branch_id=…` across the app, and they had already drifted: one of
// them (ar-invoice-settings.ts) sent `?profile=`: correctly, because it is an EDITOR and needs the
// rows for the shape it is editing: and the server ignored the parameter entirely. So a screen was
// asking for the right thing, being handed the caller's own rows instead, and nobody could tell.
//
// The kit's read now honours `?profile=` (backend/internal/settings/service.go's ForProfile). This
// builder is what stops the next copy from forgetting it again.

/**
 * surfaceConfigUrl builds the config read for one surface.
 *
 * `profile` is for EDITORS ONLY, and naming it costs authority: the server admin-gates a request for
 * a profile other than the caller's, because reading another job shape's curation is the read half of
 * the same admin act as writing it. An ordinary screen showing its own user's curation passes no
 * profile and is not gated.
 */
export function surfaceConfigUrl(
  surfaceId: string,
  opts: { branchId?: string; profile?: string } = {},
): string {
  const q = new URLSearchParams();
  if (opts.branchId) q.set('branch_id', opts.branchId);
  if (opts.profile) q.set('profile', opts.profile);
  const s = q.toString();
  return `/api/v1/surfaces/${encodeURIComponent(surfaceId)}/config${s ? `?${s}` : ''}`;
}

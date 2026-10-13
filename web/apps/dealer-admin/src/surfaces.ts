// Dealer Admin's surface registry: the one place the app's own surfaces are
// named. The app shell's left list reads this, and the router line in
// app-root picks the view by the id.
//
// THE EXTENSION POINT. The next coder adds Pricing, Role configuration and
// Deployment settings by landing a views file and adding one row here; no
// other file in this app changes. A surface is an id, the words the list
// shows, and the view function the app-root line calls for it.
//
// Today: Staff (the original's staff-roles.ts and staff-reset.ts), Hotkeys
// (the original's platform-hotkeys.ts), Audit (the original's audit-log.ts),
// Role configuration (the settings kit's surface curation, no original
// screen), and Deployment settings (the brand, the card rail, the counter's
// policy, the walkthrough), each with its PROVENANCE rows.
export interface SurfaceSpec {
  /** The id the list button, the router line and the tests name. */
  readonly id: string;
  /** The list's word for the surface. */
  readonly title: string;
  /** One line under the title, what the surface decides. */
  readonly blurb: string;
}

export const SURFACES: readonly SurfaceSpec[] = [
  { id: "staff", title: "Staff", blurb: "The register, the roles, the branch grants, the reset links." },
  { id: "hotkeys", title: "Hotkeys", blurb: "The keyboard rosters every dealer inherits, until they set their own." },
  { id: "audit", title: "Audit", blurb: "Who changed what, when, where and why." },
  { id: "role-config", title: "Role configuration", blurb: "What each job shape sees on every surface, curated per role." },
  { id: "deployment", title: "Deployment settings", blurb: "The brand, the card rail, the counter's policy, the walkthrough." },
];

export function isSurfaceId(value: string): value is SurfaceSpec["id"] {
  return SURFACES.some((s) => s.id === value);
}

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
// Assist (the original's assist-admin.ts), Print jobs (the core's print
// queue), Exports (the core's two export files), and Catalog (the original's
// product-edit.ts over the core's product writes), each with its PROVENANCE
// rows.
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
  { id: "assist", title: "Assist", blurb: "The assistant's month: who uses it, what it costs, what they may spend." },
  { id: "printing", title: "Print jobs", blurb: "One document's print history: how often it printed, and by whom." },
  { id: "exports", title: "Exports", blurb: "Two files, one call each: the audit workbook and the house's messages." },
  { id: "catalog", title: "Catalog", blurb: "A product's words and its picture: find one, describe it, set its picture." },
];

export function isSurfaceId(value: string): value is SurfaceSpec["id"] {
  return SURFACES.some((s) => s.id === value);
}

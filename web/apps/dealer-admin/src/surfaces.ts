// Dealer Admin's surface registry: the one place the app's own surfaces are
// named. The app shell's left list reads this, and the router line in
// app-root picks the view by the id.
//
// The groups follow the BOARDS FINAL layout:
// PEOPLE: Staff, Roles, Sign in methods, Chat rules
// BUSINESS: Pricing, Catalog, Delivery, Warranty
// SYSTEM: Printing, Exports, Audit, Assistant, Keys, Deployment
export interface SurfaceSpec {
  /** The id the list button, the router line and the tests name. */
  readonly id: string;
  /** The list's word for the surface. */
  readonly title: string;
  /** One line under the title, what the surface decides. */
  readonly blurb: string;
}

export interface SurfaceGroup {
  readonly name: string;
  readonly surfaces: readonly SurfaceSpec[];
}

export const SURFACE_GROUPS: readonly SurfaceGroup[] = [
  {
    name: "PEOPLE",
    surfaces: [
      { id: "staff", title: "Staff", blurb: "The register, the roles, the branch grants, the reset links." },
      { id: "role-config", title: "Roles", blurb: "What each job shape sees on every surface, curated per role." },
      { id: "sign-in-methods", title: "Sign in methods", blurb: "Google, Microsoft, email and password, and magic link, per dealer." },
      { id: "chat-rules", title: "Chat rules", blurb: "House Messages channel membership by role and employment status." },
    ],
  },
  {
    name: "BUSINESS",
    surfaces: [
      { id: "pricing", title: "Pricing", blurb: "The margin floors, the rules, the ladders, the imports and the ledgers." },
      { id: "catalog", title: "Catalog", blurb: "A product's words and its picture: find one, describe it, set its picture." },
      { id: "delivery", title: "Delivery", blurb: "The trucks, the windows and the zones this dealer delivers with." },
      { id: "warranty", title: "Warranty", blurb: "When a defect becomes a warranty claim, and how each vendor credits it." },
    ],
  },
  {
    name: "SYSTEM",
    surfaces: [
      { id: "printing", title: "Printing", blurb: "One document's print history: how often it printed, and by whom." },
      { id: "exports", title: "Exports", blurb: "Two files, one call each: the audit workbook and the house's messages." },
      { id: "audit", title: "Audit", blurb: "Who changed what, when, where and why." },
      { id: "assist", title: "Assistant", blurb: "The assistant's month: who uses it, what it costs, what they may spend." },
      { id: "keys", title: "Keys", blurb: "The platform's own keys and this dealer's overrides, read only." },
      { id: "deployment", title: "Deployment", blurb: "The brand, the card rail, the counter's policy, the walkthrough." },
      { id: "hotkeys", title: "Hotkeys", blurb: "The keyboard rosters every dealer inherits, until they set their own." },
    ],
  },
];

export const SURFACES: readonly SurfaceSpec[] = SURFACE_GROUPS.flatMap((g) => g.surfaces);

export function isSurfaceId(value: string): value is SurfaceSpec["id"] {
  return SURFACES.some((s) => s.id === value) || value === "roles" || value === "sign-in";
}

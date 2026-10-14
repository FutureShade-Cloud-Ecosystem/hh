// Which live apps a manager profile may open from its landing. The cells are the
// access matrix's (core/internal/roleconfig/default.json), every surface whose
// level is not none for the role the profile belongs to. A link to an app the
// matrix gives the role none on would open a wall, so such a role keeps the
// coming words instead. Words and links only: the app and the core still gate
// every read. A profile this table does not know is not narrowed (null), the way
// role-words.ts keeps the whole strip for a legacy or future profile.

const BY_PROFILE: Readonly<Record<string, readonly string[]>> = Object.freeze({
  "manager.yard": Object.freeze(["Money Desk", "Buying and Stock", "Yard Pick"]),
  "manager.logistics": Object.freeze([]),
  "manager.branch": Object.freeze(["Money Desk", "Buying and Stock", "Counter Till"]),
  "manager.sales": Object.freeze(["Money Desk", "Buying and Stock", "Counter Till"]),
  "manager.procurement": Object.freeze(["Money Desk", "Buying and Stock"]),
  "manager.payables": Object.freeze(["Money Desk", "Buying and Stock"]),
  "manager.controller": Object.freeze(["Money Desk", "Buying and Stock"]),
  "manager.general": Object.freeze(["Money Desk", "Buying and Stock", "Counter Till"]),
});

/** The live apps (by front door tile name) the profile's role may open, or null for a profile not in the table. */
export function appsFor(profile: string): readonly string[] | null {
  return Object.hasOwn(BY_PROFILE, profile) ? BY_PROFILE[profile] : null;
}

/** True when the profile may open the app (an unknown profile may open any live one). */
export function mayOpen(profile: string | undefined, app: string): boolean {
  const apps = profile === undefined ? null : appsFor(profile);
  return apps === null || apps.includes(app);
}

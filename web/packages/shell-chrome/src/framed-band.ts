// The framed band's words (GAPS-r2 row G1): the pure composition behind the
// white band a desk app renders above its app-local header when it stands
// alone. The band's element is hh-shell-header in its band variant; these
// are the strings each app hands it: the centred selector's "App > Surface"
// and the account chip's persona name with the role label (the tier words
// the person knows, read only; the role's RBAC never changes).

/** The demo tenant's word for the band's lockup. The demo apps frame the
 * demo tenant; the white-label tenant config leads when it lands. */
export const FRAMED_BAND_ORG = 'Gable Landscape Supply';

/** The centred selector's words: the app, then the surface the demo provides
 * ("Counter Till > New sale"). No surface is the app's front door, named by
 * the app alone. */
export function framedBandBreadcrumb(appName: string, surfaceLabel: string): string {
  const surface = surfaceLabel.trim();
  return surface === '' ? appName : `${appName} > ${surface}`;
}

/** The longest role the chip shows whole; a longer one is cut to its first 20 characters and an ellipsis. */
const ROLE_CAP = 24;

/** The account chip's words: the persona's display name with the role
 * label ("Dana, General Manager"). The demo personas' " (demo)" suffix is not
 * shown. One side missing renders the other; the two equal renders once,
 * never a doubled word; a long role is capped. */
export function framedBandAccount(userName: string, roleLabel: string): string {
  const name = userName.replace(/\s*\(demo\)\s*$/i, '').trim();
  const trimmed = roleLabel.replace(/\s*\(demo\)\s*$/i, '').trim();
  const role = trimmed.length > ROLE_CAP ? `${trimmed.slice(0, 20)}\u2026` : trimmed;
  if (name === '') return role;
  if (role === '' || trimmed === name) return name;
  return `${name}, ${role}`;
}

/** The theme a framed app applies to the document root itself: the standalone
 * band carries it, but framed by the Work shell the app renders no band, and
 * the shell's account menu only asks the app to press its Theme control.
 * Light sets data-theme, dark removes it, the sheet's own contract. */
export function applyDocumentTheme(theme: string): void {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  if (theme === 'light') root.setAttribute('data-theme', 'light');
  else if (theme === 'dark') root.removeAttribute('data-theme');
}

// The package's one address sink: the only source the position gate
// (test/positions.test.mjs) permits for a binding inside an address attribute
// (href, src, action, formaction, xlink:href, poster, cite). A value bound in
// an address attribute is a NAVIGATION target, not text, so it must come
// through here, whole, and this helper holds it to the scheme rule at run
// time. Nothing in the package binds an address today; the helper exists so
// the first caller cannot be the incident.

/** An address is safe when it carries no scheme other than http or https, or
 * no scheme at all (relative, rooted, fragment, protocol relative). The probe
 * folds the characters a URL parser ignores, so a line in the middle of the
 * scheme cannot smuggle it past. Returns the value unchanged when it passes;
 * throws when it does not. */
export function hhAddress(value: string): string {
  const probe = value.replace(/[\t\n\r]/g, '').replace(/^[\x00-\x20]+/, '').replace(/[\x00-\x20]+$/, '');
  const m = probe.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):/);
  if (m === null || /^https?:$/i.test(m[0])) return value;
  throw new Error(
    `hhAddress: refusing ${JSON.stringify(value)} as an address: ` +
      `only http, https and scheme-free (relative) addresses render`,
  );
}

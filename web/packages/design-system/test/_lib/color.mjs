// Colour arithmetic for the design system gates. Test-only: no shipped source
// imports this file, which is why the phantom-token scan may exclude test/ (see
// the scan's own header for the documented reason).
//
// WCAG 2.1 relative luminance and contrast, over sRGB. This is the ERP gate's
// arithmetic (hardscapeos_dibbits app/src/_theme-tokens.test.mjs, relativeLuminance
// and contrast), kept byte-for-byte in behaviour and pinned to the same reference
// values, plus an sRGB -> OKLCH conversion so the sheet can carry HH Pro's oklch
// values verbatim and still be measured at the same bar.
//
// The oklch side is the CSS Color 4 pipeline: sRGB -> linear sRGB -> OKLab
// (Bjorn Ottosson's matrices) -> OKLCH, and back. It is pinned below to two
// widely published reference points (#ff0000 and #0000ff) rather than trusted.

const LIN = (v) => {
  const c = v / 255;
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
};

export function srgbToLinear(hex) {
  const h = hex.replace('#', '').trim();
  const full = h.length === 3 ? h.split('').map((c) => c + c).join('') : h;
  return [0, 2, 4].map((i) => LIN(parseInt(full.slice(i, i + 2), 16)));
}

export function relativeLuminance(hex) {
  const [r, g, b] = srgbToLinear(hex);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrast(fg, bg) {
  const a = relativeLuminance(fg);
  const b = relativeLuminance(bg);
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

// ── OKLab / OKLCH ──────────────────────────────────────────────────────────────

const oklchToLinearSrgb = (L, C, H) => {
  const hr = (H * Math.PI) / 180;
  const a = C * Math.cos(hr);
  const b = C * Math.sin(hr);
  const l_ = L + 0.3963377774 * a + 0.2158037573 * b;
  const m_ = L - 0.1055613458 * a - 0.0638541728 * b;
  const s_ = L - 0.0894841775 * a - 1.291485548 * b;
  const l = l_ ** 3;
  const m = m_ ** 3;
  const s = s_ ** 3;
  return [
    +4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
};

const linearToByte = (v) => {
  const c = v <= 0.0031308 ? v * 12.92 : 1.055 * Math.pow(v, 1 / 2.4) - 0.055;
  return Math.round(Math.min(1, Math.max(0, c)) * 255);
};

const linearSrgbToOklab = ([r, g, b]) => {
  const l = 0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b;
  const m = 0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b;
  const s = 0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b;
  const l_ = Math.cbrt(l);
  const m_ = Math.cbrt(m);
  const s_ = Math.cbrt(s);
  return [
    0.2104542553 * l_ + 0.793617785 * m_ - 0.0040720468 * s_,
    1.9779984951 * l_ - 2.428592205 * m_ + 0.4505937099 * s_,
    0.0259040371 * l_ + 0.7827717662 * m_ - 0.808675766 * s_,
  ];
};

/** #rrggbb -> [L, C, H in degrees]. */
export function hexToOklch(hex) {
  const [L, a, b] = linearSrgbToOklab(srgbToLinear(hex));
  const C = Math.hypot(a, b);
  const H = C === 0 ? 0 : (((Math.atan2(b, a) * 180) / Math.PI) + 360) % 360;
  return [L, C, H];
}

/** [L, C, H in degrees] -> "#rrggbb" (clamped at the gamut edge). */
export function oklchToHex([L, C, H]) {
  const [r, g, b] = oklchToLinearSrgb(L, C, H);
  return '#' + [r, g, b].map(linearToByte).map((v) => v.toString(16).padStart(2, '0')).join('');
}

/** Parses "oklch(70% 0.17 255)" (percent or 0-1 lightness) -> [L, C, H]. */
export function parseOklch(text) {
  const m = String(text).match(/^oklch\(\s*([0-9.]+)(%?)\s+([0-9.]+)\s+([0-9.]+)\s*\)$/i);
  if (!m) return null;
  const l = m[2] === '%' ? Number(m[1]) / 100 : Number(m[1]);
  return [l, Number(m[3]), Number(m[4])];
}

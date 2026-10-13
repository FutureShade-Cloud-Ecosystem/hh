// What the gauge shape draws, with no DOM in sight. The server picks the shape
// and measures the points; this only turns the points it sent into the classes
// of a meter. Nothing here is invented: a gauge with no points or no positive
// sum draws no meter, and the exact figures always ride as text beside whatever
// is drawn (the quantised classes are the glance, not the number). A trend stays
// its figure: the core's trend points are one per branch, not a time series.
//
// Port provenance: the original's home bento drew gauges (and trend sparklines, not ported)
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md). The bento's
// bound style attributes become the sheet's static, five percent classes, the
// way the bars already do, because the position gate refuses a bound style.
import type { WidgetPoint } from "./manager-api.js";

function quantized(share: number): number {
  return Math.max(0, Math.min(100, Math.round(share * 20) * 5));
}

export interface GaugeSegment {
  readonly label: string;
  readonly number: number;
  readonly widthClass: string;
  readonly toneClass: string;
}

const TONES: ReadonlySet<string> = new Set(["good", "watch", "stop"]);

/** Each point's share of the points' own sum, with the tone the server named; null when there is no meter to draw. */
export function gaugeSegments(points: readonly WidgetPoint[]): readonly GaugeSegment[] | null {
  if (points.length === 0 || points.some((p) => p.number < 0)) return null;
  const sum = points.reduce((total, p) => total + p.number, 0);
  if (sum <= 0) return null;
  return points.map((p) => {
    const width = p.number > 0 ? Math.max(5, quantized(p.number / sum)) : 0;
    return {
      label: p.label,
      number: p.number,
      widthClass: `w${width}`,
      toneClass: TONES.has(p.tone) ? `tone-${p.tone}` : "tone-none",
    };
  });
}

// What the trend and gauge shapes draw, with no DOM in sight. The server picks
// the shape and measures the points; this only turns the points it sent into
// the classes of a line and a meter. Nothing here is invented: a trend with
// fewer than two honest points draws no line, a gauge with no points or no
// positive sum draws no meter, and the exact figures always ride as text beside
// whatever is drawn (the quantised classes are the glance, not the number).
//
// Port provenance: the original's home bento drew trend sparklines and gauges
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md). The bento's
// bound style attributes become the sheet's static, five percent classes, the
// way the bars already do, because the position gate refuses a bound style.
import { figureFor } from "./manager-logic.js";
import type { WidgetPoint } from "./manager-api.js";

export interface SparkColumn {
  readonly label: string;
  readonly number: number;
  readonly heightClass: string;
}

function quantized(share: number): number {
  return Math.max(0, Math.min(100, Math.round(share * 20) * 5));
}

/** One column per point, the tallest at full height; null when there is no line to draw. */
export function sparkColumns(points: readonly WidgetPoint[]): readonly SparkColumn[] | null {
  if (points.length < 2 || points.some((p) => p.number < 0)) return null;
  const max = points.reduce((high, p) => (p.number > high ? p.number : high), 0);
  if (max <= 0) return null;
  return points.map((p) => {
    // A positive point never rounds away to nothing: it keeps the smallest visible stub.
    const height = p.number > 0 ? Math.max(5, quantized(p.number / max)) : 0;
    return { label: p.label, number: p.number, heightClass: `h${height}` };
  });
}

/** The trend's spoken label: its first and last point, formatted the way the card formats its figure. */
export function sparkLabel(points: readonly WidgetPoint[], kind: string): string {
  if (points.length === 0) return "";
  const word = (p: WidgetPoint): string =>
    `${p.label}, ${figureFor({ key: "", state: "ok", total: p.number, points: [], note: "", range: "" }, kind) ?? ""}`;
  return `Trend from ${word(points[0])}, to ${word(points[points.length - 1])}`;
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

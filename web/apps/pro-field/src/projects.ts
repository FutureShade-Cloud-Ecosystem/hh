// The projects words. A project here is a job rollup: the dealer's own sums
// for what was spent on a job, what was quoted, and the variance between them,
// all of it computed by the core and only ever worded here.
import { priceWords } from "./figures.js";

export interface VarianceWords {
  words: string;
  tone: "neutral" | "good" | "stop";
}

/** The quoted versus actual line, from the core's own variance; nothing here sums. */
export function varianceWords(hasQuote: boolean, varianceCents: number): VarianceWords {
  if (!hasQuote) return { words: "No quote to measure against", tone: "neutral" };
  if (!Number.isFinite(varianceCents)) return { words: "Not set", tone: "neutral" };
  if (varianceCents > 0) return { words: `${priceWords(varianceCents)} over the quote`, tone: "stop" };
  if (varianceCents < 0) return { words: `${priceWords(-varianceCents)} under the quote`, tone: "good" };
  return { words: "On the quote", tone: "neutral" };
}

export const projectsEmptyWords = "No projects yet. They appear here once the dealer tags an order to a job.";

// The Weights pending row of the yard shaped homes. The first slice drew it as a
// grey "no source" row because the needs queue had no source for it. The core
// measures it now (the weight.pending tile), so a count joins the queue as a real
// row; a tile that cannot measure keeps an honest words row; a profile with no
// weights work never shows the row at all. The row opens Yard Pick, where the
// weight is entered (landing-links.ts).
import type { NeedsQueue, NeedsRow } from "./manager-api.js";
import { UNSOURCED_NEEDS } from "./manager-logic.js";
import type { WeightsPending } from "./weights-api.js";

const WEIGHTS_PROFILES: ReadonlySet<string> = new Set(["manager.yard", "manager.logistics"]);

export function hasWeightsWork(profile: string): boolean {
  return WEIGHTS_PROFILES.has(profile);
}

/** The queue with the weights row joined, or the same queue when there is nothing honest to add. */
export function mergeWeights(needs: NeedsQueue, weights: WeightsPending | null, profile: string): NeedsQueue {
  if (!hasWeightsWork(profile) || weights === null || weights.state !== "ok" || weights.count === null || weights.count <= 0) {
    return needs;
  }
  const row: NeedsRow = {
    source_key: "weights.pending",
    what: "Weights pending",
    count: weights.count,
    age: "",
    severity: "watch",
    module: "tasks",
    unit: "yard",
    screen: "yard",
    view: "Weights outstanding",
    action: "Weigh",
  };
  return {
    ...needs,
    rows: [...needs.rows, row],
    total: needs.total + weights.count,
    worst: needs.worst === "stop" ? "stop" : "watch",
  };
}

/**
 * The words rows for a measure that could not answer. A tile switched off the home (hidden, absent)
 * is the person's own choice and says nothing; one the core could not measure, or a home that did
 * not load, is said plainly rather than shown as a zero.
 */
export function unsourcedFor(profile: string, weights: WeightsPending | null): typeof UNSOURCED_NEEDS {
  if (!hasWeightsWork(profile)) return [];
  if (weights === null || weights.state === "unwired" || weights.state === "error") return UNSOURCED_NEEDS;
  return [];
}

// The Weights pending count, read from the caller's own home. The core measures
// it as the weight.pending tile (tasks asked to weigh something the yard has
// not returned), already served on GET /api/v1/dashboard and already in the
// generated client; no new route. The projection copies the one tile it names
// and nothing else, and a number that is not a number is not a count.
import { ApiClient, staffDashboard, type Result } from "@hardscapehouse/api-client";

export const WEIGHT_TILE = "weight.pending";

export interface WeightsPending {
  /** The tile's own state, or "absent" when the home carries no such tile. */
  readonly state: "ok" | "unwired" | "error" | "hidden" | "absent";
  readonly count: number | null;
}

const ABSENT: WeightsPending = { state: "absent", count: null };

export function toWeightsPending(raw: unknown): WeightsPending {
  if (typeof raw !== "object" || raw === null) return ABSENT;
  const tiles = (raw as { tiles?: unknown }).tiles;
  if (!Array.isArray(tiles)) return ABSENT;
  for (const tile of tiles) {
    if (typeof tile !== "object" || tile === null) continue;
    const t = tile as Record<string, unknown>;
    if (t.tile_id !== WEIGHT_TILE) continue;
    const state = t.state === "ok" || t.state === "unwired" || t.state === "error" || t.state === "hidden" ? t.state : "error";
    const count = state === "ok" && typeof t.number === "number" && Number.isFinite(t.number) ? t.number : null;
    return { state, count };
  }
  return ABSENT;
}

export class WeightsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  async pending(): Promise<Result<WeightsPending>> {
    const answer = await staffDashboard.dashboardGetHome(this.#client);
    return answer.ok ? { ok: true, value: toWeightsPending(answer.value) } : answer;
  }
}

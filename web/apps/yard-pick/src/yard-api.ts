// Yard Pick's one door to the core: the yard team's reads and writes, over
// the generated tasks client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the yard's screens use. A field the
// projection does not name is dropped at this door and never reaches a
// screen, so the no money rule does not rest on the server alone: the app
// could not show a figure if one arrived.
//
// Port provenance: the call set is the original app's app/src/yard-api.ts
// (the original HardscapeOS app, repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab), rebuilt on HH's generated
// client.
import { ApiClient, staffBranches, staffTasks, type Result } from "@hardscapehouse/api-client";

export interface YardBranch {
  readonly id: string;
  readonly code: string;
  readonly name: string;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");

export function toBranch(raw: unknown): YardBranch {
  const r = bag(raw);
  return { id: text(r.id), code: text(r.code), name: text(r.name) };
}

/** Every call the yard's screens make. */
export class YardApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The branches this login may reach: the yard names one for the queue, and the board shows its name. */
  async branches(): Promise<Result<YardBranch[]>> {
    const answer = await staffBranches.branchesList(this.#client);
    return answer.ok ? { ok: true, value: answer.value.map(toBranch) } : answer;
  }

  /** The yard board: one branch's pick tasks joined to their tickets and open shortages. */
  async queue(branchId: string, cursor = ""): Promise<Result<{ picks: unknown[]; nextCursor: string }>> {
    const answer = await staffTasks.yardListPickQueue(this.#client, { branch_id: branchId, cursor: cursor === "" ? undefined : cursor, limit: 50 });
    if (!answer.ok) return answer;
    return { ok: true, value: { picks: [], nextCursor: answer.value.next_cursor ?? "" } };
  }
}

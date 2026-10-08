// HH Drive's one door to the core: the driver's reads and writes, over the
// generated dispatch client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the driver's screens use. A field the
// projection does not name is dropped at this door and never reaches a screen,
// so the "no money" rule does not rest on the server alone: the app could not
// show a figure if one arrived. The core's own types carry no money on these
// reads either (dispatch.Stop marshals none, DriverLine has no money member);
// this is the second wall, and the bundle fence is the third.
//
// Port provenance: the call set is the original app's app/src/dispatch-api.ts
// (the original HardscapeOS app, repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab), rebuilt on HH's generated client.
import { ApiClient, staffDispatch, type Result } from "@hardscapehouse/api-client";

export interface DriverLine {
  readonly line_no: number;
  readonly sku: string;
  readonly description: string;
  readonly qty: string;
  readonly uom: string;
}

export interface DriverPhoto {
  readonly id: string;
  readonly caption: string;
}

export interface DriverStop {
  readonly id: string;
  readonly stop_no: number;
  readonly order_no: string;
  readonly customer_name: string;
  readonly status: string;
  readonly time_window: string;
  readonly contact_name: string;
  readonly contact_phone: string;
  readonly address_line: string;
  readonly city: string;
  readonly postal_code: string;
  readonly instructions: string;
  readonly pod_received_by: string;
  readonly pod_at: string;
  readonly failure_reason: string;
  readonly photo_count: number;
  readonly weight_kg: number | null;
  readonly load_seq: number;
  readonly load_count: number;
  readonly created_at: string;
  readonly photos: readonly DriverPhoto[];
}

export interface DriverRun {
  readonly id: string;
  readonly run_no: string;
  readonly truck_code: string;
  readonly status: string;
  readonly stops: readonly DriverStop[];
  /** The lines that come off the truck, by stop id. A stop with no entry has no answer on record. */
  readonly lines: Readonly<Record<string, readonly DriverLine[]>>;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);

export function toLine(raw: unknown): DriverLine {
  const r = bag(raw);
  return { line_no: num(r.line_no), sku: text(r.sku), description: text(r.description), qty: text(r.qty), uom: text(r.uom) };
}

export function toStop(raw: unknown): DriverStop {
  const r = bag(raw);
  const photos = Array.isArray(r.photos) ? r.photos.map((p) => ({ id: text(bag(p).id), caption: text(bag(p).caption) })) : [];
  return {
    id: text(r.id),
    stop_no: num(r.stop_no),
    order_no: text(r.order_no),
    customer_name: text(r.customer_name),
    status: text(r.status),
    time_window: text(r.time_window),
    contact_name: text(r.contact_name),
    contact_phone: text(r.contact_phone),
    address_line: text(r.address_line),
    city: text(r.city),
    postal_code: text(r.postal_code),
    instructions: text(r.instructions),
    pod_received_by: text(r.pod_received_by),
    pod_at: text(r.pod_at),
    failure_reason: text(r.failure_reason),
    photo_count: num(r.photo_count),
    weight_kg: typeof r.weight_kg === "number" && Number.isFinite(r.weight_kg) ? r.weight_kg : null,
    load_seq: num(r.load_seq),
    load_count: num(r.load_count),
    created_at: text(r.created_at),
    photos,
  };
}

export function toRun(raw: unknown): DriverRun {
  const r = bag(raw);
  const lineBag = bag(r.stop_lines);
  const lines: Record<string, readonly DriverLine[]> = {};
  for (const [stopId, list] of Object.entries(lineBag)) {
    if (Array.isArray(list)) lines[stopId] = list.map(toLine);
  }
  return {
    id: text(r.id),
    run_no: text(r.run_no),
    truck_code: text(r.truck_code),
    status: text(r.status),
    stops: Array.isArray(r.stops) ? r.stops.map(toStop) : [],
    lines,
  };
}

/** Every call the driver's screens make. */
export class DriveApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The runs assigned to this login on one day. The day is the dealer's, named by the caller. */
  async myRuns(day: string): Promise<Result<DriverRun[]>> {
    const answer = await staffDispatch.dispatchListMyRuns(this.#client, { date: day });
    if (!answer.ok) return answer;
    return { ok: true, value: answer.value.items.map(toRun) };
  }

  /** One stop with its proof photos. */
  async stop(id: string): Promise<Result<DriverStop>> {
    const answer = await staffDispatch.dispatchGetStop(this.#client, id);
    return answer.ok ? { ok: true, value: toStop(answer.value) } : answer;
  }

  /** The delivery's proof: the typed name and the recipient's own confirmation, the core's POD shape. */
  async pod(id: string, receivedBy: string): Promise<Result<DriverStop>> {
    const answer = await staffDispatch.dispatchCapturePod(this.#client, id, { received_by: receivedBy, affirmed: true });
    return answer.ok ? { ok: true, value: toStop(answer.value) } : answer;
  }

  /** One proof photo, as the raw picture with its own content type. */
  async uploadPhoto(id: string, bytes: Uint8Array, type: "image/jpeg" | "image/png" | "image/webp"): Promise<Result<true>> {
    const answer = await staffDispatch.dispatchUploadStopPhoto(this.#client, id, bytes, type);
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** Fold photos sent after the signature into the proof record. */
  async attachPhotos(id: string): Promise<Result<DriverStop>> {
    const answer = await staffDispatch.dispatchAttachStopPhotos(this.#client, id);
    return answer.ok ? { ok: true, value: toStop(answer.value) } : answer;
  }

  /** Close the run. The core refuses it, in words, unless every stop has an answer a driver records. */
  async completeRun(id: string): Promise<Result<true>> {
    const answer = await staffDispatch.dispatchCompleteRun(this.#client, id);
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** A plain move along the lifecycle. Delivered is never asked of this door: the core takes it only through proof. */
  async advance(id: string, status: "LOADED" | "EN_ROUTE" | "FAILED", reason = ""): Promise<Result<DriverStop>> {
    const answer = await staffDispatch.dispatchAdvanceStop(this.#client, id, { status, reason });
    return answer.ok ? { ok: true, value: toStop(answer.value) } : answer;
  }
}


// The audit trail's pure half: the words a row says, the sensitive actions
// the badges name, the two different empty sentences, and the filter rules.
// No DOM, no Lit, no client. views-audit.ts holds the element side.
//
// Port provenance: the sensitive set, the filter option list and the two
// empty sentences are the original app's audit-log.ts (repository
// hardscapeos_dibbits, read only; see PROVENANCE.md). The actor cell's rule
// carries over with one honest change: the HH core's audit projection
// carries no resolved name, so the cell shows the actor's role label when
// the row carries one, else the id's own block, never a blank.

/** One audit row as the list projects it. */
export interface AuditEntryLike {
  readonly action?: string;
  readonly entity_type?: string;
  readonly entity_id?: string;
  readonly actor_id?: string;
  readonly actor_role?: string;
  readonly created_at?: string;
}

/**
 * Sensitive action verbs the badges mark, so a reviewer can spot privileged
 * rows at a glance. The cleartext is never present in a row: the server
 * enforces the masked-only projection.
 */
const SENSITIVE_ACTIONS: ReadonlySet<string> = new Set([
  "EVIDENCE_VIEW",
  "EVIDENCE_CHANGE",
  "EVIDENCE_DOWNLOAD",
  "EVIDENCE_OVERRIDE",
  "SIGNATURE_CAPTURE",
  "SIGNATURE_VIEW",
  "PRICE_OVERRIDE",
  "TILL_OVERRIDE",
  "TAX_RELIEF_ASSESS",
]);

export function isSensitive(action: string): boolean {
  return SENSITIVE_ACTIONS.has(action);
}

/**
 * A short, curated action list for the filter dropdown (the trail carries
 * more; free text is also allowed via the entity and actor filters). Coarse
 * HTTP verbs and the common sensitive ones.
 */
export const ACTION_OPTIONS: readonly string[] = Object.freeze([
  "HTTP.POST",
  "HTTP.PUT",
  "HTTP.PATCH",
  "HTTP.DELETE",
  "SALE_COMPLETED",
  "PAYMENT_RECEIVED",
  "PAYMENT_ALLOCATED",
  "ORDER_STATUS_CHANGED",
  "PRICE_OVERRIDE",
  "TILL_OVERRIDE",
  "EVIDENCE_VIEW",
  "TAX_RELIEF_ASSESS",
]);

/**
 * The actor's role as a person reads it. The core's role codes are upper
 * case words with underscores; the cell opens them out the same way every
 * unknown closed value degrades: lowercased, separators as spaces. An actor
 * with no role and no id is said plainly, never a blank.
 */
export function actorWords(entry: AuditEntryLike): string {
  const role = entry.actor_role ?? "";
  if (role !== "") return role.trim().toLowerCase().replace(/[_.-]+/g, " ").replace(/\s+/g, " ");
  const id = entry.actor_id ?? "";
  if (id !== "") return id.slice(0, 8);
  return "No actor recorded";
}

const MONTH_WORD = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"] as const;

/**
 * A row's moment as a person reads it, in UTC so the trail reads the same in
 * every yard. An unparsable value shows as it came: the trail is evidence,
 * and evidence is never blanked by a formatter.
 */
export function whenWords(iso: string, now: number | null = null): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const month = MONTH_WORD[d.getUTCMonth()];
  const day = d.getUTCDate();
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  return `${month} ${day}, ${hh}:${mm} UTC`;
}

/** The entity cell: the type and, when the row carries one, the id. */
export function entityWords(entry: AuditEntryLike): string {
  const type = entry.entity_type ?? "";
  if (type === "") return "";
  const id = entry.entity_id ?? "";
  return id === "" ? type : `${type} / ${id}`;
}

/** The empty filter set, as the list's own rule names it. */
export interface AuditFilters {
  readonly entity_type: string;
  readonly action: string;
  readonly actor_id: string;
  readonly since: string;
}

export const EMPTY_FILTERS: AuditFilters = { entity_type: "", action: "", actor_id: "", since: "" };

/** A filter set that differs from the empty one is active. */
export function filtersActive(filters: AuditFilters): boolean {
  return (Object.keys(EMPTY_FILTERS) as (keyof AuditFilters)[]).some((k) => filters[k] !== EMPTY_FILTERS[k]);
}

/**
 * The two empty states are different sentences (the original's own DIB-425
 * rule): nothing matches these filters is not nothing has happened. The
 * trail is append only, and that fact is the words either way.
 */
export function auditEmptyWords(filtered: boolean): string {
  if (filtered) {
    return "No audit entries match these filters. The trail is append only and nothing is ever removed from it, so this means the filters are narrower than the record, not that the actions did not happen. Widen the time window, clear the entity type, or set the action back to all.";
  }
  return "Nothing has been recorded yet. The audit log holds one row for every mutating action: who changed what, when, where and why. It is empty on a database where nobody has changed anything yet.";
}

/** The audit trail is leadership's read (tier 1, all branches): a refusal says who may open it. */
export function auditRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) return "You may not open this. The audit trail is read by leadership only.";
  return error !== "" ? error : "Try again in a moment.";
}

/** An entity whose trail came back with no entries: a statement about the read, not the entity's history. */
export function trailEmptyWords(): string {
  return "The trail holds no entries for this entity. Entries are written when an action changes it, so an entity nobody has changed since the log began has none.";
}

/**
 * The since input (a datetime-local value) goes onto the wire as an instant.
 * An empty or unparsable value goes as it came, and the empty one is
 * filtered out at the query builder before it ever becomes a parameter.
 */
export function sinceWire(since: string): string {
  if (since.trim() === "") return "";
  const d = new Date(since);
  return Number.isNaN(d.getTime()) ? since : d.toISOString();
}

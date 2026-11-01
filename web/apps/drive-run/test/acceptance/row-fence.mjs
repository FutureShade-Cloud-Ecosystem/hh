// True only when the row's own reference names one of the driver's stops. A pod row carries
// data.stop_id; an attachment row relates to a stop through data.links, an entry with
// owner_kind "stop" and an owner_id. Free text (a caption) is never read, and a row with
// neither reference is not on a stop.
export function rowOnDriverStop(row, driverStopIds) {
  const data = row && typeof row === "object" ? row.data : undefined;
  if (!data || typeof data !== "object") return false;
  const ids = new Set(driverStopIds);
  if (typeof data.stop_id === "string" && ids.has(data.stop_id)) return true;
  if (Array.isArray(data.links)) {
    return data.links.some((l) => l && l.owner_kind === "stop" && typeof l.owner_id === "string" && ids.has(l.owner_id));
  }
  return false;
}

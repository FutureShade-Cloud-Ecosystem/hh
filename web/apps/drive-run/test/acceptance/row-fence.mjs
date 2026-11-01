// True when the row sits on one of the driver's stops.
export function rowOnDriverStop(row, driverStopIds) {
  const text = JSON.stringify(row);
  return driverStopIds.some((id) => text.includes(id));
}

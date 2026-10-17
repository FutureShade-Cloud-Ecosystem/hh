// The people the desk can name by id: the salespeople read every role may make. A read that fails names nobody,
// and the desk then says the plain word for the part ("a manager"), never an id.
import { listSalespeople } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";

export async function readRoster(): Promise<ReadonlyMap<string, string>> {
  try {
    const people = await listSalespeople();
    return new Map(people.map((person) => [person.id, person.name]));
  } catch {
    return new Map();
  }
}

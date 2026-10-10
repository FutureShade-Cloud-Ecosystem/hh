// _names.ts: names and words, never ids. A person or a record the server names only by id is said by what the
// desk can prove (a name from a roster it already read), else by a plain word for what it is. DOM-free.

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
const UUID_ALL = new RegExp(UUID.source, "gi");

/** True for a string that is an identifier rather than a word. */
export function looksLikeId(value: string): boolean {
  return UUID.test(value);
}

/**
 * Who a person id names: their name from the roster the desk read, else the plain word for their part
 * ("a manager"). An id is never the answer.
 */
export function personName(id: string | undefined, roster: ReadonlyMap<string, string>, fallback: string): string {
  if (id === undefined || id === "") return fallback;
  const name = roster.get(id);
  return name !== undefined && name !== "" ? name : fallback;
}

/**
 * A sentence with its ids taken out: a parenthesised id goes with its parentheses, any other id becomes the
 * plain word given. The server's words are kept, only the identifiers go.
 */
export function withoutIds(sentence: string, word = "a record"): string {
  return sentence
    .replace(new RegExp(`\\s*\\(\\s*${UUID.source}\\s*\\)`, "gi"), "")
    .replace(UUID_ALL, word)
    .replace(/\s{2,}/g, " ")
    .trim();
}

// The acceptance's idempotency key: a UUID the page derives, never stores.
//
// The page may touch no browser storage (the sinks scan holds that), yet every
// attempt of ONE acceptance must carry the SAME key, across a lost answer, a
// reload and another tab. So the key is a function of what makes an
// acceptance the same acceptance: the share token and the typed name exactly
// as sent. The first 16 bytes of SHA-256 of
//   "quote-accept:" + token + ":" + typedName
// become a name based UUID (version 5 bits, RFC 4122 variant bits). The
// server answers a repeat of a key with the first outcome and writes nothing,
// and refuses the same key with another typed name, so a different name is,
// by construction, a different key and a deliberate new acceptance.

const HEX = "0123456789abcdef";

function uuidFromBytes(bytes: Uint8Array): string {
  let out = "";
  for (let index = 0; index < 16; index += 1) {
    const byte = bytes[index] ?? 0;
    out += HEX[byte >> 4] + HEX[byte & 0x0f];
    if (index === 3 || index === 5 || index === 7 || index === 9) out += "-";
  }
  return out;
}

/** The deterministic key, or null when crypto.subtle is unavailable (an insecure context). */
export async function deriveAcceptKey(
  token: string,
  typedName: string,
  cryptoImpl: Pick<Crypto, "subtle"> | undefined = globalThis.crypto,
): Promise<string | null> {
  const subtle = cryptoImpl?.subtle;
  if (subtle === undefined || typeof subtle.digest !== "function") return null;
  const data = new TextEncoder().encode(`quote-accept:${token}:${typedName}`);
  const digest = new Uint8Array(await subtle.digest("SHA-256", data));
  const bytes = digest.slice(0, 16);
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x50;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  return uuidFromBytes(bytes);
}

function randomUuid(cryptoImpl: Pick<Crypto, "getRandomValues">): string {
  const bytes = new Uint8Array(16);
  cryptoImpl.getRandomValues(bytes);
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  return uuidFromBytes(bytes);
}

export interface AcceptKeys {
  keyFor(token: string, typedName: string): Promise<string>;
}

/**
 * The page's key source. Where crypto.subtle is unavailable (an insecure
 * context), derivation is impossible, so the fallback is one random UUID per
 * distinct acceptance (token and typed name) kept IN MEMORY for the page's
 * life: retries within this page life still share a key, though a reload gets
 * a fresh one. Memory only, never storage.
 */
export function createAcceptKeys(cryptoImpl: Crypto | undefined = globalThis.crypto): AcceptKeys {
  const remembered = new Map<string, string>();
  return {
    async keyFor(token, typedName) {
      const derived = await deriveAcceptKey(token, typedName, cryptoImpl);
      if (derived !== null) return derived;
      const id = JSON.stringify([token, typedName]);
      let key = remembered.get(id);
      if (key === undefined) {
        key = randomUuid(cryptoImpl);
        remembered.set(id, key);
      }
      return key;
    },
  };
}

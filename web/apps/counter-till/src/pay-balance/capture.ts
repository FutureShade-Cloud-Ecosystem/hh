// The Pay balance refusal body's reader. The generated client resolves a refusal to the envelope's
// error string and the status; the contract puts the money fields (the balance the door wanted,
// the amount sent, a message in dollars) in the 422 body beside that envelope. The till wraps its
// transport's fetch (the same documented injection point the refund approval capture uses) and
// keeps that one body for the flow to read when the refusal says so. Only the pay balance route's
// refusals are read; every other response passes untouched, and an unreadable body is stored as
// nothing, so the surface speaks the refusal without figures rather than half of them.
import type { FetchLike, ResponseLike } from "@hardscapehouse/api-client";

const PAY_BALANCE_PATH = "/api/v1/pos/pay-balance";

export interface PayBalanceRefusalFields {
  message?: string;
  balance_cents?: number;
  sent_cents?: number;
}

export class PayBalanceCapture {
  #fields: PayBalanceRefusalFields | null = null;
  readonly fetch: FetchLike;

  constructor(inner: FetchLike) {
    this.fetch = async (url, init) => {
      const response = await inner(url, init);
      if (response.ok) return response;
      let pathname: string;
      try {
        pathname = new URL(url, "http://same-origin.invalid").pathname;
      } catch {
        return response;
      }
      if (pathname !== PAY_BALANCE_PATH) return response;
      let text = "";
      try {
        text = await response.text();
      } catch {
        return response;
      }
      this.#fields = null;
      try {
        const parsed: unknown = JSON.parse(text);
        if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
          const record = parsed as Record<string, unknown>;
          this.#fields = {
            message: typeof record["message"] === "string" ? record["message"] : undefined,
            balance_cents: Number.isSafeInteger(record["balance_cents"]) ? (record["balance_cents"] as number) : undefined,
            sent_cents: Number.isSafeInteger(record["sent_cents"]) ? (record["sent_cents"] as number) : undefined,
          };
        }
      } catch {
        // Not JSON: the transport's own reading of the envelope stands.
      }
      return rebuilt(response, text);
    };
  }

  /** The last refusal's fields, and then they are gone: one refusal, one read. */
  take(): PayBalanceRefusalFields {
    const fields = this.#fields ?? {};
    this.#fields = null;
    return fields;
  }
}

function rebuilt(response: ResponseLike, text: string): ResponseLike {
  return {
    status: response.status,
    ok: false,
    text: async () => text,
    arrayBuffer: async () => new TextEncoder().encode(text).buffer as ArrayBuffer,
  };
}

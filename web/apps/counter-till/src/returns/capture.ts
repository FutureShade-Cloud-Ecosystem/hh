// The refusal body's reader. The generated client resolves a refusal to the
// envelope's error string and the status, and the contract puts the approval
// (the amount and the approver roles) in the 409's body beside that envelope,
// present exactly on the approval refusal: so the till wraps its transport's
// fetch (the documented injection point: the shell may wrap the transport
// wholesale) and keeps that one body for the flow to read when the refusal
// Result says refund_approval_required. Only the returns route's refusals are
// read; every other response passes untouched, and a body that is not a
// readable approval is stored as nothing, so the desk shows the refusal
// without figures rather than half of them.
import type { FetchLike, PosRefundApproval, ResponseLike } from "@hardscapehouse/api-client";
import { validateRefundApproval } from "./approval.js";

// The write routes whose 409 carries the approval: the single return, the credit note across orders, and the swap.
const RETURNS_PATHS = ["/api/v1/pos/returns", "/api/v1/pos/returns-across", "/api/v1/pos/swaps"];
const APPROVAL_ERROR = "refund_approval_required";

export class RefundApprovalCapture {
  #approval: PosRefundApproval | null = null;
  readonly fetch: FetchLike;

  constructor(inner?: FetchLike) {
    // The same discipline as the transport's own default: the platform's
    // global fetch is read at call time, so a later polyfill is honoured.
    const transport: FetchLike =
      inner ??
      ((url, init) => {
        const f = globalThis.fetch;
        if (typeof f !== "function") return Promise.reject(new TypeError("no global fetch is available"));
        return f(url, init as RequestInit);
      });
    this.fetch = async (url, init) => {
      const response = await transport(url, init);
      if (response.ok) return response;
      let pathname: string;
      try {
        pathname = new URL(url, "http://same-origin.invalid").pathname;
      } catch {
        return response;
      }
      if (!RETURNS_PATHS.includes(pathname)) return response;
      let text = "";
      try {
        text = await response.text();
      } catch {
        return response;
      }
      try {
        const parsed: unknown = JSON.parse(text);
        if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
          const record = parsed as Record<string, unknown>;
          if (record["error"] === APPROVAL_ERROR) {
            const approval = validateRefundApproval(record["approval"]);
            if (approval !== null) this.#approval = approval;
          }
        }
      } catch {
        // Not JSON: the plain envelope's own reading is the transport's job.
      }
      return refusedResponse(response, text);
    };
  }

  /** The last refusal's approval, and then it is gone: one refusal, one read. */
  take(): PosRefundApproval | null {
    const approval = this.#approval;
    this.#approval = null;
    return approval;
  }
}

// The refusal's body has been read to the end, so the response the transport
// would hand back is rebuilt around the cached text: the transport's refusal
// path calls text() exactly once for the envelope's error string.
function refusedResponse(response: ResponseLike, text: string): ResponseLike {
  return {
    status: response.status,
    ok: false,
    text: async () => text,
    arrayBuffer: async () => new TextEncoder().encode(text).buffer as ArrayBuffer,
  };
}

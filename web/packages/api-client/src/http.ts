// The generated operations' transport. One ApiClient per configured base URL;
// the generated code calls client.request with the method, the path template,
// the typed parameters, and the body, and this module does the rest: header
// resolution, URL building, the fetch, and the Result.
//
// The transport owns no identity. There is no token storage and no credential
// handling here (that is HH-4.2's): anything the caller must present per
// request, such as the contract's shared X-Org header, arrives through the
// caller-set header provider. The fetch itself is an injectable FetchLike with
// a default binding to the platform's global fetch, so tests record instead of
// dialing and the shell can wrap or replace the transport wholesale.
//
// Cancellation and deadlines: a request carries an AbortSignal and an optional
// timeoutMs (ApiCallOptions, what the generated operations accept as their
// optional trailing call argument). The transport aborts the fetch on either
// and resolves an error Result naming which one fired; it never rejects.
//
// Reading the response: JSON by default, the raw text when the generator
// marked the operation textResponse (plain string content: text/csv,
// text/html), the raw bytes when it marked bytesResponse (format: binary
// content: images, workbooks). A refusal (4xx, 5xx) always reads the body as
// text, because the contract's error envelope is JSON in every case.
import { err, ok, type Result } from "./result.js";

export type FetchLike = (url: string, init: RequestInitLike) => Promise<ResponseLike>;

export interface RequestInitLike {
  method: string;
  headers: Record<string, string>;
  body?: string | FormData | Uint8Array | ArrayBuffer;
  signal?: AbortSignal;
}

export interface ResponseLike {
  status: number;
  ok: boolean;
  text(): Promise<string>;
  arrayBuffer(): Promise<ArrayBuffer>;
}

/** Returns the headers to send with every request, per client. */
export type HeaderProvider = () => Record<string, string> | Promise<Record<string, string>>;

/** Per-call transport options the generated operations accept as call. */
export interface ApiCallOptions {
  /** Aborts the request; the operation resolves an error Result naming it. */
  signal?: AbortSignal;
  /** Aborts the request after this many milliseconds. */
  timeoutMs?: number;
}

export interface ApiClientOptions {
  /** The base URL, for example the edge's origin; the contract pins no host. */
  baseUrl: string;
  /** Defaults to the platform's global fetch, bound at call time. */
  fetch?: FetchLike;
  /** Caller-set header provider (org slugs, credentials, correlation ids). */
  headers?: HeaderProvider;
}

/** One operation call, as the generated code builds it. */
export interface OperationRequest {
  method: string;
  /** The documented path template, for example /api/v1/tenant/orders/{id}. */
  path: string;
  pathParams?: Record<string, string | number>;
  /** Query parameters; undefined and null entries are omitted, arrays repeat. */
  query?: Record<string, unknown>;
  /** A JSON request body; sent with the application/json content type. */
  body?: unknown;
  /** A multipart request body, serialized as FormData by the transport. */
  form?: Record<string, unknown>;
  /** A non-JSON request body (bytes, CSV), sent verbatim. */
  rawBody?: { contentType: string; content: string | Uint8Array };
  /** Set by the generator when the success response is the wire string. */
  textResponse?: boolean;
  /** Set by the generator when the success response is format: binary bytes. */
  bytesResponse?: boolean;
  /** Aborts the request; the operation resolves an error Result naming it. */
  signal?: AbortSignal;
  /** Aborts the request after this many milliseconds. */
  timeoutMs?: number;
  /** The contract operationId, carried for diagnostics only. */
  operationId?: string;
}

const JSON_CONTENT_TYPE = "application/json";

// The default binding reads globalThis.fetch at call time, so a later polyfill
// or test double installed on the global is honored.
const defaultFetch: FetchLike = (url, init) => {
  const f = globalThis.fetch;
  if (typeof f !== "function") return Promise.reject(new TypeError("no global fetch is available"));
  return f(url, init as RequestInit);
};

// The one loud failure class, now refused as data like every other: a path
// parameter that is missing, empty, or a dot segment would not produce a
// request worth sending, so request() turns these into an error Result with a
// message naming the parameter, and never dials. This is also where the
// string | number parameter the generated code passes becomes the wire's
// string spelling, converted with String().
function fillPath(path: string, params: Record<string, string | number> | undefined): string {
  if (!params) return path;
  return path.replace(/\{([^}]+)\}/g, (whole, name: string) => {
    const value = params[name];
    if (value === undefined) throw new Error(`path parameter ${name} is missing`);
    const text = String(value);
    if (text === "") throw new Error(`path parameter ${name} is empty`);
    if (text === "." || text === "..") throw new Error(`path parameter ${name} is the path segment ${text}`);
    return encodeURIComponent(text);
  });
}

function appendQuery(url: string, query: Record<string, unknown> | undefined): string {
  if (!query) return url;
  const params = new URLSearchParams();
  for (const [name, value] of Object.entries(query)) {
    if (value === undefined || value === null) continue;
    if (Array.isArray(value)) {
      for (const item of value) params.append(name, String(item));
    } else {
      params.append(name, String(value));
    }
  }
  const qs = params.toString();
  return qs === "" ? url : `${url}?${qs}`;
}

function asMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// The contract's error envelope is Error ({error: string}); the refusal string
// is that field verbatim when the body carries it, else the body itself, else
// the status.
function refusalFrom(status: number, text: string): string {
  try {
    const parsed: unknown = JSON.parse(text);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const error = (parsed as Record<string, unknown>)["error"];
      if (typeof error === "string") return error;
    }
  } catch {
    // not JSON: fall through to the raw body
  }
  return text === "" ? `HTTP ${status}` : text;
}

export class ApiClient {
  readonly baseUrl: string;
  private readonly fetchLike: FetchLike;
  private readonly headerProvider: HeaderProvider | undefined;

  constructor(options: ApiClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.fetchLike = options.fetch ?? defaultFetch;
    this.headerProvider = options.headers;
  }

  /**
   * Run one operation. Resolves a Result in every documented refusal case (a
   * 4xx or 5xx response, a malformed success body, a transport error, an
   * aborted or timed-out request, a failed header provider, an unusable path
   * parameter) and never throws on those, and never rejects: even a caller
   * error such as a missing path parameter resolves an error Result naming it,
   * because a caller that holds the Result needs no try around the call.
   */
  async request<T>(req: OperationRequest): Promise<Result<T>> {
    const id = req.operationId ?? req.path;
    let path: string;
    try {
      path = fillPath(req.path, req.pathParams);
    } catch (error) {
      return err(`${id}: ${(error as Error).message}`);
    }
    const url = appendQuery(`${this.baseUrl}${path}`, req.query);

    let providedHeaders: Record<string, string>;
    try {
      providedHeaders = await this.resolveHeaders();
    } catch (error) {
      return err(`${id}: the header provider failed: ${asMessage(error)}`);
    }
    const headers: Record<string, string> = { ...providedHeaders };
    let body: string | FormData | Uint8Array | ArrayBuffer | undefined;
    if (req.rawBody !== undefined) {
      body = req.rawBody.content;
      headers["Content-Type"] = req.rawBody.contentType;
    } else if (req.form !== undefined) {
      const form = new FormData();
      for (const [name, value] of Object.entries(req.form)) {
        if (value === undefined || value === null) continue;
        form.append(name, value instanceof Blob ? value : String(value));
      }
      body = form; // fetch sets the multipart content type and its boundary
    } else if (req.body !== undefined) {
      body = JSON.stringify(req.body);
      headers["Content-Type"] = JSON_CONTENT_TYPE;
    }
    const init: RequestInitLike = { method: req.method, headers, ...(body !== undefined ? { body } : {}) };

    // The caller's signal and the timeout drive one controller, so the fetch
    // aborts on whichever fires first and the refusal names it.
    let response: ResponseLike;
    {
      const controller = new AbortController();
      let abortKind: "timeout" | "caller" | undefined;
      const onCallerAbort = () => {
        abortKind = "caller";
        controller.abort();
      };
      if (req.signal !== undefined) {
        if (req.signal.aborted) onCallerAbort();
        else req.signal.addEventListener("abort", onCallerAbort, { once: true });
      }
      let timer: ReturnType<typeof setTimeout> | undefined;
      if (req.timeoutMs !== undefined) {
        timer = setTimeout(() => {
          abortKind = "timeout";
          controller.abort();
        }, req.timeoutMs);
      }
      try {
        response = await this.fetchLike(url, { ...init, signal: controller.signal });
      } catch (error) {
        if (abortKind === "timeout") return err(`${id}: the request timed out after ${req.timeoutMs} ms`);
        if (abortKind === "caller") return err(`${id}: the request was aborted`);
        return err(`${id}: transport error: ${asMessage(error)}`);
      } finally {
        if (timer !== undefined) clearTimeout(timer);
        if (req.signal !== undefined) req.signal.removeEventListener("abort", onCallerAbort);
      }
    }

    if (!response.ok) {
      // The refusal stands on the status even when the body cannot be read.
      let text = "";
      try {
        text = await response.text();
      } catch {
        // keep the empty body: refusalFrom falls back to the status
      }
      return err(refusalFrom(response.status, text), response.status);
    }
    if (req.bytesResponse) {
      try {
        return ok(new Uint8Array(await response.arrayBuffer()) as T);
      } catch (error) {
        return err(`${id}: transport error reading the response: ${asMessage(error)}`);
      }
    }
    let text: string;
    try {
      text = await response.text();
    } catch (error) {
      return err(`${id}: transport error reading the response: ${asMessage(error)}`);
    }
    if (req.textResponse) return ok(text as T);
    try {
      return ok(JSON.parse(text) as T);
    } catch {
      return err(`${id}: the success response is not valid JSON`);
    }
  }

  private async resolveHeaders(): Promise<Record<string, string>> {
    if (this.headerProvider === undefined) return {};
    const provided = await this.headerProvider();
    return { ...provided };
  }
}

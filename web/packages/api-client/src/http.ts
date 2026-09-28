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
import { err, ok, type Result } from "./result.js";

export type FetchLike = (url: string, init: RequestInitLike) => Promise<ResponseLike>;

export interface RequestInitLike {
  method: string;
  headers: Record<string, string>;
  body?: string | FormData;
}

export interface ResponseLike {
  status: number;
  ok: boolean;
  text(): Promise<string>;
}

/** Returns the headers to send with every request, per client. */
export type HeaderProvider = () => Record<string, string> | Promise<Record<string, string>>;

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
  pathParams?: Record<string, string>;
  /** Query parameters; undefined and null entries are omitted, arrays repeat. */
  query?: Record<string, unknown>;
  /** A JSON request body; sent with the application/json content type. */
  body?: unknown;
  /** A multipart request body, serialized as FormData by the transport. */
  form?: Record<string, unknown>;
  /** A non-JSON request body (bytes, CSV), sent verbatim. */
  rawBody?: { contentType: string; text: string };
  /** Set by the generator when the success response is the wire string. */
  textResponse?: boolean;
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

function fillPath(path: string, params: Record<string, string> | undefined): string {
  if (!params) return path;
  return path.replace(/\{([^}]+)\}/g, (whole, name: string) => {
    const value = params[name];
    if (value === undefined) throw new Error(`operation path parameter ${name} is missing`);
    return encodeURIComponent(value);
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
   * 4xx or 5xx response, a malformed success body, a transport error) and
   * never throws on those; only a caller error such as a missing path
   * parameter throws, because there is no request worth refusing yet.
   */
  async request<T>(req: OperationRequest): Promise<Result<T>> {
    let path: string;
    try {
      path = fillPath(req.path, req.pathParams);
    } catch (error) {
      throw new Error(`${req.operationId ?? req.path}: ${(error as Error).message}`);
    }
    const url = appendQuery(`${this.baseUrl}${path}`, req.query);

    const headers: Record<string, string> = { ...(await this.resolveHeaders()) };
    let body: string | FormData | undefined;
    if (req.rawBody !== undefined) {
      body = req.rawBody.text;
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

    let response: ResponseLike;
    try {
      response = await this.fetchLike(url, init);
    } catch (error) {
      return err(`${req.operationId ?? req.path}: transport error: ${asMessage(error)}`);
    }
    let text: string;
    try {
      text = await response.text();
    } catch (error) {
      return err(`${req.operationId ?? req.path}: transport error reading the response: ${asMessage(error)}`);
    }
    if (!response.ok) return err(refusalFrom(response.status, text));
    if (req.textResponse) return ok(text as T);
    try {
      return ok(JSON.parse(text) as T);
    } catch {
      return err(`${req.operationId ?? req.path}: the success response is not valid JSON`);
    }
  }

  private async resolveHeaders(): Promise<Record<string, string>> {
    if (this.headerProvider === undefined) return {};
    const provided = await this.headerProvider();
    return { ...provided };
  }
}

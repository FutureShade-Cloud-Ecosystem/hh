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
// and resolves an error Result naming which one fired; it never rejects. Both
// are armed before the header provider is called, so the provider's call sits
// inside the deadline: a provider that never settles (a wedged auth lookup)
// resolves the timeout Result instead of hanging the caller past it. The dial
// itself is raced against the armed controller, not merely handed its signal:
// a custom FetchLike that ignores init.signal and never settles still
// resolves the deadline's error Result instead of hanging the caller (the
// README states the contract a replacement must honour). Both stay armed
// until the response body has been read, because a fetch resolves at the
// headers and a body that stalls after them is still a pending request.
// Redirects are refused (redirect: "error"): a redirect could replay a
// request's headers and body against another origin, so a redirected request
// resolves a transport error Result instead. A caller-supplied multipart file
// name is guarded like a path value and refused as an error Result before
// anything is sent (see filenameProblem).
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
  /**
   * Always "error": the transport never follows a redirect. A redirect could
   * replay a request's headers and body against another origin, so a
   * redirected request resolves a transport error Result instead.
   */
  redirect: "error";
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
  /**
   * The multipart part name per form field, for the binary fields the caller
   * holds as Blobs: the generated multipart operations expose it as their
   * options argument (`{ fileFilename: "price-list.xlsx" }`). A name is
   * guarded like a path value: empty, one of the dot segments (`.` or `..`),
   * carrying a path separator (`/` or `\`), carrying a tab, carrying a line
   * break, carrying a NUL or any other control character, carrying a Unicode
   * bidirectional control character (the whole \p{Bidi_Control} class),
   * carrying an invisible format character (\p{Cf}, the two allowed joiners
   * U+200C and U+200D excepted) or a control character \p{Cc} that the
   * per-character loop would otherwise miss (the C1 range among them),
   * carrying a line separator (U+2028 or U+2029) inside the name, carrying a
   * Hangul filler (U+115F, U+1160, U+3164; a name of nothing but U+115F is
   * invisible), made of nothing but
   * spaces, beginning or ending with any white space or a dot, or longer
   * than 160
   * UTF-8 bytes (the bound the core's own stored name keeps, counted in the
   * same unit the core counts) resolves an error Result before anything
   * dials. Every refusal names what it refused in words a person understands
   * (contains a tab, ends with a line break, contains a hidden character
   * (U+00AD)), with the code point for the invisible classes. A field with
   * no name here (or no entry) rides under the
   * platform's default part name, "blob", which is what the core stores when
   * the caller says nothing.
   */
  filenames?: Record<string, string | undefined>;
  /**
   * A non-JSON request body (bytes, CSV), sent verbatim. The contentType is
   * the operation's declared wire type: present when the contract declares
   * exactly one (or the caller chose among several), absent when the caller
   * passed no contentType for an upload that declares more than one, which
   * sends no Content-Type header at all so an unsaid upload is refused, never
   * mislabeled.
   */
  rawBody?: { contentType?: string; content: string | Uint8Array };
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

// The set of header value characters the platform's Headers refuses (a
// control character, a bare CR or LF, DEL). The platform's own refusal
// message echoes the refused value, and header values are where tokens
// travel, so the transport checks first and names the header, never its
// value.
const INVALID_HEADER_VALUE = /[^\t\x20-\x7e\x80-\xff]/;

// The longest file name the transport accepts, counted in BYTES of the
// name's UTF-8 spelling. The unit is the core's own: the stored-name cap
// (core/internal/pricing/pricefile_service.go, sanitizeFileName, lines 845
// to 861) checks strings.Builder.Len() after each WriteRune, which counts
// bytes, so a name that passes here is a name the core can store whole
// rather than truncate. A code point count once let a name of 160 three-byte
// CJK characters pass here (480 bytes) and the core cut it to 54 characters.
const MAX_FILENAME_BYTES = 160;

// The Unicode bidirectional control characters, the WHOLE class (the
// binary property \p{Bidi_Control}: U+061C ALM, U+200E LRM, U+200F RLM,
// U+202A through U+202E, and U+2066 through U+2069): the marks that reorder
// how text displays. A file name carrying one can be made to display as
// something it is not (an extension appearing reversed is the classic), so
// they are refused outright; the first cut refused only nine of the twelve
// and let the three reorder neutrals through. Tested one character at a
// time so the refusal can name the character it found.
const BIDI_CONTROL = /\p{Bidi_Control}/u;

// The format characters (\p{Cf}: the zero width marks, the joiners, the
// soft hyphen, and the byte order mark among them) and the remaining
// control characters (\p{Cc}: the C0 range and DEL that the byte loop in
// filenameProblem refuses first, plus the C1 range U+0080 to U+009F the
// loop's per code unit comparison cannot see). An invisible character in a
// stored name makes names that display identically but differ in bytes, so
// both classes are refused outright. Tested one character at a time so the
// refusal can name the character it found.
const FORMAT_CHARACTER = /\p{Cf}/u;
const CONTROL_CHARACTER = /\p{Cc}/u;

// The two joiners a name may carry: U+200C ZERO WIDTH NON-JOINER and
// U+200D ZERO WIDTH JOINER. Both are \p{Cf} format characters, but they
// join letters (Persian words are spelled with the non-joiner) and build
// emoji (the family and profession sequences are joiner chains), and they
// hide nothing: each is allowed where every other format character is
// refused, so an ordinary Persian name and a plain emoji name are not
// refused by a rule meant for invisible marks. The first cut refused them
// with their class, and a rule that refuses ordinary names gets switched
// off.
const ALLOWED_JOINERS = new Set([0x200c, 0x200d]);

// The line separators: U+2028 LINE SEPARATOR (the whole \p{Zl} class) and
// U+2029 PARAGRAPH SEPARATOR (the whole \p{Zp} class). The edge rule below
// refuses them at either edge; refused inside as well, because a line break
// in the middle of a displayed name is a paste artefact and the core's
// sanitizer would keep it.
const LINE_SEPARATORS = new Set([0x2028, 0x2029]);

// The Hangul fillers: U+115F HANGUL CHOSEONG FILLER, U+1160 HANGUL
// JUNGSEONG FILLER, and U+3164 HANGUL FILLER. They are letters, not format
// characters and not white space, so no other rule sees them, and a name
// made of nothing but U+115F is accepted by every other rule and invisible:
// refused outright, as one invisible character more.
const HANGUL_FILLERS = new Set([0x115f, 0x1160, 0x3164]);

function codePointName(code: number): string {
  return `U+${code.toString(16).toUpperCase().padStart(4, "0")}`;
}

// A file name rides the multipart part's Content-Disposition as given, so it
// gets the care fillPath gives a path parameter: refused here, as data,
// before anything is sent. Refused: empty, one of the dot segments, a path
// separator (either platform's), a tab, a line break (named as such, since a
// name pasted from a spreadsheet cell usually ends in one), any other
// control character (each named by its code point), a bidirectional control
// character, an invisible format character (each named by its code point;
// the two allowed joiners excepted, see ALLOWED_JOINERS), a line separator
// inside the name, a Hangul filler (a name of nothing but U+115F is
// invisible), a name made of nothing but spaces, a
// name that begins or ends with ANY white space or a dot (the core's
// sanitizer trims edge white space off the stored name with TrimSpace, a
// Unicode trim, so an accepted NBSP, ideographic space, or line separator
// at an edge would arrive as a name the core stores changed; a Windows
// reader strips edge spaces and a trailing dot, and a leading dot hides the
// file on a Unix-like reader, so the name would not stay the name the
// caller said), and anything past MAX_FILENAME_BYTES. Every refusal NAMES
// what it refused in words a person understands (contains a tab, ends with
// a line break, contains a hidden character (U+00AD)), so a person who
// pasted from a spreadsheet or a document can fix the name instead of
// guessing at a class name. The platform
// percent-encodes a quote or a line break inside the quoted parameter, so
// framing itself cannot be split, but a separator would hand a path to
// whatever stores the name and a control character would ride the wire raw.
// The bound counts UTF-8 bytes, not code points and not code units, because
// the core's cap counts bytes (see MAX_FILENAME_BYTES).
function filenameProblem(name: string): string | undefined {
  if (name === "") return "is empty";
  if (name === "." || name === "..") return `is the path segment ${name}`;
  let index = 0;
  for (const character of name) {
    const code = character.codePointAt(0) as number;
    const atEnd = index + character.length === name.length;
    index += character.length;
    if (code === 0x2f || code === 0x5c) return "carries a path separator";
    if (code === 0x09) return "contains a tab";
    if (code === 0x0a || code === 0x0d) {
      return atEnd ? "ends with a line break" : "contains a line break";
    }
    if (LINE_SEPARATORS.has(code)) {
      return atEnd ? `ends with a line break (${codePointName(code)})` : `contains a line break (${codePointName(code)})`;
    }
    if (BIDI_CONTROL.test(character)) {
      return `contains a hidden character (${codePointName(code)})`;
    }
    if (FORMAT_CHARACTER.test(character) && !ALLOWED_JOINERS.has(code)) {
      return `contains a hidden character (${codePointName(code)})`;
    }
    if (CONTROL_CHARACTER.test(character)) {
      return `contains a control character (${codePointName(code)})`;
    }
    if (HANGUL_FILLERS.has(code)) {
      return `contains a hidden character (${codePointName(code)})`;
    }
  }
  if (name.trim() === "") return "is nothing but spaces";
  if (/^[\s.]|[\s.]$/u.test(name)) {
    return "begins or ends with a space or a dot";
  }
  const bytes = new TextEncoder().encode(name).length;
  if (bytes > MAX_FILENAME_BYTES) return `is longer than the ${MAX_FILENAME_BYTES} bytes a file name may carry`;
  return undefined;
}

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

    // The caller's signal and the timeout drive one controller, armed before
    // the header provider runs, and both stay armed until the body has been
    // read: a fetch resolves at the headers, and a body that stalls after
    // them is still a pending request. An abort during the provider's call or
    // the read resolves the same timeout or aborted Result an abort before
    // the headers would have.
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
    const aborted = (): Result<T> | undefined => {
      if (abortKind === "timeout") return err(`${id}: the request timed out after ${req.timeoutMs} ms`);
      if (abortKind === "caller") return err(`${id}: the request was aborted`);
      return undefined;
    };
    // The provider's call is raced against the armed controller, so a header
    // provider that never settles cannot outrun the deadline or the caller's
    // abort: this matters for auth, whose provider it is.
    const abortWait = new Promise<{ kind: "abort" }>((resolve) => {
      if (controller.signal.aborted) resolve({ kind: "abort" });
      else controller.signal.addEventListener("abort", () => resolve({ kind: "abort" }), { once: true });
    });

    try {
      let providedHeaders: Record<string, string>;
      const raced = await Promise.race([
        this.resolveHeaders().then(
          (headers) => ({ kind: "headers" as const, headers }),
          (error) => ({ kind: "providerFailed" as const, error }),
        ),
        abortWait,
      ]);
      if (raced.kind === "abort") {
        return aborted() ?? err(`${id}: the request was aborted`);
      }
      if (raced.kind === "providerFailed") {
        return err(`${id}: the header provider failed: ${asMessage(raced.error)}`);
      }
      providedHeaders = raced.headers;
      const headers: Record<string, string> = { ...providedHeaders };
      let body: string | FormData | Uint8Array | ArrayBuffer | undefined;
      if (req.rawBody !== undefined) {
        body = req.rawBody.content;
        // No contentType says no Content-Type at all: a raw upload declaring
        // several types is refused for not saying, never mislabeled.
        if (req.rawBody.contentType !== undefined) headers["Content-Type"] = req.rawBody.contentType;
      } else if (req.form !== undefined) {
        const form = new FormData();
        for (const [name, value] of Object.entries(req.form)) {
          if (value === undefined || value === null) continue;
          if (value instanceof Blob) {
            // A caller-named part rides under the given filename; an unnamed
            // Blob keeps the platform's own default part name, "blob". The
            // name is refused here, before anything is sent, when it is not
            // a plain file name (see filenameProblem).
            const filename = req.filenames?.[name];
            if (filename !== undefined) {
              const problem = filenameProblem(filename);
              if (problem !== undefined) return err(`${id}: the ${name} part's file name ${problem}`);
              form.append(name, value, filename);
            } else {
              form.append(name, value);
            }
          } else {
            form.append(name, String(value));
          }
        }
        body = form; // fetch sets the multipart content type and its boundary
      } else if (req.body !== undefined) {
        body = JSON.stringify(req.body);
        headers["Content-Type"] = JSON_CONTENT_TYPE;
      }
      // A header value the platform would refuse must never reach fetch: the
      // platform's own refusal message echoes the value, and header values are
      // where tokens travel, so the transport refuses here first and names the
      // header, never its value.
      for (const [name, value] of Object.entries(headers)) {
        if (INVALID_HEADER_VALUE.test(value)) {
          return err(`${id}: the ${name} header value is not a valid header value`);
        }
      }
      const init: RequestInitLike = { method: req.method, headers, redirect: "error", ...(body !== undefined ? { body } : {}) };

      // An abort that landed while the request was parked at the provider
      // (or a signal dead on arrival) must not wait on a fetch that can no
      // longer observe it: check the armed controller once more before
      // dialing.
      const beforeDial = aborted();
      if (beforeDial !== undefined) return beforeDial;

      // The dial is raced against the armed controller, not merely handed its
      // signal. The transport contract (the README, "What a custom FetchLike
      // must honour") asks a FetchLike to observe init.signal, and the
      // platform's fetch does, but nothing can force a replacement to: the
      // race is the floor. A transport that accepts the signal and never
      // settles still resolves the deadline's or the caller's abort's error
      // Result here, instead of hanging the caller past it.
      const racedDial = await Promise.race([
        this.fetchLike(url, { ...init, signal: controller.signal }).then(
          (response) => ({ kind: "response" as const, response }),
          (error) => ({ kind: "dialFailed" as const, error }),
        ),
        abortWait,
      ]);
      if (racedDial.kind === "abort") {
        return aborted() ?? err(`${id}: the request was aborted`);
      }
      if (racedDial.kind === "dialFailed") {
        return aborted() ?? err(`${id}: transport error: ${asMessage(racedDial.error)}`);
      }
      const response = racedDial.response;

      if (!response.ok) {
        // The refusal stands on the status even when the body cannot be read.
        let text = "";
        try {
          text = await response.text();
        } catch (error) {
          return aborted() ?? err(`${id}: transport error reading the response: ${asMessage(error)}`);
        }
        return err(refusalFrom(response.status, text), response.status);
      }
      if (req.bytesResponse) {
        try {
          return ok(new Uint8Array(await response.arrayBuffer()) as T);
        } catch (error) {
          return aborted() ?? err(`${id}: transport error reading the response: ${asMessage(error)}`);
        }
      }
      let text: string;
      try {
        text = await response.text();
      } catch (error) {
        return aborted() ?? err(`${id}: transport error reading the response: ${asMessage(error)}`);
      }
      if (req.textResponse) return ok(text as T);
      try {
        return ok(JSON.parse(text) as T);
      } catch {
        return err(`${id}: the success response is not valid JSON`);
      }
    } finally {
      if (timer !== undefined) clearTimeout(timer);
      if (req.signal !== undefined) req.signal.removeEventListener("abort", onCallerAbort);
    }
  }

  private async resolveHeaders(): Promise<Record<string, string>> {
    if (this.headerProvider === undefined) return {};
    const provided = await this.headerProvider();
    return { ...provided };
  }
}

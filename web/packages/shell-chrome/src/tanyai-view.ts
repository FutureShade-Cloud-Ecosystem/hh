// The TanyAI panel's pure half: the Answer to view mapping of PANEL-API r1 section 1, the section 2 fields
// the panel handles when they are present (they are omitempty on the wire, so a v1 answer keeps working),
// the section 3 stream reducer, the glossary's words for every notice and error, and the history the panel
// sends back. No DOM, no network, no Lit: everything here is a plain function a unit test can hold.
//
// Rules this file keeps (the contract, sections 1, 8 and 9):
//   * text is plain text; this module never produces markup, a link or a preview of anything;
//   * the panel never builds a URL: a ref becomes an Open button whose press is an event the host resolves;
//   * a notice is worded from the glossary table by its code and class, never from core text, and never
//     names a record; partial and stale are not notices (refused in 8.2) and render nothing;
//   * no provider, database or transport text ever reaches the screen: an error is worded from a fixed list;
//   * the history carries only what the person typed or picked, the answer's text, and ran and clarify
//     kept opaque, at most six turns; the panel adds no claim, role or id of its own.

/** The event the host shell resolves into its own route; the panel never builds a URL. */
export const TANYAI_OPEN_REF_EVENT = 'hh-tanyai-open-ref';

/** The turns the core accepts today (maxHistory in core/internal/assist/service.go). */
export const MAX_HISTORY = 6;

// ── wire shapes (structural; the generated client's types satisfy them) ─────────────────────

export interface TanyaiRef {
  kind: string;
  id: string;
  label?: string;
}

export interface TanyaiResult {
  command: string;
  headline: string;
  items: { title: string; fields: { label: string; value: string }[] }[];
  empty: boolean;
  note?: string;
  ref?: TanyaiRef;
}

export interface TanyaiClarify {
  question: string;
  options: { label: string; value: string; hint?: string }[];
  command: string;
  args: Record<string, string>;
  arg: string;
}

export interface TanyaiNotice {
  code: string;
  class?: string;
}

export interface TanyaiScope {
  branches?: { id: string; label: string }[];
  period?: { from?: string; to?: string; label?: string };
  account?: { label: string };
  as_of?: string;
}

export interface TanyaiAnswer {
  text: string;
  results: TanyaiResult[];
  limit_reached: boolean;
  usage?: { provider: string; model: string; tokens_in: number; tokens_out: number };
  ran: { command: string; args: Record<string, string> }[];
  clarify?: TanyaiClarify;
  carried?: string[];
  suggest?: string[];
  // Section 2, omitempty.
  citations?: { n: number; label: string; ref: TanyaiRef }[];
  notice?: TanyaiNotice;
  scope?: TanyaiScope;
  answer_id?: string;
  conversation_id?: string;
}

export interface TanyaiCommandInfo {
  code: string;
  title: string;
  summary?: string;
}

// ── the glossary's words (hh-s4/ux/GLOSSARY-r1.md, "TanyAI notices") ────────────────────────

const REFUSED_WORDS: Readonly<Record<string, string>> = {
  cost: 'Costs are not shown for your role. A manager can see them.',
  margin: 'Margins are not shown for your role. A manager can see them.',
  payment_trail: 'Payment details are not shown for your role. A manager can see them.',
  pii: 'Contact details are not shown for your role. A manager can see them.',
  capability: 'Your role cannot ask TanyAI about this. A manager can.',
  disabled: 'TanyAI is switched off for your role. Ask a manager.',
};

const NOTICE_WORDS: Readonly<Record<string, string>> = {
  answered_privately: 'TanyAI answered privately for the members at the time.',
  model_unavailable: "TanyAI's chat is not available right now. The quick lookups below still work.",
  too_broad: 'That question covers too much at once. Try one branch, one customer or a shorter period.',
};

const FALLBACK_NOTICE_WORDS = 'TanyAI could not answer that. Try a different question.';

/** The words for a notice, by code and class. An unknown code or class never shows itself. */
export function noticeWords(notice: TanyaiNotice): string {
  if (notice.code === 'refused') return REFUSED_WORDS[notice.class ?? ''] ?? REFUSED_WORDS['capability']!;
  return NOTICE_WORDS[notice.code] ?? FALLBACK_NOTICE_WORDS;
}

const ERROR_WORDS: Readonly<Record<string, string>> = {
  timeout: 'TanyAI took too long to answer. Try again.',
  unavailable: 'TanyAI cannot answer right now. Try again in a moment.',
  rate_limited: 'Too many questions at once. Wait a moment and ask again.',
};

export const OFFLINE_WORDS = 'TanyAI needs a connection. Your question is not saved.';

/** The words for a failure. Only the code or the status picks them: the failure's own text is never shown. */
export function errorWords(failure: { code?: string; status?: number; text?: string }): string {
  if (failure.code !== undefined && ERROR_WORDS[failure.code] !== undefined) return ERROR_WORDS[failure.code]!;
  if (failure.code === 'refused') return REFUSED_WORDS['capability']!;
  if (failure.code === 'too_broad') return NOTICE_WORDS['too_broad']!;
  if (failure.status === 429) return ERROR_WORDS['rate_limited']!;
  if (failure.status === 403) return REFUSED_WORDS['capability']!;
  if (failure.status === 504 || failure.status === 408) return ERROR_WORDS['timeout']!;
  return ERROR_WORDS['unavailable']!;
}

// ── the view ────────────────────────────────────────────────────────────────────────────────

export interface ResultView {
  command: string;
  headline: string;
  note: string | null;
  empty: boolean;
  columns: string[];
  rows: { title: string; cells: string[] }[];
  openLabel: string | null;
  ref: TanyaiRef | null;
}

export interface AnswerView {
  kind: 'answer' | 'clarify' | 'suggest' | 'over_budget' | 'chat_off';
  text: string;
  results: ResultView[];
  clarify: { question: string; options: { label: string; value: string; hint: string | null }[] } | null;
  stillAbout: string | null;
  suggestions: { code: string; label: string }[];
  /** True where the quick lookups are the way forward: over budget, chat off, model unavailable. */
  commandChips: boolean;
  notice: { code: string; tone: 'refused' | 'info'; words: string } | null;
  scope: string | null;
  citations: { n: number; label: string; openLabel: string; ref: TanyaiRef }[];
  usage: string | null;
  answerId: string | null;
}

export interface AnswerViewOptions {
  commands?: readonly TanyaiCommandInfo[];
  spendVisible?: boolean;
}

const CHAT_OFF_OPENING = "The assistant's chat is switched off";
const SHOWN_NOTICES: readonly string[] = ['refused', 'answered_privately', 'model_unavailable', 'too_broad'];

function refLabel(ref: TanyaiRef): string {
  return `Open ${ref.label !== undefined && ref.label !== '' ? ref.label : ref.kind}`;
}

function resultView(result: TanyaiResult): ResultView {
  const columns: string[] = [];
  for (const item of result.items) for (const f of item.fields) if (!columns.includes(f.label)) columns.push(f.label);
  const rows = result.items.map((item) => ({
    title: item.title,
    cells: columns.map((c) => item.fields.find((f) => f.label === c)?.value ?? ''),
  }));
  return {
    command: result.command,
    headline: result.headline,
    note: result.note !== undefined && result.note !== '' ? result.note : null,
    empty: result.empty || result.items.length === 0,
    columns,
    rows,
    openLabel: result.ref === undefined ? null : refLabel(result.ref),
    ref: result.ref ?? null,
  };
}

/** The quiet usage line, only where assist/me says this role may see spend. */
export function usageWords(usage: NonNullable<TanyaiAnswer['usage']>): string {
  return `AI runs used: ${usage.tokens_in + usage.tokens_out}`;
}

/** What the figures cover, in one line: "Point Loma, this month, as of 8:15 AM". */
export function scopeWords(scope: TanyaiScope | undefined): string | null {
  if (scope === undefined) return null;
  const parts: string[] = [];
  if (scope.branches !== undefined && scope.branches.length > 0) parts.push(scope.branches.map((b) => b.label).join(', '));
  if (scope.account !== undefined) parts.push(scope.account.label);
  if (scope.period?.label !== undefined && scope.period.label !== '') parts.push(scope.period.label);
  if (scope.as_of !== undefined) {
    // The clock the core wrote, in the dealer's own zone: read the digits, never convert to this device's zone.
    const m = /T(\d{2}):(\d{2})/.exec(scope.as_of);
    if (m !== null) {
      const h = Number(m[1]);
      parts.push(`as of ${h % 12 === 0 ? 12 : h % 12}:${m[2]} ${h < 12 ? 'AM' : 'PM'}`);
    }
  }
  return parts.length === 0 ? null : parts.join(', ');
}

/** The Answer as the panel shows it. Every field of the contract's section 1 is used; none is invented. */
export function answerView(answer: TanyaiAnswer, options: AnswerViewOptions = {}): AnswerView {
  const commands = options.commands ?? [];
  const notice = answer.notice;
  const overBudget = answer.limit_reached || notice?.code === 'over_budget';
  const chatOff = !overBudget && answer.text.startsWith(CHAT_OFF_OPENING);
  const clarify =
    answer.clarify !== undefined && answer.clarify.options.length > 0
      ? {
          question: answer.clarify.question,
          options: answer.clarify.options.map((o) => ({ label: o.label, value: o.value, hint: o.hint ?? null })),
        }
      : null;
  const suggestions = (answer.suggest ?? []).map((code) => ({
    code,
    label: commands.find((c) => c.code === code)?.title ?? 'Another lookup',
  }));
  const kind: AnswerView['kind'] = overBudget
    ? 'over_budget'
    : chatOff
      ? 'chat_off'
      : clarify !== null
        ? 'clarify'
        : suggestions.length > 0
          ? 'suggest'
          : 'answer';
  const shown = notice !== undefined && SHOWN_NOTICES.includes(notice.code) ? notice : null;
  return {
    kind,
    text: answer.text,
    results: answer.results.map(resultView),
    clarify,
    stillAbout: answer.carried !== undefined && answer.carried.length > 0 ? `Still about: ${answer.carried.join(', ')}` : null,
    suggestions,
    commandChips: kind === 'over_budget' || kind === 'chat_off' || shown?.code === 'model_unavailable',
    notice: shown === null ? null : { code: shown.code, tone: shown.code === 'refused' ? 'refused' : 'info', words: noticeWords(shown) },
    scope: scopeWords(answer.scope),
    citations: (answer.citations ?? []).map((c) => ({ n: c.n, label: c.label, openLabel: refLabel({ ...c.ref, label: c.ref.label ?? c.label }), ref: c.ref })),
    usage: options.spendVisible === true && answer.usage !== undefined ? usageWords(answer.usage) : null,
    answerId: answer.answer_id ?? null,
  };
}

// ── history ─────────────────────────────────────────────────────────────────────────────────

/** A turn as the panel keeps it. Only the keys of HistoryMessage ever leave the panel. */
export interface PanelTurn {
  role: string;
  text: string;
  ran?: { command: string; args: Record<string, string> }[];
  clarify?: TanyaiClarify;
  [extra: string]: unknown;
}

export interface HistoryMessage {
  role: 'user' | 'assistant';
  text: string;
  ran?: { command: string; args: Record<string, string> }[];
  clarify?: TanyaiClarify;
}

/** At most the newest six turns, each reduced to what the core reads. ran stays opaque. */
export function historyFor(turns: readonly PanelTurn[]): HistoryMessage[] {
  const out: HistoryMessage[] = [];
  for (const t of turns) {
    if (t.role !== 'user' && t.role !== 'assistant') continue;
    const m: HistoryMessage = { role: t.role, text: t.text };
    if (t.ran !== undefined && t.ran.length > 0) m.ran = t.ran;
    if (t.clarify !== undefined) m.clarify = t.clarify;
    out.push(m);
  }
  return out.slice(-MAX_HISTORY);
}

// ── streaming (section 3): the draft is replaced whole by done ──────────────────────────────

export interface StreamDraft {
  words: string;
  text: string;
  results: TanyaiResult[];
  done: TanyaiAnswer | null;
  error: { code: string; words: string } | null;
}

export type TanyaiStreamEvent = { event: string; data: unknown };

export function emptyDraft(): StreamDraft {
  return { words: '', text: '', results: [], done: null, error: null };
}

/** status words and error words are the core's closed lists, shown verbatim; delta is an unchecked draft. */
export function applyStreamEvent(draft: StreamDraft, ev: TanyaiStreamEvent): StreamDraft {
  const data = (ev.data ?? {}) as Record<string, unknown>;
  switch (ev.event) {
    case 'status':
      return { ...draft, words: typeof data['words'] === 'string' ? data['words'] : draft.words };
    case 'delta':
      return { ...draft, text: draft.text + (typeof data['text'] === 'string' ? data['text'] : '') };
    case 'result':
      return { ...draft, results: [...draft.results, ev.data as TanyaiResult] };
    case 'done':
      return { ...emptyDraft(), done: ev.data as TanyaiAnswer };
    case 'error': {
      const code = typeof data['code'] === 'string' ? data['code'] : 'unavailable';
      const words = typeof data['words'] === 'string' && data['words'] !== '' ? data['words'] : errorWords({ code });
      return { ...draft, error: { code, words } };
    }
    default:
      return draft;
  }
}

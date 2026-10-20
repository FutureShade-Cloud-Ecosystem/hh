// hh-tanyai-panel: the one TanyAI panel every shell and desk app mounts (PANEL-API r1 sections 1, 4 and 5).
//
// A side sheet at 1440 and 1024, a full height sheet on a phone. The host hands it a transport (the four
// staff operations, from the generated client) and opens it by calling show(); the header button, the F9
// key (the existing assist.open hotkey) and the host's own controls all end in show(). The panel emits
//
//   hh-tanyai-open-ref  { ref, source }   an "Open <record>" button was pressed; the host shell resolves
//                                         the ref to its own route (the panel never builds a URL)
//   hh-tanyai-close                       the panel closed (Escape, the close control, hide())
//
// and renders the states of section 4: closed, first run (what TanyAI can do for this role), typing,
// working (in words, with a stop), answered, clarify, suggestion only, over budget, chat off, refused,
// answered privately, offline, error with retry, long answers (the thread scrolls inside the panel and a
// results table scrolls in its card) and model unavailable (the quick lookups still work).
//
// Everything the core says goes in as text. Nothing the person did not type or pick is ever sent: the
// history is the newest six turns, reduced to role, text, ran and clarify (tanyai-view.ts). Money never
// queues and TanyAI never queues either: offline, a question is not sent and not stored.
//
// Markup follows the house delegation pattern: every control rides a data-action name and the panel's one
// click listener acts on it; the template binds values only in the positions the design system allows.

import { css, html, nothing, type PropertyValues, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import { answerStyles, renderAnswer } from './tanyai-render.js';
import type { TanyaiCommandList, TanyaiMe, TanyaiTransport } from './tanyai-transport.js';
import {
  answerView,
  applyStreamEvent,
  emptyDraft,
  errorWords,
  historyFor,
  OFFLINE_WORDS,
  TANYAI_OPEN_REF_EVENT,
  type AnswerView,
  type PanelTurn,
  type StreamDraft,
  type TanyaiAnswer,
  type TanyaiResult,
} from './tanyai-view.js';

/** The core's two fixed sentences (core/internal/assist/service.go), used only before an answer carries them. */
export const LIMIT_REACHED_WORDS =
  "You've used this month's AI allowance, ask your admin if you need more. You can still look things up with the buttons below; those don't use any AI.";
export const CHAT_OFF_WORDS = "The assistant's chat is switched off right now. You can still look things up with the buttons below.";

const WORKING_WORDS = 'TanyAI is checking what your role can see.';

type Phase = 'idle' | 'working' | 'error' | 'offline';

interface Turn extends PanelTurn {
  label?: string;
  view?: AnswerView;
}

interface Action {
  kind: 'chat' | 'command';
  question: string;
  label: string;
  command?: string;
  args?: Record<string, string>;
}

interface CommandCard {
  code: string;
  title: string;
  summary: string;
  args: { name: string; label: string; example: string; required: boolean }[];
}

/** The focused element, looking through open shadow roots, so focus can be handed back to the control that opened us. */
function deepActive(): HTMLElement | null {
  let el = document.activeElement as HTMLElement | null;
  while (el?.shadowRoot?.activeElement) el = el.shadowRoot.activeElement as HTMLElement;
  return el;
}

const styles = css`
  :host {
    display: none;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  :host([open]) { display: block; }
  * { box-sizing: border-box; }

  .sheet {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: var(--hh-z-overlay);
    display: flex;
    flex-direction: column;
    width: min(440px, 100vw);
    height: 100vh;
    height: 100dvh;
    background: var(--hh-page);
    color: var(--hh-text);
    border-left: 1px solid var(--hh-line);
    box-shadow: var(--hh-elev-3);
    padding-bottom: var(--hh-safe-bottom);
  }
  @media (max-width: 1100px) { .sheet { width: min(400px, 100vw); } }
  @media (max-width: 599px) {
    .sheet { width: 100vw; border-left: none; }
  }
  @media (prefers-reduced-motion: no-preference) {
    .sheet { animation: slide-in 160ms ease-out; }
    @keyframes slide-in { from { transform: translateX(24px); opacity: 0; } to { transform: none; opacity: 1; } }
  }

  header {
    display: flex;
    align-items: center;
    gap: var(--hh-s-8);
    padding: var(--hh-s-8) var(--hh-s-16);
    min-height: 56px;
    background: var(--hh-card);
    border-bottom: 1px solid var(--hh-line);
  }
  h2 { margin: 0; font-size: 16px; font-weight: 700; flex: 1; min-width: 0; }
  h2:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .head-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 44px;
    min-height: 44px;
    padding: 0 var(--hh-s-8);
    background: transparent;
    color: var(--hh-text-dim);
    border: 1px solid transparent;
    border-radius: var(--hh-radius-control);
    font-family: inherit;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
  }
  .head-btn:hover { color: var(--hh-text); border-color: var(--hh-line); }
  .head-btn:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .head-btn svg { width: 18px; height: 18px; display: block; }

  .thread {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overscroll-behavior: contain;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-16);
    padding: var(--hh-s-16);
  }
  .thread:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }

  .turn-user { align-self: flex-end; max-width: 88%; }
  .bubble {
    margin: 0;
    padding: var(--hh-s-8) var(--hh-s-12);
    background: var(--hh-field);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    font-size: 14px;
    line-height: 1.4;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .turn-assistant { min-width: 0; }

  .intro { display: flex; flex-direction: column; gap: var(--hh-s-12); }
  .intro h3 { margin: 0; font-size: 16px; font-weight: 700; }
  .intro p { margin: 0; font-size: 14px; line-height: 1.45; color: var(--hh-text-dim); }
  .lookups { display: flex; flex-direction: column; gap: var(--hh-s-8); margin: 0; padding: 0; list-style: none; }
  .lookup {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hh-s-2);
    width: 100%;
    min-height: 44px;
    padding: var(--hh-s-8) var(--hh-s-12);
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    font-family: inherit;
    text-align: left;
    cursor: pointer;
  }
  .lookup:hover { border-color: var(--hh-accent-text); }
  .lookup:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .lookup .t { font-size: 14px; font-weight: 600; }
  .lookup .s { font-size: var(--hh-s-12); color: var(--hh-text-dim); }
  .skeleton { height: 44px; border-radius: var(--hh-radius-control); background: var(--hh-field); border: 1px solid var(--hh-line); }

  .banner {
    margin: 0;
    padding: var(--hh-s-12);
    background: var(--hh-field);
    border: 1px solid var(--hh-line);
    border-left-width: 4px;
    border-radius: var(--hh-radius-control);
    font-size: 14px;
    line-height: 1.4;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    align-items: flex-start;
  }
  .banner[data-kind='error'], .banner[data-kind='offline'] { border-left-color: var(--hh-danger); }
  .banner[data-kind='limit'], .banner[data-kind='off'] { border-left-color: var(--hh-state-watch); }
  .banner p { margin: 0; }
  .working { display: flex; align-items: center; gap: var(--hh-s-8); font-size: 14px; color: var(--hh-text-dim); }
  .dot { width: 8px; height: 8px; border-radius: var(--hh-radius-pill); background: var(--hh-accent-text); flex: none; }
  @media (prefers-reduced-motion: no-preference) {
    .dot { animation: pulse 1.1s ease-in-out infinite; }
    @keyframes pulse { 50% { opacity: 0.3; } }
  }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 44px;
    padding: 0 var(--hh-s-16);
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    font-family: inherit;
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) { border-color: var(--hh-accent-text); }
  .btn:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .btn.primary { background: var(--hh-accent); color: var(--hh-accent-ink); border-color: var(--hh-accent); }
  .btn.primary:hover:not(:disabled) { background: var(--hh-accent-hover); }
  .btn:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
    cursor: not-allowed;
  }

  .form { display: flex; flex-direction: column; gap: var(--hh-s-12); padding: var(--hh-s-12); background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); }
  .form h3 { margin: 0; font-size: 14px; font-weight: 700; }
  .field { display: flex; flex-direction: column; gap: var(--hh-s-4); font-size: 13px; font-weight: 600; }
  .field .hint { font-weight: 400; font-size: var(--hh-s-12); color: var(--hh-text-dim); }
  .form-actions { display: flex; gap: var(--hh-s-8); }

  input, textarea {
    width: 100%;
    min-height: 44px;
    padding: var(--hh-s-8) var(--hh-s-12);
    background: var(--hh-field);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    font-family: inherit;
    font-size: 14px;
    line-height: 1.4;
  }
  input:focus-visible, textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 1px; }
  textarea:disabled { background: var(--hh-disabled-fill); color: var(--hh-disabled-ink); border-color: var(--hh-disabled-line); }

  .composer {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-12) var(--hh-s-16);
    background: var(--hh-card);
    border-top: 1px solid var(--hh-line);
  }
  .composer-row { display: flex; gap: var(--hh-s-8); align-items: flex-end; }
  textarea { resize: none; max-height: 120px; flex: 1; }
  .composer .quiet { margin: 0; font-size: var(--hh-s-12); color: var(--hh-text-dim); }
`;

export class ShellTanyai extends HHElement {
  static styles = [baseStyles, answerStyles, styles];

  static properties = {
    ...HHElement.properties,
    open: { type: Boolean, reflect: true },
    turns: { state: true },
    phase: { state: true },
    working: { state: true },
    me: { state: true },
    commandList: { state: true },
    loadFailed: { state: true },
    form: { state: true },
    draftLength: { state: true },
    online: { state: true },
  };

  /** Whether the panel is showing. show() and hide() are the doors; a host may also set the attribute. */
  declare open: boolean;
  /** The seam to the core. The host sets it once; without one the panel says it is not available. */
  declare transport: TanyaiTransport | null;
  /** The record kinds the host can open. Null means every kind; a kind not listed gets no Open button. */
  declare openableKinds: readonly string[] | null;
  declare hotkey: boolean;

  declare turns: Turn[];
  declare phase: Phase;
  declare working: string;
  declare me: TanyaiMe | null;
  declare commandList: TanyaiCommandList | null;
  declare loadFailed: boolean;
  declare form: CommandCard | null;
  declare draftLength: number;
  declare online: boolean;

  #opener: HTMLElement | null = null;
  #action: Action | null = null;
  #abort: AbortController | null = null;
  #scrollTo = -1;

  constructor() {
    super();
    this.open = false;
    this.transport = null;
    this.openableKinds = null;
    this.hotkey = true;
    this.turns = [];
    this.phase = 'idle';
    this.working = WORKING_WORDS;
    this.me = null;
    this.commandList = null;
    this.loadFailed = false;
    this.form = null;
    this.draftLength = 0;
    this.online = true;
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener('click', this.#onClick);
    this.addEventListener('keydown', this.#onKeyDown);
    this.addEventListener('input', this.#onInput);
    if (typeof document !== 'undefined') document.addEventListener('keydown', this.#onDocKeyDown);
    if (typeof window !== 'undefined') {
      window.addEventListener('online', this.#onNet);
      window.addEventListener('offline', this.#onNet);
      this.online = navigator.onLine;
    }
  }

  override disconnectedCallback(): void {
    this.removeEventListener('click', this.#onClick);
    this.removeEventListener('keydown', this.#onKeyDown);
    this.removeEventListener('input', this.#onInput);
    if (typeof document !== 'undefined') document.removeEventListener('keydown', this.#onDocKeyDown);
    if (typeof window !== 'undefined') {
      window.removeEventListener('online', this.#onNet);
      window.removeEventListener('offline', this.#onNet);
    }
    this.#abort?.abort();
    super.disconnectedCallback();
  }

  // ── doors ─────────────────────────────────────────────────────────────────────────────────

  /** Opens the panel, remembers what had focus, and moves focus in. */
  show(): void {
    if (this.open) return;
    this.#opener = deepActive();
    this.open = true;
    this.online = navigator.onLine;
    void this.#loadIntro();
    void this.updateComplete.then(() => this.#focusIn());
  }

  /** Closes the panel and hands focus back to the control that opened it. Idempotent. */
  hide(): void {
    if (!this.open) return;
    this.open = false;
    const back = this.#opener;
    this.#opener = null;
    this.dispatchEvent(new CustomEvent('hh-tanyai-close', { bubbles: true, composed: true }));
    if (back?.isConnected) back.focus();
  }

  toggle(): void {
    if (this.open) this.hide();
    else this.show();
  }

  // ── loading what TanyAI can do for this role ────────────────────────────────────────────────

  async #loadIntro(): Promise<void> {
    const t = this.transport;
    if (t === null) {
      this.loadFailed = true;
      return;
    }
    if (this.me !== null && this.commandList !== null) return;
    this.loadFailed = false;
    const [me, commands] = await Promise.all([t.me(), t.commands()]);
    if (me.ok) this.me = me.value;
    if (commands.ok) this.commandList = commands.value;
    this.loadFailed = !me.ok || !commands.ok;
  }

  // ── events ────────────────────────────────────────────────────────────────────────────────

  readonly #onNet = (): void => {
    this.online = navigator.onLine;
    if (this.online && this.phase === 'offline') this.phase = 'idle';
  };

  readonly #onInput = (event: Event): void => {
    const target = event.composedPath()[0];
    if (target instanceof HTMLTextAreaElement) this.draftLength = target.value.trim().length;
  };

  readonly #onDocKeyDown = (event: KeyboardEvent): void => {
    if (!this.hotkey || event.key !== 'F9' || event.defaultPrevented) return;
    event.preventDefault();
    this.toggle();
  };

  readonly #onKeyDown = (event: Event): void => {
    const e = event as KeyboardEvent;
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      this.hide();
      return;
    }
    const target = e.composedPath()[0];
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      if (target instanceof HTMLTextAreaElement) {
        e.preventDefault();
        void this.#sendDraft();
      } else if (target instanceof HTMLInputElement) {
        e.preventDefault();
        void this.#runForm();
      }
      return;
    }
    if (e.key === 'Tab' && window.matchMedia('(max-width: 599px)').matches) this.#trapTab(e);
  };

  /** On a phone the sheet covers the app, so Tab cycles inside it. */
  #trapTab(e: KeyboardEvent): void {
    const items = [...this.renderRoot.querySelectorAll<HTMLElement>('button, textarea, input, [tabindex="0"]')].filter(
      (el) => !(el as HTMLButtonElement).disabled,
    );
    if (items.length === 0) return;
    const first = items[0]!;
    const last = items[items.length - 1]!;
    const active = this.renderRoot instanceof ShadowRoot ? (this.renderRoot.activeElement as HTMLElement | null) : null;
    if (e.shiftKey && active === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }

  readonly #onClick = (event: Event): void => {
    const control = event
      .composedPath()
      .find((n): n is Element => n instanceof Element && n.hasAttribute('data-action'));
    if (control === undefined) return;
    // The panel owns its clicks: a host that delegates on data-action names must not see ours.
    event.stopPropagation();
    const num = (name: string): number => Number(control.getAttribute(name) ?? '-1');
    switch (control.getAttribute('data-action')) {
      case 'close':
        return this.hide();
      case 'reset':
        return this.#reset();
      case 'send':
        return void this.#sendDraft();
      case 'stop':
        return this.#stop();
      case 'retry':
        return void this.#retry();
      case 'retry-intro':
        this.me = null;
        this.commandList = null;
        return void this.#loadIntro();
      case 'pick':
        return this.#pick(num('data-turn'), num('data-option'));
      case 'run-command':
        return this.#chooseCommand(control.getAttribute('data-command') ?? '');
      case 'form-run':
        return void this.#runForm();
      case 'form-cancel':
        this.form = null;
        return void this.updateComplete.then(() => this.#focusIn());
      case 'open-ref':
        return this.#openRef(num('data-turn'), num('data-result'), 'result');
      case 'open-citation':
        return this.#openRef(num('data-turn'), num('data-citation'), 'citation');
      default:
    }
  };

  #openRef(turn: number, index: number, source: 'result' | 'citation'): void {
    const view = this.turns[turn]?.view;
    const ref = source === 'result' ? view?.results[index]?.ref : view?.citations[index]?.ref;
    if (ref === undefined || ref === null) return;
    this.dispatchEvent(new CustomEvent(TANYAI_OPEN_REF_EVENT, { detail: { ref, source }, bubbles: true, composed: true }));
  }

  // ── asking ────────────────────────────────────────────────────────────────────────────────

  get #textarea(): HTMLTextAreaElement | null {
    return this.renderRoot.querySelector<HTMLTextAreaElement>('textarea');
  }

  get #composerOff(): boolean {
    return this.phase === 'working' || this.#chatOff || this.#overBudget || !this.online;
  }

  get #chatOff(): boolean {
    return this.me?.chat_enabled === false || this.commandList?.chat_enabled === false;
  }

  get #overBudget(): boolean {
    return this.me?.over_budget === true;
  }

  async #sendDraft(): Promise<void> {
    const ta = this.#textarea;
    if (ta === null || this.#composerOff) return;
    const question = ta.value.trim();
    if (question === '') return;
    if (!navigator.onLine) {
      this.online = false;
      this.phase = 'offline';
      return;
    }
    ta.value = '';
    this.draftLength = 0;
    await this.#perform({ kind: 'chat', question, label: question }, true);
  }

  #pick(turn: number, option: number): void {
    if (this.phase === 'working') return;
    const choice = this.turns[turn]?.clarify?.options[option];
    if (choice === undefined) return;
    void this.#perform({ kind: 'chat', question: choice.value, label: choice.label }, true);
  }

  #chooseCommand(code: string): void {
    if (this.phase === 'working') return;
    const info = this.commandList?.commands.find((c) => c.code === code);
    const title = info?.title ?? 'Another lookup';
    const args = (info as { args?: CommandCard['args'] } | undefined)?.args ?? [];
    if (args.some((a) => a.required)) {
      this.form = { code, title, summary: info?.summary ?? '', args };
      void this.updateComplete.then(() => this.renderRoot.querySelector<HTMLInputElement>('.form input')?.focus());
      return;
    }
    void this.#perform({ kind: 'command', question: title, label: title, command: code, args: {} }, true);
  }

  async #runForm(): Promise<void> {
    const form = this.form;
    if (form === null) return;
    const args: Record<string, string> = {};
    for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('.form input')) {
      const name = input.getAttribute('data-arg') ?? '';
      if (input.value.trim() !== '') args[name] = input.value.trim();
    }
    if (form.args.some((a) => a.required && args[a.name] === undefined)) {
      const empty = [...this.renderRoot.querySelectorAll<HTMLInputElement>('.form input')].find((i) => i.value.trim() === '');
      empty?.focus();
      return;
    }
    this.form = null;
    await this.#perform({ kind: 'command', question: form.title, label: form.title, command: form.code, args }, true);
  }

  #reset(): void {
    this.#abort?.abort();
    this.turns = [];
    this.phase = 'idle';
    this.form = null;
    this.#action = null;
    void this.updateComplete.then(() => this.#focusIn());
  }

  #stop(): void {
    this.#abort?.abort();
  }

  async #retry(): Promise<void> {
    const a = this.#action;
    if (a === null) return;
    if (!navigator.onLine) {
      this.online = false;
      this.phase = 'offline';
      return;
    }
    await this.#perform(a, false);
  }

  /** One question or one command, through the transport. `fresh` adds the person's turn; a retry reuses it. */
  async #perform(action: Action, fresh: boolean): Promise<void> {
    const t = this.transport;
    if (t === null) return;
    this.#action = action;
    const history = historyFor(this.turns.filter((turn) => !(fresh === false && turn === this.turns[this.turns.length - 1])));
    if (fresh) this.turns = [...this.turns, { role: 'user', text: action.question, label: action.label }];
    this.phase = 'working';
    this.working = WORKING_WORDS;
    const abort = new AbortController();
    this.#abort = abort;
    let failure: { status?: number; code?: string } | null = null;
    let answer: TanyaiAnswer | null = null;

    if (action.kind === 'command') {
      const r = await t.run(action.command ?? '', action.args ?? {}, abort.signal);
      if (r.ok) answer = this.#commandAnswer(action, r.value);
      else failure = { status: r.status };
    } else if (t.chatStream !== undefined) {
      let draft: StreamDraft = emptyDraft();
      try {
        for await (const ev of t.chatStream({ question: action.question, history }, abort.signal)) {
          draft = applyStreamEvent(draft, ev);
          if (draft.words !== '') this.working = draft.words;
          if (draft.done !== null || draft.error !== null) break;
        }
      } catch {
        failure = {};
      }
      if (draft.done !== null) answer = draft.done;
      else if (draft.error !== null) failure = { code: draft.error.code };
      else failure ??= {};
    } else {
      const r = await t.chat({ question: action.question, history }, abort.signal);
      if (r.ok) answer = r.value;
      else failure = { status: r.status };
    }

    if (this.#abort !== abort) return;
    this.#abort = null;
    if (abort.signal.aborted && answer === null) {
      // Stopped by the person: nothing was answered, the question goes back into the box.
      this.#dropLastUser(action);
      this.phase = 'idle';
      return;
    }
    if (answer !== null) {
      this.#land(answer);
      return;
    }
    if (!navigator.onLine) {
      // TanyAI never queues: the question is not kept anywhere but the box it was typed in.
      this.online = false;
      this.#dropLastUser(action);
      this.phase = 'offline';
      return;
    }
    this.working = errorWords(failure ?? {});
    this.phase = 'error';
  }

  #commandAnswer(action: Action, result: TanyaiResult): TanyaiAnswer {
    return { text: '', results: [result], limit_reached: false, ran: [{ command: action.command ?? '', args: action.args ?? {} }] };
  }

  #land(answer: TanyaiAnswer): void {
    const view = answerView(answer, {
      commands: this.commandList?.commands ?? [],
      spendVisible: this.me?.spend_visible === true,
    });
    const turn: Turn = { role: 'assistant', text: answer.text, ran: answer.ran, view };
    if (answer.clarify !== undefined) turn.clarify = answer.clarify;
    this.turns = [...this.turns, turn];
    this.#scrollTo = this.turns.length - 1;
    this.phase = 'idle';
    this.#action = null;
  }

  /** Takes the person's last turn off the thread and puts their words back in the box (stopped, or offline). */
  #dropLastUser(action: Action): void {
    const last = this.turns[this.turns.length - 1];
    if (last?.role === 'user') this.turns = this.turns.slice(0, -1);
    if (action.kind === 'chat') {
      void this.updateComplete.then(() => {
        const ta = this.#textarea;
        if (ta !== null && ta.value === '') {
          ta.value = action.question;
          this.draftLength = ta.value.trim().length;
        }
      });
    }
  }

  // ── focus and scroll ────────────────────────────────────────────────────────────────────────

  #focusIn(): void {
    const ta = this.#textarea;
    if (ta !== null && !ta.disabled) ta.focus();
    else this.renderRoot.querySelector<HTMLElement>('h2')?.focus();
  }

  protected override updated(changed: PropertyValues): void {
    super.updated(changed);
    if (this.#scrollTo >= 0) {
      const target = this.renderRoot.querySelector<HTMLElement>(`[data-turn-index="${this.#scrollTo}"]`);
      this.#scrollTo = -1;
      target?.scrollIntoView({ block: 'start' });
      if (!this.#composerOff) this.#textarea?.focus({ preventScroll: true });
    }
    if (changed.has('phase') && (this.phase === 'error' || this.phase === 'offline')) {
      this.renderRoot.querySelector<HTMLElement>('.banner[role="alert"] .btn')?.focus({ preventScroll: true });
    }
  }

  // ── render ────────────────────────────────────────────────────────────────────────────────

  render(): Template {
    if (!this.open) return html``;
    return html`
      <aside class="sheet" role="dialog" aria-modal="false" aria-labelledby="tanyai-title">
        <header>
          <h2 id="tanyai-title" tabindex="-1">TanyAI</h2>
          ${this.turns.length > 0
            ? html`<button type="button" class="head-btn" data-action="reset" aria-label="Start a new conversation">New conversation</button>`
            : nothing}
          <button type="button" class="head-btn" data-action="close" aria-label="Close TanyAI">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18"></path>
            </svg>
          </button>
        </header>
        <div class="thread" role="log" aria-live="polite" aria-label="Conversation with TanyAI" tabindex="0">
          ${this.#renderBody()}
        </div>
        ${this.#renderComposer()}
      </aside>
    `;
  }

  #renderBody(): Template {
    const canOpen = (kind: string): boolean => this.openableKinds === null || this.openableKinds.includes(kind);
    return html`
      ${this.turns.length === 0 ? this.#renderFirstRun() : nothing}
      ${this.turns.map((turn, i) =>
        turn.role === 'user'
          ? html`
              <div class="turn-user" data-turn-index=${String(i)}>
                <h3 class="sr">You asked</h3>
                <p class="bubble">${turn.label ?? turn.text}</p>
              </div>
            `
          : html`
              <div class="turn-assistant" data-turn-index=${String(i)}>
                <h3 class="sr">TanyAI answered</h3>
                ${turn.view !== undefined ? renderAnswer(turn.view, i, canOpen) : nothing}
                ${turn.view?.commandChips === true && i === this.turns.length - 1 ? this.#renderLookups() : nothing}
              </div>
            `,
      )}
      ${this.form !== null ? this.#renderForm(this.form) : nothing}
      ${this.phase === 'working'
        ? html`
            <div class="working" role="status">
              <span class="dot" aria-hidden="true"></span>
              <span>${this.working}</span>
              <button type="button" class="btn" data-action="stop">Stop</button>
            </div>
          `
        : nothing}
      ${this.phase === 'error'
        ? html`
            <div class="banner" role="alert" data-kind="error">
              <p>${this.working}</p>
              <button type="button" class="btn" data-action="retry">Try again</button>
            </div>
          `
        : nothing}
      ${this.phase === 'offline' || !this.online
        ? html`
            <div class="banner" role="alert" data-kind="offline">
              <p>${OFFLINE_WORDS}</p>
            </div>
          `
        : nothing}
    `;
  }

  #renderFirstRun(): Template {
    if (this.transport === null || (this.loadFailed && this.commandList === null)) {
      return html`
        <div class="banner" role="alert" data-kind="error">
          <p>${errorWords({})}</p>
          <button type="button" class="btn" data-action="retry-intro">Try again</button>
        </div>
      `;
    }
    if (this.commandList === null) {
      return html`
        <div class="intro" role="status" aria-label="Loading what TanyAI can do for you">
          <div class="skeleton"></div><div class="skeleton"></div><div class="skeleton"></div>
        </div>
      `;
    }
    return html`
      <div class="intro">
        <h3>Ask about your day</h3>
        ${this.#overBudget
          ? html`<div class="banner" data-kind="limit"><p>${LIMIT_REACHED_WORDS}</p></div>`
          : this.#chatOff
            ? html`<div class="banner" data-kind="off"><p>${CHAT_OFF_WORDS}</p></div>`
            : html`<p>TanyAI answers only what your role may see, and says what its answer covers. Ask in your own words, or pick a lookup.</p>`}
        ${this.#renderLookups()}
      </div>
    `;
  }

  #renderLookups(): Template {
    const commands = this.commandList?.commands ?? [];
    if (commands.length === 0) return html``;
    return html`
      <ul class="lookups" aria-label="Quick lookups">
        ${commands.map(
          (c) => html`
            <li>
              <button type="button" class="lookup" data-action="run-command" data-command=${c.code}>
                <span class="t">${c.title}</span>
                ${c.summary !== '' ? html`<span class="s">${c.summary}</span>` : nothing}
              </button>
            </li>
          `,
        )}
      </ul>
    `;
  }

  #renderForm(form: CommandCard): Template {
    return html`
      <div class="form" role="group" aria-label=${form.title}>
        <h3>${form.title}</h3>
        ${form.args.map(
          (a) => html`
            <label class="field">
              <span>${a.label}${a.required ? '' : ' (optional)'}</span>
              <input type="text" autocomplete="off" data-arg=${a.name} />
              <span class="hint">For example ${a.example}</span>
            </label>
          `,
        )}
        <div class="form-actions">
          <button type="button" class="btn primary" data-action="form-run">Look it up</button>
          <button type="button" class="btn" data-action="form-cancel">Cancel</button>
        </div>
      </div>
    `;
  }

  #renderComposer(): Template {
    return html`
      <div class="composer">
        <label class="sr" for="tanyai-ask">Ask TanyAI</label>
        <div class="composer-row">
          <textarea id="tanyai-ask" rows="1" maxlength="1000" ?disabled=${this.#composerOff}></textarea>
          <button type="button" class="btn primary" data-action="send" ?disabled=${this.#composerOff || this.draftLength === 0}>Ask</button>
        </div>
        <p class="quiet">Enter to ask. Shift and Enter for a new line. F9 opens or closes TanyAI.</p>
      </div>
    `;
  }
}

defineElement('hh-tanyai-panel', ShellTanyai);

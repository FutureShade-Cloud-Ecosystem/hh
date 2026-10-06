// The shared feedback widget (Feature Polish, decision log row 958): the small
// pill every staff-facing app carries, and the panel it opens. One component,
// three mounts (the front door, the Counter Till, the Quote Desk), never the
// customer's public desk.
//
// THE PILL. A fixed bottom-right pill in the badge gold: 38px tall, 22px off
// the edges, matching the lane the ported till already reserves for it (its
// --hos-fab-inset and --hos-fab-size tokens resolve to exactly these numbers,
// and pos-hotkey-legend.ts keeps its dismiss control off the right-hand end
// for this pill). A mount that pins a bar across the foot (the till's hotkey
// legend, 35px) sets the lift attribute and the pill sits wholly above that
// bar by position, never by a stacking race. It never covers a working
// control: the front door's picker and the Quote Desk's chrome end well above
// the corner. The numbers are literals
// here rather than var() reads of the shared POS kit's tokens because this
// package's phantom gate holds every var() to a token defined in its own
// sheet; the comment beside each literal names the twin it must move with.
//
// THE PANEL. The kind in plain words (Something broke, Polish idea, Flag for
// later, mapped to the server's bug, feedback, flag), the note, "Capture this
// screen" (an in-page capture of the current view with the widget itself
// hidden from the shot, through the vendored library beside this file, no
// CDN), paste, drag or pick an image, up to three thumbnails each removable.
// Send hands the submission to the app's own sender and answers with the work
// item's number; a failure keeps the note and the images and lets the user
// retry.
//
// NO PILL. A host that opens the widget from its own button mounts it with
// trigger="external": the pill is hidden and the host calls show() on the
// element. The panel sits in the same corner lane either way.
//
// THE SENDER SEAM. The widget never knows the wire: the mounting app injects
// a sender (its own API client, its own credential), so the design system
// gains no transport and no token handling. A missing sender is legal and the
// Send button says so: a mount that cannot send still collects the note.
//
// THE BINDING DISCIPLINE. This package's position gate refuses event and
// property bindings outright, so the widget rides the host delegation rule:
// clicks, keys, input, change and the drag family are heard on the HOST and
// read off composedPath(); the note's value and the thumbnails' pixels are
// placed imperatively in updated(), the textarea by name and each thumbnail
// painted onto its canvas from its blob. No address is ever bound in a
// template: the thumbnails are canvases, not img elements, so an object URL
// never becomes markup.
import { css, html, nothing, type PropertyValues, type TemplateResult as Template } from 'lit';
import { defineElement, HHElement } from './base.js';
import { baseStyles } from './primitives.js';

/** One screenshot the submission carries, as the app's sender will post it. */
export interface FeedbackImage {
  readonly blob: Blob;
  readonly name: string;
}

/** What the widget hands the app's sender on Send. */
export interface FeedbackSendRequest {
  readonly type: 'bug' | 'feedback' | 'flag';
  readonly message: string;
  readonly screen: string;
  readonly url: string;
  readonly userAgent: string;
  readonly images: readonly FeedbackImage[];
}

/** The sender's answer: the work item's number on success, words on failure. */
export type FeedbackSendResult =
  | { readonly ok: true; readonly issueNumber: string }
  | { readonly ok: false; readonly error: string };

/** The seam the mounting app injects; the widget owns everything else. */
export type FeedbackSender = (request: FeedbackSendRequest) => Promise<FeedbackSendResult>;

/** The kinds in plain words, mapped to the server's bug, feedback, flag. */
const KINDS: ReadonlyArray<{ readonly id: 'bug' | 'feedback' | 'flag'; readonly label: string }> = [
  { id: 'bug', label: 'Something broke' },
  { id: 'feedback', label: 'Polish idea' },
  { id: 'flag', label: 'Flag for later' },
];

/** The most screenshots one submission carries; the server enforces the same. */
const MAX_IMAGES = 3;

/** One screenshot's byte bound; the server enforces the same. */
const MAX_IMAGE_BYTES = 5 << 20;

/** The image types the server's allow-list accepts. */
const IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/webp'];

/** One thumbnail: the blob, its file name, and the canvases it has painted. */
interface Thumb {
  readonly blob: Blob;
  readonly name: string;
}

/** The page's opaque background, so a capture never rides on transparency:
 * the body's own colour when it paints one, else the document element's, else
 * the light sheet's page white (a transparent capture renders black in some
 * viewers, which is not what the screen looked like). */
function pageBackground(): string {
  for (const element of [document.body, document.documentElement]) {
    if (element === null) continue;
    const colour = getComputedStyle(element).backgroundColor;
    // rgb(r, g, b) and rgba(r, g, b, a): opaque when the alpha is absent or 1.
    const alpha = /rgba?\([^)]*,\s*([\d.]+)\s*\)$/.exec(colour);
    if (alpha === null || Number(alpha[1]) >= 1) return colour;
  }
  return '#ffffff';
}

export class HHFeedback extends HHElement {
  static styles = [baseStyles, css`
    :host {
      /* The host is the mounting point only; the pill and the panel position
         themselves against the viewport. */
      display: block;
      width: 0;
      height: 0;
    }

    .pill {
      position: fixed;
      /* 22px: the lane the ported till reserved (--hos-fab-inset). */
      right: 22px;
      bottom: calc(22px + var(--hh-safe-bottom));
      z-index: 80;
      display: inline-flex;
      align-items: center;
      gap: var(--hh-s-8);
      /* 38px: the pill the ported till sized (--hos-fab-size). */
      height: 38px;
      min-height: 38px;
      padding: 0 var(--hh-s-16);
      border-radius: var(--hh-radius-pill);
      border: 1px solid var(--hh-accent-ink);
      /* The badge gold as a FILL, the near-black ink it always carries. */
      background: var(--hh-accent);
      color: var(--hh-accent-ink);
      font-size: var(--hh-s-12);
      font-weight: 600;
      box-shadow: 0 2px 10px rgb(0 0 0 / 0.25);
      cursor: pointer;
    }
    /* The till's pinned hotkey legend is 35px tall across the foot, and a bar
       painted later than the pill would cover it. A mount that carries a
       bottom bar sets the lift attribute and the pill sits wholly above the
       bar by position: 22px + 26px = 48px, 13px clear of the 35px legend
       (the twin is --hos-fab-lift in the POS kit's tokens). */
    :host([lift]) .pill {
      bottom: calc(22px + 26px + var(--hh-safe-bottom));
    }
    /* A host that opens the widget from its own button mounts it with
       trigger="external": no floating pill, the panel opens by show(). */
    :host([trigger='external']) .pill { display: none; }
    .pill:focus-visible {
      outline: 2px solid var(--hh-focus-ring);
      outline-offset: 2px;
    }
    .pill:hover { background: var(--hh-accent-hover); }

    .panel {
      position: fixed;
      right: 22px;
      bottom: calc(22px + 38px + var(--hh-s-12) + var(--hh-safe-bottom));
      z-index: 80;
      width: min(420px, calc(100vw - 44px));
      max-height: calc(100vh - 100px - var(--hh-safe-bottom));
      overflow-y: auto;
      background: var(--hh-card);
      color: var(--hh-text);
      border: 1px solid var(--hh-control-line);
      border-radius: var(--hh-radius-card);
      padding: var(--hh-s-16);
      display: grid;
      gap: var(--hh-s-12);
      box-shadow: 0 12px 40px rgb(0 0 0 / 0.35);
    }
    :host([lift]) .panel {
      bottom: calc(22px + 26px + 38px + var(--hh-s-12) + var(--hh-safe-bottom));
      max-height: calc(100vh - 126px - var(--hh-safe-bottom));
    }

    .head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--hh-s-8);
    }
    h2 {
      margin: 0;
      font-size: var(--hh-s-16);
      color: var(--hh-text);
    }
    .close {
      min-height: 32px;
      padding: 0 var(--hh-s-8);
      font-size: var(--hh-s-12);
    }

    .kinds {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: var(--hh-s-8);
    }
    .kind {
      min-height: 44px;
      padding: 0 var(--hh-s-4);
      font-size: var(--hh-s-12);
      background: var(--hh-card);
      color: var(--hh-text);
      border: 1px solid var(--hh-control-line);
      border-radius: var(--hh-radius-control);
      cursor: pointer;
    }
    .kind[aria-pressed='true'] {
      border-color: var(--hh-accent-text);
      color: var(--hh-accent-text);
      font-weight: 600;
    }

    textarea {
      font-family: var(--hh-font-ui);
      font-size: var(--hh-s-12);
      line-height: 1.5;
      color: var(--hh-text);
      background: var(--hh-card);
      border: 1px solid var(--hh-control-line);
      border-radius: var(--hh-radius-control);
      padding: var(--hh-s-12);
      min-height: 108px;
      resize: vertical;
      width: 100%;
    }
    textarea:focus-visible {
      outline: 2px solid var(--hh-focus-ring);
      outline-offset: 2px;
    }

    .tools {
      display: flex;
      flex-wrap: wrap;
      gap: var(--hh-s-8);
    }
    .tool {
      min-height: 38px;
      padding: 0 var(--hh-s-12);
      font-size: var(--hh-s-12);
    }

    .thumbs {
      display: flex;
      flex-wrap: wrap;
      gap: var(--hh-s-8);
      list-style: none;
      margin: 0;
      padding: 0;
    }
    .thumb {
      position: relative;
      width: 86px;
      height: 64px;
      border: 1px solid var(--hh-control-line);
      border-radius: var(--hh-radius-control);
      overflow: hidden;
      background: var(--hh-page);
    }
    .thumb canvas {
      width: 100%;
      height: 100%;
      display: block;
    }
    .thumb button {
      position: absolute;
      top: 2px;
      right: 2px;
      width: 22px;
      height: 22px;
      min-height: 22px;
      padding: 0;
      border-radius: var(--hh-radius-pill);
      border: 1px solid var(--hh-control-line);
      background: var(--hh-card);
      color: var(--hh-text);
      font-size: var(--hh-s-12);
      line-height: 1;
      cursor: pointer;
    }
    .thumb button:focus-visible {
      outline: 2px solid var(--hh-focus-ring);
      outline-offset: 1px;
    }

    .drop {
      margin: 0;
      border: 1px dashed var(--hh-control-line);
      border-radius: var(--hh-radius-control);
      padding: var(--hh-s-8) var(--hh-s-12);
      color: var(--hh-text-dim);
      font-size: var(--hh-s-12);
    }
    .drop.over {
      border-color: var(--hh-accent-text);
      color: var(--hh-accent-text);
    }

    .send {
      justify-self: start;
    }
    .status {
      margin: 0;
      font-size: var(--hh-s-12);
      color: var(--hh-text-dim);
    }
    .status[data-tone='error'] { color: var(--hh-state-stop); }
    .status[data-tone='sent'] { color: var(--hh-state-good); }
  `];

  static properties = {
    ...HHElement.properties,
    screen: { attribute: 'screen' },
    send: { attribute: false },
    open: { type: Boolean, state: true },
  };

  /** The screen's plain name, stamped on the submission and the work item. */
  declare screen: string;
  /** The mounting app's sender; a mount without one still collects the note. */
  declare send: FeedbackSender | null;
  /** Whether the panel is open; the pill is always rendered. */
  declare open: boolean;

  #kind: 'bug' | 'feedback' | 'flag' = 'bug';
  #note = '';
  #thumbs: Thumb[] = [];
  #sending = false;
  #sentNumber: string | null = null;
  #error = '';
  #dragOver = false;
  /** The blob each live thumbnail canvas currently shows. A reopen builds new
   * canvases (absent here, so painted again) and a removal that shifts the
   * list repaints the slots whose blob changed. */
  /** The file inputs already carrying the change listener. */
  #pickers = new WeakSet<HTMLInputElement>();
  #painted = new WeakMap<HTMLCanvasElement, Blob>();

  constructor() {
    super();
    this.screen = '';
    this.send = null;
    this.open = false;
  }

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener('click', this.#onHostClick);
    this.addEventListener('keydown', this.#onKeydown);
    this.addEventListener('input', this.#onInput);
    this.addEventListener('dragover', this.#onDragOver);
    this.addEventListener('dragleave', this.#onDragLeave);
    this.addEventListener('drop', this.#onDrop);
    document.addEventListener('paste', this.#onPaste);
  }

  disconnectedCallback(): void {
    this.removeEventListener('click', this.#onHostClick);
    this.removeEventListener('keydown', this.#onKeydown);
    this.removeEventListener('input', this.#onInput);
    this.removeEventListener('dragover', this.#onDragOver);
    this.removeEventListener('dragleave', this.#onDragLeave);
    this.removeEventListener('drop', this.#onDrop);
    document.removeEventListener('paste', this.#onPaste);
    this.#thumbs = [];
    super.disconnectedCallback();
  }

  /** The imperative half: the note's text and the thumbnails' pixels. The
   * panel is not rendered while closed, so each open builds a fresh textarea
   * and fresh canvases: the kept note is put back and the kept images are
   * painted again, so what is shown is exactly what Send will post. */
  protected override updated(changed: PropertyValues): void {
    super.updated(changed);
    // The hidden picker's change event is not composed, so it never reaches
    // the host: the listener rides the input itself, once per fresh input
    // (each open builds a new one).
    const picker = this.renderRoot.querySelector('input[type="file"]');
    if (picker instanceof HTMLInputElement && !this.#pickers.has(picker)) {
      this.#pickers.add(picker);
      picker.addEventListener('change', this.#onChange);
    }
    const note = this.renderRoot.querySelector('[data-testid="feedback-note"]');
    if (note instanceof HTMLTextAreaElement && note.value !== this.#note) note.value = this.#note;
    void this.#paintThumbs();
  }

  /** Paints every thumbnail whose canvas does not yet show its blob. */
  async #paintThumbs(): Promise<void> {
    const slots = Array.from(this.renderRoot.querySelectorAll<HTMLElement>('[data-thumb-index]'));
    for (const slot of slots) {
      const index = Number(slot.getAttribute('data-thumb-index'));
      const thumb = this.#thumbs[index];
      const canvas = slot.querySelector('canvas');
      if (thumb === undefined || !(canvas instanceof HTMLCanvasElement)) continue;
      if (this.#painted.get(canvas) === thumb.blob) continue;
      this.#painted.set(canvas, thumb.blob);
      try {
        const bitmap = await createImageBitmap(thumb.blob);
        const context = canvas.getContext('2d');
        if (context === null) continue;
        context.clearRect(0, 0, canvas.width, canvas.height);
        const scale = Math.min(canvas.width / bitmap.width, canvas.height / bitmap.height);
        const width = bitmap.width * scale;
        const height = bitmap.height * scale;
        context.drawImage(bitmap, (canvas.width - width) / 2, (canvas.height - height) / 2, width, height);
        bitmap.close();
      } catch {
        // An undecodable image keeps its empty canvas; the remove control
        // still works and Send still carries the blob.
      }
    }
  }

  /** The host delegation rule: read the control off composedPath(). */
  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-action'));
    if (control === undefined) return;
    switch (control.getAttribute('data-action')) {
      case 'toggle':
        this.#toggle();
        break;
      case 'close':
        this.open = false;
        break;
      case 'kind':
        this.#kind = (control.getAttribute('data-kind') ?? 'bug') as 'bug' | 'feedback' | 'flag';
        this.#sentNumber = null;
        this.#error = '';
        this.requestUpdate();
        break;
      case 'capture':
        void this.#capture();
        break;
      case 'pick':
        this.#sentNumber = null;
        this.#error = '';
        this.#pickFiles();
        break;
      case 'remove':
        this.#removeThumb(Number(control.getAttribute('data-index') ?? '0'));
        break;
      case 'send':
        void this.#send();
        break;
    }
  };

  /** Escape closes the panel; the note and the images stay for the reopen. */
  readonly #onKeydown = (event: KeyboardEvent): void => {
    if (event.key === 'Escape' && this.open) {
      this.open = false;
      event.stopPropagation();
    }
  };

  /** The note is typed, never bound: the state rides this field alone. */
  readonly #onInput = (event: Event): void => {
    const target = event.composedPath()[0];
    if (target instanceof HTMLTextAreaElement) this.#note = target.value;
  };

  /** The hidden picker's change lands here (bound on the input itself). */
  readonly #onChange = (event: Event): void => {
    const target = event.composedPath()[0];
    if (target instanceof HTMLInputElement && target.type === 'file') {
      for (const file of Array.from(target.files ?? [])) this.#addImage(file);
      target.value = '';
    }
  };

  readonly #onDragOver = (event: DragEvent): void => {
    if (!this.open) return;
    event.preventDefault();
    if (!this.#dragOver) {
      this.#dragOver = true;
      this.requestUpdate();
    }
  };

  readonly #onDragLeave = (): void => {
    if (this.#dragOver) {
      this.#dragOver = false;
      this.requestUpdate();
    }
  };

  readonly #onDrop = (event: DragEvent): void => {
    if (!this.open) return;
    event.preventDefault();
    this.#dragOver = false;
    for (const file of Array.from(event.dataTransfer?.files ?? [])) this.#addImage(file);
    this.requestUpdate();
  };

  /** A paste while the panel is open becomes a thumbnail. */
  readonly #onPaste = (event: ClipboardEvent): void => {
    if (!this.open) return;
    const files = event.clipboardData?.files;
    if (files === undefined || files.length === 0) return;
    event.preventDefault();
    for (const file of Array.from(files)) this.#addImage(file);
  };

  /** Opens the panel: the way a host that owns its own button (trigger
   * attribute set to external) opens the widget. */
  show(): void {
    if (!this.open) this.#toggle();
  }

  #toggle(): void {
    this.open = !this.open;
    if (this.open) {
      this.#sentNumber = null;
      this.#error = '';
    }
  }

  #removeThumb(index: number): void {
    const thumb = this.#thumbs[index];
    if (thumb === undefined) return;
    this.#thumbs = this.#thumbs.filter((_, at) => at !== index);
    this.#sentNumber = null;
    this.requestUpdate();
  }

  /** The hidden picker opens on demand; its change lands in #onChange. */
  #pickFiles(): void {
    const input = this.renderRoot.querySelector('input[type="file"]');
    if (input instanceof HTMLInputElement) input.click();
  }

  /** One image in: type and bounds checked here, again server side. */
  #addImage(file: File): void {
    this.#error = '';
    if (!IMAGE_TYPES.includes(file.type)) {
      this.#error = 'That file is not a PNG, JPEG or WebP image.';
      this.requestUpdate();
      return;
    }
    if (file.size > MAX_IMAGE_BYTES) {
      this.#error = 'That image is larger than the 5 MB bound.';
      this.requestUpdate();
      return;
    }
    if (this.#thumbs.length >= MAX_IMAGES) {
      this.#error = 'At most three images: remove one first.';
      this.requestUpdate();
      return;
    }
    this.#thumbs = [...this.#thumbs, { blob: file, name: file.name || 'screenshot' }];
    this.requestUpdate();
  }

  /** The in-page capture: the current view, this widget hidden from the shot. */
  async #capture(): Promise<void> {
    if (this.#thumbs.length >= MAX_IMAGES) {
      this.#error = 'At most three images: remove one first.';
      this.requestUpdate();
      return;
    }
    this.#error = '';
    this.#sending = true;
    this.requestUpdate();
    try {
      const { domToBlob } = await import('./vendor/modern-screenshot.mjs');
      const root = document.documentElement;
      const backgroundColor = pageBackground();
      const filter = (node: Node): boolean =>
        !(node instanceof Element && (node.tagName === 'HH-FEEDBACK' || !!node.closest('hh-feedback')));
      // PNG first (flat UI colours compress well and stay lossless); the
      // bounds retry as JPEG, then a smaller JPEG, so a capture is never
      // dropped for size alone when one tighter encode would carry it.
      let blob = await domToBlob(root, { backgroundColor, filter, scale: 1 });
      if (blob !== null && blob.size > MAX_IMAGE_BYTES) {
        blob = await domToBlob(root, { backgroundColor, filter, scale: 1, type: 'image/jpeg', quality: 0.9 });
      }
      if (blob !== null && blob.size > MAX_IMAGE_BYTES) {
        blob = await domToBlob(root, { backgroundColor, filter, scale: 0.6, type: 'image/jpeg', quality: 0.85 });
      }
      if (blob === null || blob.size > MAX_IMAGE_BYTES) {
        this.#error = 'The capture is too large to attach.';
        return;
      }
      const stamp = new Date().toISOString().replace(/[:.]/g, '-');
      this.#thumbs = [...this.#thumbs, { blob, name: `screen-${stamp}.png` }];
    } catch {
      this.#error = 'The screen could not be captured.';
    } finally {
      this.#sending = false;
      this.requestUpdate();
    }
  }

  async #send(): Promise<void> {
    this.#error = '';
    const note = this.#note.trim();
    if (note === '') {
      this.#error = 'Write the note first: one line tells the house what happened.';
      this.requestUpdate();
      return;
    }
    if (this.send === null) {
      this.#error = 'This screen cannot send feedback itself; the note stays here.';
      this.requestUpdate();
      return;
    }
    this.#sending = true;
    this.requestUpdate();
    try {
      const answer = await this.send({
        type: this.#kind,
        message: note,
        screen: this.screen,
        url: window.location.href,
        userAgent: navigator.userAgent,
        images: this.#thumbs.map((thumb) => ({ blob: thumb.blob, name: thumb.name })),
      });
      if (answer.ok) {
        this.#sentNumber = answer.issueNumber;
        // A sent submission is finished: the note and the images go.
        this.#thumbs = [];
        this.#note = '';
      } else {
        // A failure keeps everything, for the retry.
        this.#error = answer.error;
      }
    } catch {
      this.#error = 'Sending failed; the note and the images are kept.';
    } finally {
      this.#sending = false;
      this.requestUpdate();
    }
  }

  render(): Template {
    return html`
      <button
        type="button"
        class="pill"
        data-action="toggle"
        data-testid="feedback-pill"
        aria-expanded="${this.open ? 'true' : 'false'}"
      >
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path>
        </svg>
        Feedback
      </button>
      ${this.open ? this.#panel() : nothing}
    `;
  }

  #panel(): Template {
    const sent = this.#sentNumber;
    return html`
      <div
        class="panel"
        role="dialog"
        aria-label="Send feedback"
        data-testid="feedback-panel"
      >
        <div class="head">
          <h2>Send feedback</h2>
          <button type="button" class="btn-secondary close" data-action="close" aria-label="Close feedback">Close</button>
        </div>

        <div class="kinds" role="group" aria-label="What kind of feedback is this?">
          ${KINDS.map(
            (kind) => html`
              <button
                type="button"
                class="kind"
                data-action="kind"
                data-kind="${kind.id}"
                data-testid="${`feedback-kind-${kind.id}`}"
                aria-pressed="${this.#kind === kind.id ? 'true' : 'false'}"
              >
                ${kind.label}
              </button>
            `,
          )}
        </div>

        <textarea
          data-testid="feedback-note"
          placeholder="Say what you saw, in your own words."
          aria-label="The note"
        ></textarea>

        <div class="tools">
          <button type="button" class="btn-secondary tool" data-action="capture" ?disabled="${this.#sending}" data-testid="feedback-capture">
            ${this.#sending ? 'Working...' : 'Capture this screen'}
          </button>
          <button type="button" class="btn-secondary tool" data-action="pick" ?disabled="${this.#sending}" data-testid="feedback-pick">
            Add an image
          </button>
        </div>

        ${this.#thumbs.length > 0
          ? html`
              <ul class="thumbs" data-testid="feedback-thumbs">
                ${this.#thumbs.map(
                  (_, index) => html`
                    <li class="thumb" data-thumb-index="${index}" data-testid="${`feedback-thumb-${index}`}">
                      <canvas width="84" height="62" aria-hidden="true"></canvas>
                      <button
                        type="button"
                        data-action="remove"
                        data-index="${index}"
                        data-testid="${`feedback-remove-${index}`}"
                        aria-label="Remove image ${index + 1}"
                      >
                        ×
                      </button>
                    </li>
                  `,
                )}
              </ul>
            `
          : nothing}

        <p class="${this.#dragOver ? 'drop over' : 'drop'}">
          Paste, drag, or pick an image: up to three.
        </p>

        <button
          type="button"
          class="btn-primary send"
          data-action="send"
          ?disabled="${this.#sending}"
          data-testid="feedback-send"
        >
          ${this.#sending ? 'Sending...' : 'Send'}
        </button>

        ${sent !== null
          ? html`<p class="status" data-tone="sent" data-testid="feedback-sent" role="status">${sent === '' ? 'Sent.' : `Sent: ${sent}`}</p>`
          : nothing}
        ${this.#error !== ''
          ? html`<p class="status" data-tone="error" data-testid="feedback-error" role="alert">${this.#error}</p>`
          : nothing}

        <input
          type="file"
          accept="image/png,image/jpeg,image/webp"
          multiple
          class="visually-hidden"
          aria-hidden="true"
          tabindex="-1"
          data-testid="feedback-file"
        />
      </div>
    `;
  }
}

defineElement('hh-feedback', HHFeedback);

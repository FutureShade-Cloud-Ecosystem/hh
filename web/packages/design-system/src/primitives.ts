// The primitive layer: the seam where the Ionic primitives land.
//
// The source brief names Ionic as the kits' primitive library. The few
// primitives the kits need exist here as small template helpers over plain
// markup, with the call sites the Ionic elements will occupy. When Ionic lands
// as a workspace dependency, the BODIES of these helpers swap to the ion-*
// primitives and the kits do not change; that is the whole point of the seam.
// This deviation is recorded in the package README.
//
// Two carried conduct rules are implemented here, once, so every kit inherits
// them and no kit can get them wrong:
//
//   1. Disabled must LOOK disabled. A filled control at reduced opacity still
//      reads as clickable in daylight, so a disabled control DROPS ITS FILL AND
//      GOES GREY through the --hh-disabled-* token swap. There is no fade
//      anywhere in this package and the gate in test/kits.test.mjs fails if one
//      appears.
//   2. The focus ring is tokens or it does not exist. Every primitive carries
//      the :focus-visible ring from --hh-focus-ring, because a shadow root does
//      not inherit the document's ring rule.

import { css, html, nothing, type TemplateResult as Template } from 'lit';

export type ButtonKind = 'primary' | 'secondary' | 'danger';

/** The shared stylesheet every kit adopts. Token-only: no literal colours. */
export const baseStyles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
    /* The sheet declares the scheme per half; the kits follow it, so form
       controls and scrollbars inside a kit answer the theme too. */
    color-scheme: var(--hh-color-scheme);
    /* Tabular figures inside the kit as well (row 942): the sheet sets the
       same pair on the document root, but a kit can be embedded where that
       half never loaded, and a shadow root inherits only what the host page
       set. Inter renders every digit one width, so columns of money,
       quantities and codes line up without a mono face. */
    font-variant-numeric: tabular-nums;
    font-feature-settings: "tnum";
  }
  * { box-sizing: border-box; }

  /* Word-only-on-the-outside utility: visually gone, fully present to
     assistive tech. The state system pairs it with a VISIBLE cue (an icon, a
     marker), never with colour alone. */
  .visually-hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
    border: 0;
  }


  button {
    font-family: inherit;
    font-size: var(--hh-s-16);
    line-height: 1;
    min-height: var(--hh-h-input);
    padding: 0 var(--hh-s-20);
    border-radius: var(--hh-radius-control);
    border: 1px solid transparent;
    cursor: pointer;
    /* One line, centred both ways: a button that carries an icon beside its
       label draws the two on the same centre line at the design system's icon
       gap, the icon at the label's own size (1em of the button's font), so the
       mark is never larger than or higher than the word. */
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--hh-s-8);
  }
  .btn .icon {
    flex: none;
    width: 1em;
    height: 1em;
  }
  button:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .btn-primary {
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    border-color: var(--brand-fill-on);
  }
  .btn-primary:hover { background: var(--brand-fill-hover); }
  .btn-secondary {
    background: transparent;
    color: var(--hh-text);
    /* The boundary of a control is judged as a shape at 3:1 (WCAG 1.4.11), and
       the line token sits under that bar against the page, so the secondary
       button takes its own boundary token, declared in both halves to clear
       3:1 on the page and the card. Measured in test/contrast.test.mjs. */
    border-color: var(--hh-control-line);
  }
  .btn-danger {
    background: var(--hh-danger-fill);
    color: var(--hh-danger-fill-ink);
    /* On the brand mode card the fill alone is 2.78:1: no red can be both 4.5:1 under white
       ink and 3:1 against that card, so the button's own 1px edge is its boundary in brand
       mode (white there, transparent in light), the same pattern .btn-primary uses. */
    border-color: var(--hh-danger-fill-edge);
  }
  /* THE disabled rule, stated once: a token swap, never a fade. Filled kinds
     drop their fill and go grey; the grey pair clears AA in both halves. */
  button:disabled,
  button:disabled:hover,
  .control:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
    cursor: default;
  }
  /* The form controls: the same face the button carries (height, radius, the
     focus ring, the disabled swap above), so a form row reads as one system.
     The VALUE of a control is the consumer's wiring-pass job, never a binding:
     the shell sets it in its own update pass, as the quote desk already does. */
  .control {
    font-family: inherit;
    font-size: var(--hh-s-16);
    line-height: 1;
    min-height: var(--hh-h-input);
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
  }
  select.control {
    padding-inline-end: var(--hh-s-20);
  }
  .control:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  /* The segmented control: a joined row of buttons on the recessed track,
     the pill of the till's own view flip at the sheet's own face. The
     pressed segment is marked by aria-pressed and the row face (the row-alt
     fill), never by colour alone. */
  .segmented {
    display: inline-flex;
    align-items: stretch;
    gap: var(--hh-s-2);
    padding: var(--hh-s-2);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-track);
  }
  .seg-btn {
    /* The row's own box sits at the control face height (the sheet's input
       token), so a segmented control beside a text control reads as one
       row: the segment's height less its padding and the row's borders. */
    min-height: calc(var(--hh-h-input) - 6px);
    padding: 0 var(--hh-s-16);
    border: 1px solid transparent;
    border-radius: var(--hh-radius-pill);
    background: transparent;
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
    white-space: nowrap;
  }
  .seg-btn:hover:not(:disabled) {
    color: var(--hh-text);
  }
  .seg-btn.seg-on {
    background: var(--hh-row-alt);
    border-color: var(--hh-control-line);
    color: var(--hh-text);
    font-weight: 600;
  }
  /* The named group: the fieldset keeps its legend (the group's accessible
     name) and the legend is set as the group's visible heading. */
  .field-group {
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
    margin: 0;
    padding: var(--hh-s-12);
  }
  .field-group-heading {
    font-family: inherit;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    padding: 0 var(--hh-s-4);
  }

  .spinner-wrap {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-12);
  }
  .spinner {
    width: 28px;
    height: 28px;
    border-radius: var(--hh-radius-pill);
    border: 3px solid var(--hh-track);
    /* The moving arc is gold-as-INK, per theme: the gold FILL is 1.50:1 against
       the light page and a spinner drawn with it would vanish in Light Mode. */
    border-top-color: var(--hh-accent-text);
    animation: hh-spin 0.9s linear infinite;
  }
  @media (prefers-reduced-motion: reduce) {
    .spinner { animation: none; border-inline-end-color: var(--hh-accent-text); }
  }

  .chip {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    padding: var(--hh-s-4) var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-card);
    font-size: var(--hh-s-12);
  }
  .chip .icon { width: 14px; height: 14px; }
  .chip-neutral { color: var(--hh-text-dim); border: 1px solid var(--hh-line); }
  .chip-good { color: var(--hh-state-good); border: 1px solid var(--hh-state-good); }
  .chip-watch { color: var(--hh-state-watch); border: 1px solid var(--hh-state-watch); }
  .chip-stop { color: var(--hh-state-stop); border: 1px solid var(--hh-state-stop); }

  ul.rows { list-style: none; margin: 0; padding: 0; }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hh-s-12);
    padding: var(--hh-s-12) var(--hh-s-16);
    background: var(--hh-row);
    border-bottom: 1px solid var(--hh-line);
    color: var(--hh-text);
  }
  .row-alt { background: var(--hh-row-alt); }
  .row-selected { border-inline-start: 3px solid var(--hh-focus-ring); font-weight: 600; }

  @keyframes hh-spin { to { transform: rotate(360deg); } }
`;

export interface ButtonSpec {
  label: string;
  kind?: ButtonKind;
  disabled?: boolean;
  /** The action name for the shell to wire; rendered as data-action. */
  action?: string;
  /** An icon drawn before the label (the state icons' `icon()` markup, a
   * closed constant): sized to the label and centred on its line by the sheet. */
  iconMarkup?: Template;
}

export function button(spec: ButtonSpec): Template {
  const kind = spec.kind ?? 'primary';
  // Lit's binding forms for the flag and the action: ?disabled renders the
  // bare attribute when true and nothing when false; the nothing sentinel
  // drops data-action when the spec carries none. Only closed-union constants
  // and flags are bound; a caller's values arrive as the label's text.
  return html`<button type="button" class="btn btn-${kind}" ?disabled=${spec.disabled === true} data-action=${spec.action ?? nothing}>${spec.iconMarkup ?? nothing}${spec.label}</button>`;
}

export interface SelectSpec {
  /** The control's accessible name. */
  label: string;
  /** The field name for the shell's change delegation; rendered as data-field. */
  field?: string;
  /** The screen test hook; rendered as data-testid. */
  testid?: string;
  disabled?: boolean;
  /** The select's static options, when the caller's own template holds them
   * (closed constants). Options a server answers for stay the consumer's
   * wiring-pass job, as do the control's value and its selection. */
  content?: Template;
}

/** The design system's select: the native control carrying the control face
 * and the shell's delegation attributes, with no value binding anywhere.
 *
 * The two shapes are deliberate: a select whose options stay Lit's own (the
 * static content) and a BARE select with no child part at all. A shell that
 * fills a select's options by hand in its wiring pass sets the element's
 * textContent, which ejects a child part's marker nodes and kills the next
 * render; the bare shape leaves the element with nothing for Lit to mourn. */
export function select(spec: SelectSpec): Template {
  if (spec.content === undefined) {
    return html`<select
      class="control"
      data-control="select"
      data-field=${spec.field ?? nothing}
      data-testid=${spec.testid ?? nothing}
      aria-label=${spec.label}
      ?disabled=${spec.disabled === true}
    ></select>`;
  }
  return html`<select
    class="control"
    data-control="select"
    data-field=${spec.field ?? nothing}
    data-testid=${spec.testid ?? nothing}
    aria-label=${spec.label}
    ?disabled=${spec.disabled === true}
  >${spec.content}</select>`;
}

export interface DateInputSpec {
  /** The control's accessible name. */
  label: string;
  /** The field name for the shell's change delegation; rendered as data-field. */
  field?: string;
  /** The screen test hook; rendered as data-testid. */
  testid?: string;
  disabled?: boolean;
}

/** The design system's date input: the native date control carrying the
 * control face. The value is the consumer's wiring-pass job, never a binding. */
export function dateInput(spec: DateInputSpec): Template {
  return html`<input
    class="control"
    data-control="date"
    type="date"
    data-field=${spec.field ?? nothing}
    data-testid=${spec.testid ?? nothing}
    aria-label=${spec.label}
    ?disabled=${spec.disabled === true}
  />`;
}

export interface TextInputSpec {
  /** The control's accessible name. */
  label: string;
  /** The field name for the shell's input delegation; rendered as data-field. */
  field?: string;
  /** The screen test hook; rendered as data-testid. */
  testid?: string;
  /** The input's closed type: free text or a search box. */
  type?: 'text' | 'search';
  disabled?: boolean;
}

/** The design system's text input: the native text or search control carrying
 * the control face. The type is the spec's closed union, rendered as a static
 * attribute of its own branch (the position gate holds templates to no value
 * binding outside the data and aria namespaces). The value is the consumer's
 * wiring-pass job, never a binding. */
export function textInput(spec: TextInputSpec): Template {
  if (spec.type === 'search') {
    return html`<input
      class="control"
      data-control="text"
      type="search"
      data-field=${spec.field ?? nothing}
      data-testid=${spec.testid ?? nothing}
      aria-label=${spec.label}
      ?disabled=${spec.disabled === true}
    />`;
  }
  return html`<input
    class="control"
    data-control="text"
    type="text"
    data-field=${spec.field ?? nothing}
    data-testid=${spec.testid ?? nothing}
    aria-label=${spec.label}
    ?disabled=${spec.disabled === true}
  />`;
}

export interface SegmentOption {
  /** The segment's words. */
  label: string;
  /** The segment's own value, the shell's click delegation reads it (data-value). */
  value: string;
}

export interface SegmentedSpec {
  /** The group's accessible name. */
  label: string;
  /** The field name for the shell's click delegation; rendered as data-field. */
  field?: string;
  /** The action name every segment carries for the shell's click delegation;
   * rendered as data-action (the shell reads the segment's own data-value). */
  action?: string;
  /** The screen test hook; rendered as data-testid. */
  testid?: string;
  disabled?: boolean;
  /** The options, closed constants the caller owns. */
  options: readonly SegmentOption[];
  /** The value of the pressed segment ('' or absent presses none): the one
   * marked state, rendered as the segment's own aria-pressed and active face. */
  selected?: string;
}

/** The design system's segmented control: a one of many picker as a joined
 * row of buttons on the recessed track. No hidden input and no value
 * binding: each segment carries its value and its pressed state, and the
 * shell's click delegation reads the pressed segment's data-value, the same
 * discipline the select's change delegation follows. */
export function segmented(spec: SegmentedSpec): Template {
  const on = spec.selected ?? '';
  return html`<div
    class="segmented"
    role="group"
    data-control="segmented"
    data-field=${spec.field ?? nothing}
    data-testid=${spec.testid ?? nothing}
    aria-label=${spec.label}
  >${spec.options.map(
    (option) => html`<button
      type="button"
      class="seg-btn${option.value === on ? ' seg-on' : ''}"
      data-value=${option.value}
      data-action=${spec.action ?? nothing}
      aria-pressed=${option.value === on ? 'true' : 'false'}
      ?disabled=${spec.disabled === true}
    >${option.label}</button>`,
  )}</div>`;
}

export interface FieldGroupSpec {
  /** The group's visible heading; the legend carries it as the group's
   * accessible name, so the group needs no second label. */
  heading: string;
  /** The group's controls. */
  content: Template;
}

/** The design system's named field group: the fieldset keeps its legend (the
 * group's accessible name) and the legend is set as the group's heading. */
export function fieldGroup(spec: FieldGroupSpec): Template {
  return html`<fieldset class="field-group" data-control="field-group">
    <legend class="field-group-heading">${spec.heading}</legend>
    ${spec.content}
  </fieldset>`;
}

/** The motion half of the loading pattern. The WORD lives in the state
 * indicator, which pairs the spinner with the loader icon and the label, so the
 * ring carries no text of its own and hides from assistive tech. */
export function spinner(): Template {
  return html`<span class="spinner-wrap"><span class="spinner" role="presentation" aria-hidden="true"></span></span>`;
}

export type ChipTone = 'neutral' | 'good' | 'watch' | 'stop';

export function chip(text: string, tone: ChipTone = 'neutral', iconMarkup?: Template): Template {
  const toneClass = tone === 'neutral' ? 'chip-neutral' : `chip-${tone}`;
  return html`<span class="chip ${toneClass}">${iconMarkup ?? ''}<span class="chip-text">${text}</span></span>`;
}

/** A list row on the striped pair, the role the row-stripe tokens exist for. */
export function row(content: Template, opts: { alt?: boolean; selected?: boolean } = {}): Template {
  const cls = `row${opts.alt ? ' row-alt' : ''}${opts.selected ? ' row-selected' : ''}`;
  // The selection is more than a bar and a weight: aria-current tells assistive
  // tech which row the detail pane is showing, on the element that carries the
  // visual cue. Lit's nothing sentinel drops the attribute on unselected rows.
  return html`<li class="${cls}" aria-current=${opts.selected ? 'true' : nothing}>${content}</li>`;
}

// The TanyAI panel's answer markup and its styles. Everything the person reads from the core goes in as a
// text node: the reply, a headline, a cell, a note, a chip. There is no raw markup path, no link and no
// preview anywhere here (plan invariant P3-5), and the template binds values only where the design
// system's position gate allows (text, class, role, data- and aria-). Every control rides a data-action
// name and the panel's one listener acts on it, the house delegation pattern.

import { css, html, nothing, type TemplateResult as Template } from 'lit';
import type { AnswerView, ResultView } from './tanyai-view.js';

/** Whether the host can open a record of this kind; the panel shows no Open button the host cannot honour. */
export type CanOpen = (kind: string) => boolean;

function renderResult(r: ResultView, turn: number, index: number, canOpen: CanOpen): Template {
  return html`
    <section class="result" data-result=${String(index)}>
      ${r.empty
        ? html`<h4 class="caption">${r.headline}</h4><p class="empty">Nothing found</p>`
        : html`
            <div class="scroll" role="region" tabindex="0" aria-label=${r.headline}>
              <table>
                <caption>${r.headline}</caption>
                <thead>
                  <tr>
                    <th scope="col">Item</th>
                    ${r.columns.map((c) => html`<th scope="col">${c}</th>`)}
                  </tr>
                </thead>
                <tbody>
                  ${r.rows.map(
                    (row) => html`
                      <tr>
                        <th scope="row">${row.title}</th>
                        ${row.cells.map((cell) => html`<td>${cell}</td>`)}
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      ${r.note !== null ? html`<p class="note">${r.note}</p>` : nothing}
      ${r.openLabel !== null && r.ref !== null && canOpen(r.ref.kind)
        ? html`<button type="button" class="chip open" data-action="open-ref" data-turn=${String(turn)} data-result=${String(index)}>${r.openLabel}</button>`
        : nothing}
    </section>
  `;
}

/** One answer. `turn` is its index in the panel's turn list, so a press can find its record. */
export function renderAnswer(view: AnswerView, turn = 0, canOpen: CanOpen = () => true): Template {
  return html`
    <div class="answer" data-kind=${view.kind}>
      ${view.notice !== null
        ? html`<p class="notice" role="note" data-notice=${view.notice.code} data-tone=${view.notice.tone}>${view.notice.words}</p>`
        : nothing}
      ${view.text !== '' ? html`<p class="text">${view.text}</p>` : nothing}
      ${view.citations.length > 0
        ? html`
            <ol class="cites" aria-label="Where these figures came from">
              ${view.citations.map(
                (c, i) => html`
                  <li>
                    <span class="mark" aria-hidden="true">${String(c.n)}</span>
                    ${canOpen(c.ref.kind) ? html`<button type="button" class="chip open" data-action="open-citation" data-turn=${String(turn)} data-citation=${String(i)}>${c.openLabel}</button>` : html`<span class="quiet">${c.label}</span>`}
                  </li>
                `,
              )}
            </ol>
          `
        : nothing}
      ${view.results.map((r, i) => renderResult(r, turn, i, canOpen))}
      ${view.scope !== null ? html`<p class="quiet scope">${view.scope}</p>` : nothing}
      ${view.clarify !== null
        ? html`
            ${view.clarify.question !== view.text ? html`<p class="text">${view.clarify.question}</p>` : nothing}
            <div class="chips" role="group" aria-label="Pick one">
              ${view.clarify.options.map(
                (o, i) => html`
                  <button type="button" class="chip pick" data-action="pick" data-turn=${String(turn)} data-option=${String(i)}>
                    <span class="chip-main">${o.label}</span>
                    ${o.hint !== null ? html`<span class="chip-hint">${o.hint}</span>` : nothing}
                  </button>
                `,
              )}
            </div>
          `
        : nothing}
      ${view.suggestions.length > 0
        ? html`
            <div class="chips" role="group" aria-label="Quick lookups">
              ${view.suggestions.map(
                (s) => html`<button type="button" class="chip" data-action="run-command" data-command=${s.code}>${s.label}</button>`,
              )}
            </div>
          `
        : nothing}
      ${view.stillAbout !== null ? html`<p class="quiet">${view.stillAbout}</p>` : nothing}
      ${view.usage !== null ? html`<p class="quiet">${view.usage}</p>` : nothing}
    </div>
  `;
}

/** The answer's styles, tokens only. The panel includes them in its own sheet. */
export const answerStyles = css`
  .answer { display: flex; flex-direction: column; gap: var(--hh-s-8); min-width: 0; }
  .text {
    margin: 0;
    font-size: 14px;
    line-height: 1.45;
    color: var(--hh-text);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .quiet { margin: 0; font-size: var(--hh-s-12); color: var(--hh-text-dim); }
  .notice {
    margin: 0;
    padding: var(--hh-s-8) var(--hh-s-12);
    border: 1px solid var(--hh-line);
    border-left-width: 4px;
    border-radius: var(--hh-radius-control);
    background: var(--hh-field);
    color: var(--hh-text);
    font-size: 13px;
    line-height: 1.4;
  }
  .notice[data-tone='refused'] { border-left-color: var(--hh-state-watch); }
  .notice[data-tone='info'] { border-left-color: var(--hh-info); }
  .result {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-12);
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    min-width: 0;
  }
  .result .caption, caption {
    margin: 0;
    padding: 0 0 var(--hh-s-8);
    text-align: left;
    font-size: 13px;
    font-weight: 700;
    color: var(--hh-text);
  }
  .scroll { overflow-x: auto; max-width: 100%; }
  .scroll:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  table { border-collapse: collapse; width: 100%; font-size: var(--hh-s-12); }
  th, td {
    padding: var(--hh-s-4) var(--hh-s-8);
    border-top: 1px solid var(--hh-line);
    text-align: left;
    white-space: nowrap;
    font-weight: 400;
    font-variant-numeric: tabular-nums;
  }
  thead th { font-weight: 700; color: var(--hh-text-dim); border-top: none; }
  tbody th { font-weight: 600; }
  .empty { margin: 0; font-size: 13px; color: var(--hh-text-dim); }
  .note { margin: 0; font-size: var(--hh-s-12); color: var(--hh-text-dim); }
  .cites { margin: 0; padding: 0; list-style: none; display: flex; flex-wrap: wrap; gap: var(--hh-s-8); }
  .cites li { display: inline-flex; align-items: center; gap: var(--hh-s-4); }
  .mark {
    display: inline-grid;
    place-items: center;
    min-width: 20px;
    height: 20px;
    border-radius: var(--hh-radius-pill);
    background: var(--hh-field);
    border: 1px solid var(--hh-line);
    font-size: 11px;
    font-weight: 700;
    color: var(--hh-text);
  }
  .chips { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); }
  .chip {
    display: inline-flex;
    flex-direction: column;
    align-items: flex-start;
    justify-content: center;
    gap: var(--hh-s-2);
    min-height: 44px;
    max-width: 100%;
    padding: var(--hh-s-8) var(--hh-s-12);
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    font-family: inherit;
    font-size: 13px;
    font-weight: 600;
    line-height: 1.2;
    text-align: left;
    cursor: pointer;
  }
  .chip:hover { border-color: var(--hh-accent-text); }
  .chip:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .chip-hint { font-size: 11px; font-weight: 400; color: var(--hh-text-dim); }
  .chip.open { align-self: flex-start; }
`;

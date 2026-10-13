// The arrange view's screens: the edit grid with its controls, the gallery
// sheet, the widget form with its live preview, the pinned tabs and classic
// tiles blocks, and the role defaults an admin sets. Pure template functions
// over the desk's state; app-root owns the desk and every listener, as the
// landing does.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, and bound values only in class, role,
// the data- and aria- namespaces and ?disabled. Controls ride data-action and
// data-op names and the host's listeners act on them; field values are
// mirrored from input events, never bound.
//
// Visual reference: the original app's home bento in arrange mode (the arrange
// bar, the gallery-first builder, the card control row), rebuilt on HH's
// tokens per this app's own boards (manager-home, Real @6f7f1d1f); the FB
// Design board for the builder itself is named r12 item 5 and does not exist
// yet, so the original's screen is the reference this app polishes.
import { html, nothing, type TemplateResult } from "lit";
import { EMPTY_HOME_WORDS, type BuilderState } from "./builder-desk.js";
import type { BuilderGalleryCard, BuilderWidgetRow, BuilderWidgetView } from "./builder-api.js";
import {
  WIDGET_SIZES,
  entryTitle,
  pinWord,
  shapeWord,
  sizeWord,
  sourceWord,
  spanOf,
  spanTag,
  splitWord,
  widgetSpan,
  widgetTitle,
  splitsFor,
  shapesFor,
  rangesFor,
  type WidgetDraft,
} from "./builder-logic.js";
import { figureFor, rangeWord } from "./manager-logic.js";

// -- the view's frame -----------------------------------------------------------------------------------

export function renderBuilder(model: BuilderState): TemplateResult {
  return html`
    <div class="view-head">
      <button type="button" class="btn btn-secondary back" data-action="back"><span class="chev" aria-hidden="true">‹</span>Back to Home</button>
      <h1>Arrange home</h1>
      <span class="where">${sourceWords(model)}</span>
      <p class="page-sub">Move, resize, add and remove the widgets on your home, and pin what the day opens with.</p>
    </div>
    ${model.error !== ""
      ? html`<p class="error-line" data-test="builder-error">${model.error}</p>
          <div class="actions-row">
            <button type="button" class="btn btn-secondary" data-action="builder" data-op="reload">Try again</button>
          </div>`
      : nothing}
    ${model.phase === "loading" ? html`<p class="notice-line">Loading your home…</p>` : nothing}
    ${model.phase === "ready" ? renderReady(model) : nothing}
  `;
}

function sourceWords(model: BuilderState): string {
  const source = sourceWord(model.source);
  return source === "" ? "" : `${source} · the ${model.profile} profile`;
}

function renderReady(model: BuilderState): TemplateResult {
  return html`
    <div class="scope-tabs" role="tablist" aria-label="What you are arranging">
      <button type="button" class="scope-tab" role="tab" data-action="builder" data-op="scope-mine" aria-selected=${model.scope === "mine" ? "true" : "false"}>My home</button>
      <button type="button" class="scope-tab" role="tab" data-action="builder" data-op="scope" aria-selected=${model.scope === "role" ? "true" : "false"}>Role defaults</button>
    </div>
    ${model.notice !== "" ? html`<p class="notice-line" role="status" data-test="builder-notice">${model.notice}</p>` : nothing}
    ${model.writeError !== "" ? html`<p class="error-line" data-test="builder-write-error">${model.writeError}</p>` : nothing}
    ${model.scope === "mine" ? renderMine(model) : renderRole(model)}
    <p class="who-note">Your layout is yours alone: the role default keeps running underneath it until you save a change. Role defaults are set by an administrator.</p>
  `;
}

// -- my scope ------------------------------------------------------------------------------------------

function renderMine(model: BuilderState): TemplateResult {
  const dirty = layoutDiffers(model.savedWidgets, model.widgets);
  return html`
    <div class="arrange-bar" role="status">
      <span class="say">
        <strong>Arranging.</strong> Move a widget with its arrows, step its size through the five presets with
        Smaller and Bigger, remove what you do not read. The needs-you queue stays on every home. Every control
        here is a button, so the whole surface works from the keyboard.
      </span>
      <span class="right">
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="reset-layout" ?disabled=${model.busy || model.source !== "user"}>Reset to role default</button>
        <button type="button" class="btn btn-primary" data-action="builder" data-op="save" data-test="builder-save" ?disabled=${model.busy || !dirty}>${model.busy ? "Saving…" : "Save layout"}</button>
      </span>
    </div>
    <div class="actions-row">
      <button type="button" class="btn btn-primary" data-action="builder" data-op="open-gallery" data-test="builder-add" ?disabled=${model.busy}>Add widget</button>
      <span class="source-chip">${model.widgets.length} ${model.widgets.length === 1 ? "widget" : "widgets"} · ${sourceWord(model.source)}</span>
    </div>
    ${model.galleryOpen ? renderGallery(model) : nothing}
    ${model.formOpen && model.draft !== null ? renderForm(model, model.draft) : nothing}
    ${model.widgets.length === 0
      ? html`
          <div class="empty-edit" data-test="builder-empty">
            <p>${EMPTY_HOME_WORDS}</p>
            <button type="button" class="btn btn-primary" data-action="builder" data-op="open-gallery">Add widget</button>
          </div>
        `
      : html`<section class="edit-grid" aria-label="Your home, arranging" data-test="builder-grid">
          ${model.widgets.map((w, i) => renderEditCard(w, i, model.widgets.length))}
        </section>`}
    ${renderPins(model)}
    ${renderTiles(model)}
  `;
}

/** The save button's test, computed beside the render: order, size, span, title, shape, split, window or branch. */
function layoutDiffers(before: readonly BuilderWidgetRow[], after: readonly BuilderWidgetRow[]): boolean {
  const sig = (rows: readonly BuilderWidgetRow[]) =>
    rows
      .map((w) => `${w.widget_key}:${w.size}:${w.cols ?? ""}x${w.rows ?? ""}:${w.title}:${w.shape}:${w.split}:${w.range}:${w.branch_id ?? ""}`)
      .join(" ");
  return sig(before) !== sig(after);
}

function renderEditCard(w: BuilderWidgetView, index: number, count: number): TemplateResult {
  const span = widgetSpan(w);
  const title = widgetTitle(w);
  const first = index === 0;
  const last = index === count - 1;
  return html`
    <div class="edit-cell c${span.cols} r${span.rows}">
      <article class="edit-card" data-widget=${w.widget_key}>
        <h2 class="card-title">${title}</h2>
        <p class="words-line">${shapeWord(w.shape)} · ${sizeWord(w.size)} · ${rangeWord(w.range)}${w.split !== "none" && w.split !== "" ? html` · ${splitWord(w.split)}` : nothing}</p>
        <span class="span-tag">${spanTag(span.cols, span.rows)}</span>
        <div class="controls">
          <button type="button" class="btn btn-secondary" data-action="builder" data-op="move" data-key=${w.widget_key} data-delta="-1" aria-label="Move ${title} earlier" ?disabled=${first}>‹ Earlier</button>
          <button type="button" class="btn btn-secondary" data-action="builder" data-op="move" data-key=${w.widget_key} data-delta="1" aria-label="Move ${title} later" ?disabled=${last}>Later ›</button>
          <button type="button" class="btn btn-secondary" data-action="builder" data-op="size-step" data-key=${w.widget_key} data-step="-1" aria-label="Make ${title} smaller" ?disabled=${w.size === "S"}>Smaller</button>
          <button type="button" class="btn btn-secondary" data-action="builder" data-op="size-step" data-key=${w.widget_key} data-step="1" aria-label="Make ${title} bigger" ?disabled=${w.size === "XL"}>Bigger</button>
          ${w.measure === "needs.you"
            ? html`<span class="source-chip">Stays on every home</span>`
            : html`<button type="button" class="btn btn-secondary" data-action="builder" data-op="remove" data-key=${w.widget_key} aria-label="Remove ${title}">Remove</button>`}
        </div>
      </article>
    </div>
  `;
}

// -- the gallery -----------------------------------------------------------------------------------------

function renderGallery(model: BuilderState): TemplateResult {
  return html`
    <section class="sheet-block" aria-label="Add a widget" data-test="builder-gallery">
      <div class="sheet-head">
        <h2>Add a widget</h2>
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="close-gallery">Close</button>
      </div>
      ${model.gallery.length === 0
        ? html`<p class="notice-line">The gallery has no entries for your role yet. Build your own from the catalog below.</p>`
        : html`
            <div class="gallery-grid">
              ${model.gallery.map((entry) => renderGalleryCard(entry))}
            </div>
          `}
      <button type="button" class="btn btn-primary" data-action="builder" data-op="open-form" data-test="builder-open-form">Build your own</button>
    </section>
  `;
}

function renderGalleryCard(entry: BuilderGalleryCard): TemplateResult {
  const title = entryTitle(entry);
  return html`
    <article class="gallery-card" data-entry=${entry.entry_key}>
      <span class="title">${title}</span>
      <span class="blurb">${entry.blurb}</span>
      <span class="words-line">${shapeWord(entry.shape)} · ${sizeWord(entry.size)} · ${rangeWord(entry.range)}</span>
      <button type="button" class="btn btn-primary add" data-action="builder" data-op="place-entry" data-entry=${entry.entry_key} aria-label="Add ${title} to your home">Add</button>
    </article>
  `;
}

// -- the form ---------------------------------------------------------------------------------------------

function renderForm(model: BuilderState, draft: WidgetDraft): TemplateResult {
  const measure = model.catalog?.measures.find((m) => m.metric_id === draft.measure);
  return html`
    <section class="sheet-block" aria-label="Build a widget" data-test="builder-form">
      <div class="sheet-head">
        <h2>Build your own</h2>
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="close-form">Cancel</button>
      </div>
      <div class="form-grid">
        <div class="field-groups">
          ${choiceGroup("Measure", model.catalog?.measures.map((m) => ({ value: m.metric_id, word: m.label })) ?? [], draft.measure, "measure")}
          ${choiceGroup("Body", shapesFor(measure).map((s) => ({ value: s, word: shapeWord(s) })), draft.shape, "shape")}
          ${choiceGroup("Split", splitsFor(measure).map((s) => ({ value: s, word: splitWord(s) })), draft.split, "split")}
          ${choiceGroup(
            "Branch",
            [{ value: "", word: "Every branch I can see" }, ...model.branches.map((b) => ({ value: b.id, word: b.name }))],
            draft.branch_id,
            "branch_id",
          )}
          ${choiceGroup("Window", rangesFor(measure, model.catalog?.ranges ?? []).map((r) => ({ value: r, word: rangeWord(r) })), draft.range, "range")}
          ${choiceGroup("Size", WIDGET_SIZES.map((s) => ({ value: s, word: sizeWord(s) })), draft.size, "size")}
          <div class="field-group">
            <span class="label">Title</span>
            <input class="text-field" type="text" data-field="builder-title" placeholder="Empty uses the measure's own words" aria-label="Title, empty uses the measure's own words" />
          </div>
        </div>
        ${renderPreview(model, draft)}
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-primary" data-action="builder" data-op="place-draft" data-test="builder-place">Place on home</button>
        <span class="source-chip">Placing adds it to the top of your home; nothing is kept until you save the layout.</span>
      </div>
    </section>
  `;
}

function choiceGroup(label: string, values: readonly { value: string; word: string }[], current: string, field: string): TemplateResult {
  return html`
    <div class="field-group" role="group" aria-label=${label}>
      <span class="label">${label}</span>
      <div class="choices">
        ${values.map((v) => html`
          <button type="button" class="choice-pill" data-action="builder" data-op="pick" data-field=${field} data-value=${v.value} aria-pressed=${current === v.value ? "true" : "false"}>${v.word}</button>
        `)}
      </div>
    </div>
  `;
}

function renderPreview(model: BuilderState, draft: WidgetDraft): TemplateResult {
  const tile = model.previewTile;
  return html`
    <div class="preview-box" data-test="builder-preview" aria-live="polite">
      <span class="label">Preview</span>
      ${tile === null
        ? html`<p class="figure none">${model.previewNote !== "" ? model.previewNote : "Choose a measure to preview it."}</p>`
        : previewBody(tile, draft)}
      ${tile !== null ? html`<span class="caption">${rangeWord(draft.range)}</span>` : nothing}
    </div>
  `;
}

function previewBody(tile: NonNullable<BuilderState["previewTile"]>, draft: WidgetDraft): TemplateResult {
  // The preview reads the composed tile through the landing's own figure rule:
  // only state ok with a real total is a figure; null is never a zero.
  const datum = { key: tile.tile_id, state: tile.state, total: tile.total, points: tile.points, note: tile.note, range: draft.range };
  const figure = figureFor(datum, tile.kind);
  if (figure === null) {
    return html`<p class="figure none">${tile.note !== "" ? tile.note : "Nothing to show for this window yet."}</p>`;
  }
  if (tile.points.length > 1) {
    const max = tile.points.reduce((high, p) => (p.number > high ? p.number : high), 0);
    return html`
      <p class="figure">${figure}</p>
      <ul class="preview-bars">
        ${tile.points.map((p) => html`
          <li class="bar-row">
            <span class="label">${p.label}</span>
            <span class="track"><span class="fill ${barWidthClass(p.number, max)}"></span></span>
            <span class="value">${p.number}</span>
          </li>
        `)}
      </ul>
    `;
  }
  return html`<p class="figure">${figure}</p>`;
}

function barWidthClass(part: number, max: number): string {
  const share = max > 0 ? Math.round((part / max) * 20) * 5 : 0;
  return `w${Math.max(0, Math.min(100, share))}`;
}

// -- pinned tabs -------------------------------------------------------------------------------------------

function renderPins(model: BuilderState): TemplateResult {
  return html`
    <section class="block" data-test="builder-tabs">
      <h2>Pinned tabs</h2>
      <p class="block-sub">The surfaces your shell opens with. An unpinned row is kept, so unpinning is never a deletion.</p>
      <div class="rows-list">
        ${model.tabs.length === 0 ? html`<div class="edit-row"><span class="sub">Nothing is pinned yet.</span></div>` : nothing}
        ${model.tabs.map((pin, i) => {
          const last = i === model.tabs.length - 1;
          const word = pinWord(pin.tab_key);
          return html`
            <div class="edit-row" data-tab=${pin.tab_key}>
              <span class="name">${word}<span class="sub">${pin.visible ? "On the strip" : "Unpinned"}</span></span>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="pin-move" data-key=${pin.tab_key} data-delta="-1" aria-label="Move the ${word} tab earlier" ?disabled=${i === 0}>‹ Earlier</button>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="pin-move" data-key=${pin.tab_key} data-delta="1" aria-label="Move the ${word} tab later" ?disabled=${last}>Later ›</button>
              <button type="button" class="choice-pill" data-action="builder" data-op="pin-toggle" data-key=${pin.tab_key} aria-pressed=${pin.visible ? "true" : "false"}>${pin.visible ? "Pinned" : "Pin"}</button>
            </div>
          `;
        })}
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="save-tabs" ?disabled=${model.busy}>Save tabs</button>
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="reset-tabs" ?disabled=${model.busy}>Reset tabs to the role default</button>
      </div>
    </section>
  `;
}

// -- classic tiles -------------------------------------------------------------------------------------------

function renderTiles(model: BuilderState): TemplateResult {
  return html`
    <section class="block" data-test="builder-tiles">
      <h2>Classic tiles</h2>
      <p class="block-sub">The dashboard route's own tiles, the classic home. A tile nobody measures yet says so before you pin it.</p>
      <div class="rows-list">
        ${model.tiles.length === 0 ? html`<div class="edit-row"><span class="sub">The catalog has no tiles for this deployment yet.</span></div>` : nothing}
        ${model.tiles.map((tile, i) => {
          const last = i === model.tiles.length - 1;
          return html`
            <div class="edit-row" data-tile=${tile.tile_id}>
              <span class="name">${tile.label}<span class="sub">${tile.lead !== "" ? tile.lead : tile.module}</span></span>
              ${tile.wired ? nothing : html`<span class="warn-tag">no data yet</span>`}
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="tile-move" data-key=${tile.tile_id} data-delta="-1" aria-label="Move ${tile.label} earlier" ?disabled=${i === 0}>‹ Earlier</button>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="tile-move" data-key=${tile.tile_id} data-delta="1" aria-label="Move ${tile.label} later" ?disabled=${last}>Later ›</button>
              <button type="button" class="choice-pill" data-action="builder" data-op="tile-toggle" data-key=${tile.tile_id} aria-pressed=${tile.visible ? "true" : "false"}>${tile.visible ? "Shown" : "Hidden"}</button>
            </div>
          `;
        })}
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="save-tiles" ?disabled=${model.busy}>Save tiles</button>
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="reset-tiles" ?disabled=${model.busy}>Reset tiles to the role default</button>
      </div>
    </section>
  `;
}

// -- the role defaults ----------------------------------------------------------------------------------------

function renderRole(model: BuilderState): TemplateResult {
  return html`
    ${model.roleError !== "" ? html`<p class="error-line" data-test="role-error">${model.roleError}</p>` : nothing}
    ${model.roleLoading ? html`<p class="notice-line">Loading the role defaults…</p>` : nothing}
    ${model.profiles.length > 0
      ? html`
          <div class="profile-picker" role="group" aria-label="Which role's defaults">
            ${model.profiles.map((p) => html`
              <button type="button" class="scope-tab" data-action="builder" data-op="pick-profile" data-profile=${p} aria-pressed=${model.role?.profile === p ? "true" : "false"}>${p}</button>
            `)}
          </div>
        `
      : nothing}
    ${model.role === null ? nothing : renderRoleBlocks(model)}
  `;
}

function renderRoleBlocks(model: BuilderState): TemplateResult {
  const role = model.role;
  if (role === null) return html``;
  return html`
    ${roleBlockWidgets(model, role.widgets)}
    ${roleBlockTiles(model)}
    ${roleBlockTabs(model)}
    ${roleBlockGallery(model)}
    ${roleBlockLanding(model)}
  `;
}

function roleBlockWidgets(model: BuilderState, widgets: readonly BuilderWidgetRow[]): TemplateResult {
  const role = model.role;
  if (role === null) return html``;
  return html`
    <section class="block" data-test="role-widgets">
      <h2>The role's widgets</h2>
      <p class="block-sub">Everyone with this role sees these unless they arrange their own home.</p>
      <div class="edit-grid">
        ${widgets.map((w, i) => {
          const span = spanOf(w.size);
          const title = widgetTitle(w);
          const first = i === 0;
          const last = i === widgets.length - 1;
          const cols = span.cols === 0 ? 6 : span.cols;
          const rows = span.rows === 0 ? 1 : span.rows;
          return html`
            <div class="edit-cell c${cols} r${rows}">
              <article class="edit-card" data-widget=${w.widget_key}>
                <h2 class="card-title">${title}</h2>
                <p class="words-line">${shapeWord(w.shape)} · ${sizeWord(w.size)} · ${rangeWord(w.range)}</p>
                <div class="controls">
                  <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-move" data-key=${w.widget_key} data-delta="-1" aria-label="Move the role's ${title} earlier" ?disabled=${first}>‹ Earlier</button>
                  <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-move" data-key=${w.widget_key} data-delta="1" aria-label="Move the role's ${title} later" ?disabled=${last}>Later ›</button>
                  <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-size-step" data-key=${w.widget_key} data-step="-1" aria-label="Make the role's ${title} smaller" ?disabled=${w.size === "S"}>Smaller</button>
                  <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-size-step" data-key=${w.widget_key} data-step="1" aria-label="Make the role's ${title} bigger" ?disabled=${w.size === "XL"}>Bigger</button>
                  ${w.measure === "needs.you"
                    ? html`<span class="source-chip">Stays on every home</span>`
                    : html`<button type="button" class="btn btn-secondary" data-action="builder" data-op="role-remove" data-key=${w.widget_key} aria-label="Remove the role's ${title}">Remove</button>`}
                </div>
              </article>
            </div>
          `;
        })}
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-primary" data-action="builder" data-op="role-save" data-test="role-widgets-save" ?disabled=${model.busy}>${model.busy ? "Saving…" : `Save the ${role.profile} role's widgets`}</button>
      </div>
    </section>
  `;
}

function roleBlockTiles(model: BuilderState): TemplateResult {
  const role = model.role;
  if (role === null) return html``;
  return html`
    <section class="block" data-test="role-tiles">
      <h2>The role's classic tiles</h2>
      <div class="rows-list">
        ${role.tiles.map((tile, i) => {
          const last = i === role.tiles.length - 1;
          return html`
            <div class="edit-row" data-tile=${tile.tile_id}>
              <span class="name">${tile.label}</span>
              ${tile.wired ? nothing : html`<span class="warn-tag">no data yet</span>`}
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-tile-move" data-key=${tile.tile_id} data-delta="-1" aria-label="Move the role's ${tile.label} tile earlier" ?disabled=${i === 0}>‹ Earlier</button>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-tile-move" data-key=${tile.tile_id} data-delta="1" aria-label="Move the role's ${tile.label} tile later" ?disabled=${last}>Later ›</button>
              <button type="button" class="choice-pill" data-action="builder" data-op="role-tile-toggle" data-key=${tile.tile_id} aria-pressed=${tile.visible ? "true" : "false"}>${tile.visible ? "Shown" : "Hidden"}</button>
            </div>
          `;
        })}
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-save-tiles" ?disabled=${model.busy}>Save the ${role.profile} role's tiles</button>
      </div>
    </section>
  `;
}

function roleBlockTabs(model: BuilderState): TemplateResult {
  const role = model.role;
  if (role === null) return html``;
  return html`
    <section class="block" data-test="role-tabs">
      <h2>The role's pinned tabs</h2>
      <div class="rows-list">
        ${role.pins.length === 0 ? html`<div class="edit-row"><span class="sub">Nothing is pinned for this role yet.</span></div>` : nothing}
        ${role.pins.map((pin, i) => {
          const last = i === role.pins.length - 1;
          const word = pinWord(pin.tab_key);
          return html`
            <div class="edit-row" data-tab=${pin.tab_key}>
              <span class="name">${word}</span>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-pin-move" data-key=${pin.tab_key} data-delta="-1" aria-label="Move the role's ${word} tab earlier" ?disabled=${i === 0}>‹ Earlier</button>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-pin-move" data-key=${pin.tab_key} data-delta="1" aria-label="Move the role's ${word} tab later" ?disabled=${last}>Later ›</button>
              <button type="button" class="choice-pill" data-action="builder" data-op="role-pin-toggle" data-key=${pin.tab_key} aria-pressed=${pin.visible ? "true" : "false"}>${pin.visible ? "Pinned" : "Pin"}</button>
            </div>
          `;
        })}
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-save-tabs" ?disabled=${model.busy}>Save the ${role.profile} role's tabs</button>
      </div>
    </section>
  `;
}

function roleBlockGallery(model: BuilderState): TemplateResult {
  const role = model.role;
  if (role === null) return html``;
  return html`
    <section class="block" data-test="role-gallery">
      <h2>The role's gallery</h2>
      <p class="block-sub">The ready-made widgets this role's Add widget sheet opens on. Removing one is a save: the list is written whole.</p>
      <div class="rows-list">
        ${role.entries.length === 0 ? html`<div class="edit-row"><span class="sub">The gallery is empty for this role.</span></div>` : nothing}
        ${role.entries.map((entry, i) => {
          const last = i === role.entries.length - 1;
          const title = entryTitle(entry);
          return html`
            <div class="edit-row" data-entry=${entry.entry_key}>
              <span class="name">${title}<span class="sub">${entry.blurb}</span></span>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-entry-move" data-entry=${entry.entry_key} data-delta="-1" aria-label="Move the gallery's ${title} earlier" ?disabled=${i === 0}>‹ Earlier</button>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-entry-move" data-entry=${entry.entry_key} data-delta="1" aria-label="Move the gallery's ${title} later" ?disabled=${last}>Later ›</button>
              <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-entry-remove" data-entry=${entry.entry_key} aria-label="Remove ${title} from the gallery">Remove</button>
            </div>
          `;
        })}
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-save-gallery" data-test="role-gallery-save" ?disabled=${model.busy}>Save the ${role.profile} role's gallery</button>
      </div>
    </section>
  `;
}

function roleBlockLanding(model: BuilderState): TemplateResult {
  const role = model.role;
  if (role === null) return html``;
  const home = role.home;
  return html`
    <section class="block" data-test="role-landing">
      <h2>Where the role lands</h2>
      <p class="block-sub">A takeover opens a screen directly. On phones only keeps the bento at a desk.</p>
      <div class="field-groups">
        <div class="field-group" role="group" aria-label="Takeover">
          <button type="button" class="choice-pill" data-action="builder" data-op="home-takeover" aria-pressed=${home.takeover ? "true" : "false"}>${home.takeover ? "Skip the bento and open a screen directly" : "The bento is this role's landing"}</button>
        </div>
        <div class="field-group">
          <span class="label">Landing unit</span>
          <input class="text-field" type="text" data-field="builder-landing-unit" placeholder="yard" aria-label="The landing unit" />
        </div>
        <div class="field-group">
          <span class="label">Landing screen</span>
          <input class="text-field" type="text" data-field="builder-landing-module" placeholder="yard" aria-label="The landing screen" />
        </div>
        <div class="field-group" role="group" aria-label="Phones only">
          <button type="button" class="choice-pill" data-action="builder" data-op="home-mobile" aria-pressed=${home.mobile_only ? "true" : "false"}>${home.mobile_only ? "On phones only" : "On phones and at the desk"}</button>
        </div>
      </div>
      <div class="actions-row">
        <button type="button" class="btn btn-secondary" data-action="builder" data-op="role-home-save" data-test="role-landing-save" ?disabled=${model.busy}>Save the ${role.profile} role's landing</button>
      </div>
    </section>
  `;
}

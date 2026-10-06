// The control primitives' conduct gates, on top of the two carried rules the
// shared sheet states once: the select, the date input and the named field
// group render the control face (the focus ring, the disabled token swap)
// with no value binding anywhere, the value and the options being the
// consumer's wiring-pass job. The disabled rule is the sheet's: a select
// held with ?disabled lands in the grey token swap, never a fade.
//
// The rendered markup carries Lit's server render comment markers between
// attributes and around interpolated words, so the assertions read the
// attributes and the words, never one whole tag.
//
// Import order is the render helper's contract: the DOM shim lands before any
// src module loads.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { html } from 'lit';

import { renderTemplate } from './_lib/render.mjs';
import { baseStyles, select, dateInput, fieldGroup, button } from '../src/primitives.js';

test('the select renders the control face with the shell delegation attributes and no value binding', () => {
  const markup = renderTemplate(
    select({ label: 'Filter by status', field: 'status-filter', testid: 'qb-list-filter', content: html`<option value="">All statuses</option>` }),
  );
  assert.match(markup, /<select\b/);
  for (const attr of ['class="control"', 'data-control="select"', 'data-field="status-filter"', 'data-testid="qb-list-filter"', 'aria-label="Filter by status"']) {
    assert.ok(markup.includes(attr), `the select renders ${attr}`);
  }
  assert.match(markup, /<option value="">All statuses<\/option>/);
  // The select element itself carries no value and no selection: the caller's
  // wiring pass owns both. The options' own value attributes are static
  // content and stay.
  const selectTag = markup.match(/<select\b[^>]*>/);
  assert.ok(selectTag !== null, 'the markup holds the select tag');
  assert.ok(!/[\s"'](value|selected)=?/.test(selectTag[0].replace(/^<select/, '')), 'the select element renders no value or selection of its own');
});

test('the date input renders the control face and its value is nowhere in the markup', () => {
  const markup = renderTemplate(dateInput({ label: 'Expiry date', field: 'new-expiry', testid: 'qb-new-expiry' }));
  assert.match(markup, /<input\b/);
  for (const attr of ['class="control"', 'data-control="date"', 'type="date"', 'data-field="new-expiry"', 'data-testid="qb-new-expiry"', 'aria-label="Expiry date"']) {
    assert.ok(markup.includes(attr), `the date input renders ${attr}`);
  }
  assert.ok(!/[\s"']value=/.test(markup), 'the date input renders no value of its own');
});

test('the named field group keeps the legend as the group heading and the accessible name', () => {
  const markup = renderTemplate(fieldGroup({ heading: 'New quote', content: html`<p>row</p>` }));
  assert.match(markup, /<fieldset class="field-group" data-control="field-group">/);
  assert.match(markup, /<legend class="field-group-heading">/);
  assert.match(markup, /New quote/);
  assert.match(markup, /<p>row<\/p>/);
});

test('a held control renders the real disabled attribute, and the sheet swaps tokens instead of fading', () => {
  const markup = renderTemplate(select({ label: 'Branch', field: 'new-branch', disabled: true }));
  assert.match(markup, /\sdisabled(>|\s|<)/, 'the held select renders the real disabled attribute');
  const sheet = baseStyles.cssText;
  assert.ok(sheet.includes('--hh-disabled-fill'), 'the shared sheet holds the disabled token swap');
  assert.ok(!/(?:^|[\s;{"'`])(opacity|filter)\s*:/.test(sheet), 'the shared sheet fades nothing');
});

test('the action button stays on the sheet classes, so the controls read as one system', () => {
  const markup = renderTemplate(button({ label: 'Create', kind: 'primary', action: 'create' }));
  assert.match(markup, /<button type="button"/);
  assert.match(markup, /class="btn btn-primary"/);
  assert.match(markup, /data-action="create"/);
  assert.match(markup, /Create/);
});

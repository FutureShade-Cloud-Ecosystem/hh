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
import { baseStyles, select, dateInput, fieldGroup, button, segmented, textInput } from '../src/primitives.js';

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

// The segmented control (the quotes list's status tabs, the CP1 split-list
// round): a one of many picker rendered as a joined row of buttons. The
// pressed state is the segment's own aria-pressed and the sheet's active
// face, never a value binding on a hidden input; the shell's click
// delegation reads the pressed segment's data-value.
test('the segmented control renders a named group of buttons with the pressed one marked', () => {
  const markup = renderTemplate(
    segmented({
      label: 'Filter by status',
      field: 'status-filter',
      action: 'tab',
      testid: 'qd-list-tabs',
      selected: 'DRAFT',
      options: [
        { label: 'All', value: '' },
        { label: 'Draft', value: 'DRAFT' },
        { label: 'Sent', value: 'SENT' },
      ],
    }),
  );
  assert.match(markup, /<div\b[^>]*role="group"/);
  for (const attr of ['data-control="segmented"', 'data-field="status-filter"', 'data-testid="qd-list-tabs"', 'aria-label="Filter by status"']) {
    assert.ok(markup.includes(attr), `the segmented control renders ${attr}`);
  }
  // Every segment carries its own value and its pressed state; exactly the
  // selected one is pressed. The attributes are read one by one (the server
  // render's comment markers sit between them), in the order the template
  // writes them.
  assert.ok(markup.includes('data-action="tab"'), 'each segment carries the shell delegation action');
  const segments = [...markup.matchAll(/<button\s+type="button"\s+class="(seg-btn[^"]*)"\s+data-value="([^"]*)"\s+data-action="tab"\s+aria-pressed="([^"]*)"/g)];
  assert.equal(segments.length, 3, 'every option renders one segment button');
  const pressed = segments.filter((m) => m[3] === 'true');
  assert.deepEqual(pressed.map((m) => m[2]), ['DRAFT'], 'exactly the selected option is pressed');
  assert.ok(pressed[0][1].includes('seg-on'), 'the pressed segment carries the active face class');
  const off = segments.find((m) => m[2] === '');
  assert.equal(off[3], 'false', 'an unselected segment is not pressed');
  assert.ok(!off[1].includes('seg-on'), 'an unselected segment carries no active face class');
});

test('a held segment renders the real disabled attribute', () => {
  const markup = renderTemplate(
    segmented({ label: 'Filter by status', field: 'status-filter', disabled: true, options: [{ label: 'All', value: '' }] }),
  );
  assert.match(markup, /\bdisabled\s*>/, 'the held segment renders the real disabled attribute');
  // The joined row's own face is on the sheet, so every consumer reads the
  // same control.
  const sheet = baseStyles.cssText;
  assert.ok(sheet.includes('.segmented'), 'the shared sheet holds the segmented row face');
  assert.ok(sheet.includes('.seg-btn'), 'the shared sheet holds the segment face');
});

// The text input (the quotes list's search box): the control face on the
// native text or search input, the value the consumer's wiring-pass job.
test('the text input renders the control face and its value is nowhere in the markup', () => {
  const markup = renderTemplate(textInput({ label: 'Search quotes', field: 'list-search', testid: 'qd-list-search', type: 'search' }));
  assert.match(markup, /<input\b/);
  for (const attr of ['class="control"', 'data-control="text"', 'type="search"', 'data-field="list-search"', 'data-testid="qd-list-search"', 'aria-label="Search quotes"']) {
    assert.ok(markup.includes(attr), `the text input renders ${attr}`);
  }
  assert.ok(!/[\s"']value=/.test(markup), 'the text input renders no value of its own');
});

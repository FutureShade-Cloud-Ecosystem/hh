// The conflict cards' contract, over the server render: the words each
// outcome's card carries (the r4 offline board's words: superseded on the
// server, the merge report, the manual review card, the refused card), the
// card list's empty and counted shapes, and the dismiss contract (the button
// rides a data-action name the shell's one click listener acts on; the card
// is dismissed, never deleted, so nothing here drops a row).

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderKit } from './lib/render.mjs';
import { conflictCardView, ShellConflictCard, ShellConflictList } from '../src/shell-cards.ts';

const card = (over = {}) => ({
  id: 'card:k1',
  kind: 'superseded',
  scope: 'stops',
  policy: 'server_wins',
  serverState: { id: 's1', stop_no: 3, customer_name: 'Northline Hardscapes (demo)' },
  clientPayload: { id: 's1', time_window: 'morning' },
  error: null,
  ...over,
});

test('a server wins card says superseded on the server and names the server row', () => {
  const view = conflictCardView(card());
  assert.equal(view.tag, 'Server wins · stops');
  assert.equal(view.heading, 'Superseded on the server');
  assert.match(view.body, /moved on the server while you were offline/);
  assert.match(view.body, /server row is shown/);
});

test('a manual review card says it needs a person and that it never merges on its own', () => {
  const view = conflictCardView(card({ kind: 'review', scope: 'shortages', policy: 'manual' }));
  assert.equal(view.tag, 'Manual · shortages');
  assert.equal(view.heading, "Needs a person's review");
  assert.match(view.body, /never merges on its own/);
});

test('a merge report card says what merged', () => {
  const view = conflictCardView(card({ kind: 'merge_report', scope: 'attachments', policy: 'last_write_wins_by_field' }));
  assert.equal(view.tag, 'Merge · attachments');
  assert.equal(view.heading, 'Merged with a newer change');
  assert.match(view.body, /field by field/);
});

test('a rejected card carries the refusal words the server sent', () => {
  const view = conflictCardView(card({ kind: 'rejected', scope: 'pick_tasks', policy: null, error: 'line 2 is short, give the reason' }));
  assert.equal(view.tag, 'Refused · pick tasks');
  assert.equal(view.heading, 'Not accepted');
  assert.match(view.body, /line 2 is short, give the reason/);
});

test('an unknown kind is a defect at the call site: refused loudly, never painted', () => {
  assert.throws(() => conflictCardView(card({ kind: 'mystery' })), /unknown card kind/);
});

test('a superseded card renders its words, its server facts and the dismiss control', () => {
  const markup = renderKit(ShellConflictCard, { card: card() });
  assert.match(markup, /Superseded on the server/);
  assert.match(markup, /data-action="dismiss-card"/);
  assert.match(markup, /data-card-id="card:k1"/);
  assert.match(markup, /Dismiss/);
});

test('the card shows what the server holds, from the server state the outcome brought back', () => {
  const markup = renderKit(ShellConflictCard, { card: card() });
  assert.match(markup, /Northline Hardscapes \(demo\)/);
});

test('the card list renders one row per open card and none when all are dismissed', () => {
  const rows = renderKit(ShellConflictList, { cards: [card(), card({ id: 'card:k2', kind: 'review', scope: 'shortages', policy: 'manual' })] });
  assert.match(rows, /2 to review/);
  assert.match(rows, /Superseded on the server/);
  assert.match(rows, /Needs a person&#39;s review/);
  const empty = renderKit(ShellConflictList, { cards: [] });
  assert.match(empty, /Nothing waits for review/);
});

test('conflict card visual restyle renders YOUR DEVICE and AT THE COUNTER comparison boxes, warning badge, and safe actions without Keep mine', () => {
  const markup = renderKit(ShellConflictCard, {
    card: card({
      kind: 'review',
      scope: 'shortages',
      policy: 'manual',
      serverState: { id: 'sh-1', description: 'Coping stone, sandstone' },
      clientPayload: { id: 'sh-1', resolution: 'Short 4 EA reported' },
    }),
  });
  assert.match(markup, /YOUR DEVICE/, 'shows YOUR DEVICE comparison box');
  assert.match(markup, /AT THE COUNTER/, 'shows AT THE COUNTER comparison box');
  assert.match(markup, /Short 4 EA reported/, 'shows device payload summary');
  assert.match(markup, /Coping stone, sandstone/, 'shows counter server state summary');
  assert.match(markup, /warning-badge/, 'renders warning badge');
  assert.match(markup, /Use counter(?:&#39;|')s/, 'renders Use counter\'s safe action');
  assert.match(markup, /Open the order/, 'renders Open the order safe action');
  assert.doesNotMatch(markup, /Keep mine/, 'never offers an unsafe Keep mine write action');
});


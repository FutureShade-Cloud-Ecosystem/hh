// Ported from nothing: the wiring item's own host seam tests (the review's
// P3-5 and P3-10). The seams here are module state and one request door, so
// the cases run against the implementations with the fetch and the storage
// seams stubbed at their edges. Node built-in runner, same loader as the
// other ported suites.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  api,
  branchName,
  refusal,
  clearPanelLayoutRaw,
  clearSessionState,
  readPanelLayoutRaw,
  setBranches,
  wirePorts,
  writePanelLayoutRaw,
} from './ports.ts';

// A fixed house day, so the ring's requested-date default is assertable.
const HOUSE_DAY = '2026-04-01';

function wire() {
  wirePorts({
    headers: async () => ({ 'X-Org': 'hh-demo', Authorization: 'Bearer stub-token' }),
    houseDay: () => HOUSE_DAY,
    user: () => null,
    token: () => 'stub-token',
  });
}

// ── the request door's body enrichment (the review's P3-10) ──────────────────

test('the ring body gains the contract-required fields, and the card token is the demo token', async () => {
  wire();
  let captured = null;
  const realFetch = globalThis.fetch;
  globalThis.fetch = (async (url, init) => {
    captured = { url: String(url), init: init ?? {} };
    return new Response('{}', { status: 201 });
  });
  try {
    const answer = await api('/api/v1/pos/sales', {
      method: 'POST',
      body: JSON.stringify({
        idempotency_key: '00000000-0000-4000-8000-000000000001',
        branch_id: 'b',
        till_session_id: 't',
        customer_id: '',
        sale_type: 'COLLECT_NOW',
        tender: 'CARD',
        payment_token: 'tok_simulated_runner',
        requested_date: '',
        lines: [{ product_id: 'p', qty_entered: 1, uom_entered: 'sf', cost_cents: 0 }],
      }),
    });
    assert.equal(answer.status, 201);
    const sent = JSON.parse(String(captured?.init.body));
    // The contract's required strings and flags the reference's caller did not know.
    assert.equal(sent['cash_sale'], false);
    assert.equal(sent['signature_ref'], '');
    assert.equal(sent['notes'], '');
    assert.equal(sent['offline'], false);
    // The requested date defaults to the house zone's day the host names.
    assert.equal(sent['requested_date'], HOUSE_DAY);
    // The card token is the demo's fixed token, never the reference's spelling.
    assert.equal(sent['payment_token'], 'demo-card-token');
    // The headers ride the wired provider (the door hands fetch a Headers).
    const headers = captured?.init.headers;
    const headerValue = (name) => (typeof headers.get === 'function' ? headers.get(name) : headers[name]);
    assert.equal(headerValue('X-Org'), 'hh-demo');
    assert.equal(headerValue('Authorization'), 'Bearer stub-token');
  } finally {
    globalThis.fetch = realFetch;
  }
});

test('a cash ring says cash_sale, and a split legs carry the swap with the tender untouched', async () => {
  wire();
  let captured = null;
  const realFetch = globalThis.fetch;
  globalThis.fetch = (async (_url, init) => {
    captured = { init: init ?? {} };
    return new Response('{}', { status: 201 });
  });
  try {
    await api('/api/v1/pos/sales', {
      method: 'POST',
      body: JSON.stringify({
        tender: 'CASH',
        cash_tendered_cents: 4520,
        tenders: [
          { method: 'CASH', amount_cents: 4000 },
          { method: 'CARD', amount_cents: 520, payment_token: 'tok_simulated_runner' },
        ],
        lines: [],
      }),
    });
    const sent = JSON.parse(String(captured?.init.body));
    assert.equal(sent['cash_sale'], true);
    // The legs keep their rails and amounts; a card leg's token is swapped.
    const legs = sent['tenders'];
    assert.deepEqual(
      legs.map((leg) => [leg['method'], leg['amount_cents']]),
      [
        ['CASH', 4000],
        ['CARD', 520],
      ],
    );
    assert.equal(legs[1]['payment_token'], 'demo-card-token');
    assert.equal(legs[0]['payment_token'], undefined);
  } finally {
    globalThis.fetch = realFetch;
  }
});

test('the resolve body gains the contract-required fields the caller cannot name', async () => {
  wire();
  let captured = null;
  const realFetch = globalThis.fetch;
  globalThis.fetch = (async (_url, init) => {
    captured = { init: init ?? {} };
    return new Response('{}', { status: 200 });
  });
  try {
    await api('/api/v1/pricing/resolve', {
      method: 'POST',
      body: JSON.stringify({
        product_id: 'p',
        customer_id: 'c',
        customer_type: 'RETAIL',
        branch_id: 'b',
        qty_entered: 2,
        uom_entered: 'sf',
        cost_cents: 0,
      }),
    });
    const sent = JSON.parse(String(captured?.init.body));
    assert.equal(sent['category_id'], '');
    assert.equal(sent['has_approved_override'], false);
    assert.equal(sent['doc_ref_type'], 'order');
    assert.equal(sent['doc_ref_id'], '');
    // Everything the caller did send rides untouched.
    assert.equal(sent['qty_entered'], 2);
    assert.equal(sent['uom_entered'], 'sf');
  } finally {
    globalThis.fetch = realFetch;
  }
});

test('the parked list is unwrapped from the page shape to the rows the panel reads', async () => {
  wire();
  const realFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    new Response(JSON.stringify({ items: [{ id: 't1', ticket_no: 'PARK-1' }], limit: 1 }), { status: 200 });
  try {
    const answer = await api('/api/v1/pos/parked?branch_id=b');
    const rows = await answer.json();
    assert.equal(Array.isArray(rows), true);
    assert.equal(rows[0]['ticket_no'], 'PARK-1');
  } finally {
    globalThis.fetch = realFetch;
  }
});

// ── sign-out's own clear (the review's P3-5) ─────────────────────────────────

test('clearSessionState wipes the panel boxes and the branch facts the session owned', () => {
  // Another session's layout and branch list: a shared till inherits neither.
  writePanelLayoutRaw('hos-pos-panels-v1', '{"boxes":{},"order":[]}');
  setBranches([{ id: 'branch-a', name: 'Trenton' }]);
  assert.equal(readPanelLayoutRaw('hos-pos-panels-v1'), '{"boxes":{},"order":[]}');
  assert.equal(branchName('branch-a'), 'Trenton');

  clearSessionState();

  // Both go with the session: the boxes are forgotten, and an unknown branch
  // shortens to its id rather than naming a yard the login never reached.
  assert.equal(readPanelLayoutRaw('hos-pos-panels-v1'), null);
  assert.equal(branchName('branch-a'), 'branch-a');
});

test('the panel store keeps only what the session wrote: one key goes, the rest stand', () => {
  writePanelLayoutRaw('key-one', 'one');
  writePanelLayoutRaw('key-two', 'two');
  clearPanelLayoutRaw('key-one');
  assert.equal(readPanelLayoutRaw('key-one'), null);
  assert.equal(readPanelLayoutRaw('key-two'), 'two');
  clearSessionState();
});

// ── the host's own fetch (the Work shell's no signal guard rides it) ─────────

test('the request door sends through the fetch the host supplies, and a host without one uses the platform fetch', async () => {
  const seen = [];
  wirePorts({
    headers: async () => ({ 'X-Org': 'hh-demo', Authorization: 'Bearer stub-token' }),
    houseDay: () => HOUSE_DAY,
    user: () => null,
    token: () => 'stub-token',
    fetch: async (url, init) => {
      seen.push(`${(init && init.method) || 'GET'} ${url}`);
      return new Response('{"error":"No signal."}', { status: 503 });
    },
  });
  const refused = await api('/api/v1/pos/sales', { method: 'POST', body: '{}' });
  assert.equal(refused.status, 503);
  assert.deepEqual(seen, ['POST /api/v1/pos/sales']);

  wire();
  const realFetch = globalThis.fetch;
  let platform = 0;
  globalThis.fetch = async () => {
    platform += 1;
    return new Response('{}', { status: 200 });
  };
  try {
    await api('/api/v1/pos/parked');
    assert.equal(platform, 1);
    assert.deepEqual(seen, ['POST /api/v1/pos/sales'], 'the earlier host fetch is not used after a re-wire');
  } finally {
    globalThis.fetch = realFetch;
  }
});

// ── a refusal the host made before anything dialed (the no signal guard) ─────

test('a failed read throws the status as ever, but a refusal the host marked speaks its own words', async () => {
  wire();
  const plain = await refusal(new Response('{"error":"nope"}', { status: 503 }), 'resume');
  assert.equal(plain.message, 'resume 503');
  const marked = await refusal(
    new Response(JSON.stringify({ error: 'No signal. This needs the server.' }), { status: 503, headers: { 'X-HH-Host-Refusal': '1' } }),
    'resume',
  );
  assert.equal(marked.message, 'No signal. This needs the server.');
  const unreadable = await refusal(new Response('not json', { status: 503, headers: { 'X-HH-Host-Refusal': '1' } }), 'park');
  assert.equal(unreadable.message, 'park 503');
  const empty = await refusal(new Response('{}', { status: 503, headers: { 'X-HH-Host-Refusal': '1' } }), 'park');
  assert.equal(empty.message, 'park 503');
});

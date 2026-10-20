// The panel element over the server render: its fixed sentences are the core's own, its first run and its
// thread render the states of PANEL-API r1 section 4, and every control is labelled. The behaviour (keys, focus,
// asking, errors) is walked in a real browser by the Money Desk's tanyai.pw.ts.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { renderKit } from './lib/render.mjs';
import { ShellTanyai, LIMIT_REACHED_WORDS, CHAT_OFF_WORDS } from '../src/shell-tanyai.ts';
import { answerView } from '../src/tanyai-view.ts';

const core = readFileSync(fileURLToPath(new URL('../../../../core/internal/assist/service.go', import.meta.url)), 'utf8');
const goString = (name) => {
  const m = core.match(new RegExp(`const ${name} = ((?:"[^\\n]*"\\s*\\+?\\s*)+)`));
  return m[1].match(/"((?:[^"\\]|\\.)*)"/g).map((s) => JSON.parse(s)).join('');
};

test('the over budget and chat off sentences are the core\'s own, word for word', () => {
  assert.equal(LIMIT_REACHED_WORDS, goString('LimitReachedMessage'));
  assert.equal(CHAT_OFF_WORDS, goString('ChatDisabledMessage'));
});

const COMMANDS = {
  chat_enabled: true,
  commands: [
    { code: 'ar_aging', title: 'Who owes us', summary: 'Open balances by how late they are.', max_tier: 2, args: [] },
    { code: 'stock', title: 'Stock on hand', summary: 'What is on the shelf.', max_tier: 2, args: [] },
  ],
};

test('closed, the panel renders nothing', () => {
  assert.equal(renderKit(ShellTanyai, { open: false }).replace(/<!--.*?-->|<\?>/g, '').trim(), '');
});

test('first run lists the lookups for this role and says the answers follow the role', () => {
  const markup = renderKit(ShellTanyai, { open: true, transport: {}, commandList: COMMANDS, me: { chat_enabled: true, spend_visible: false } });
  assert.match(markup, /Ask about your day/);
  assert.match(markup, /Who owes us/);
  assert.match(markup, /Open balances by how late they are\./);
  assert.match(markup, /answers only what your role may see/);
});

test('first run without the lists yet is a skeleton in words, not a bare spinner', () => {
  const markup = renderKit(ShellTanyai, { open: true, transport: {} });
  assert.match(markup, /aria-label="Loading what TanyAI can do for you"/);
  assert.match(markup, /role="status"/);
});

test('with no transport the panel says it cannot answer, with a retry', () => {
  const markup = renderKit(ShellTanyai, { open: true });
  assert.match(markup, /TanyAI cannot answer right now\. Try again in a moment\./);
  assert.match(markup, /data-action="retry-intro"/);
});

test('over budget and chat off show the core sentence and keep the lookups', () => {
  const over = renderKit(ShellTanyai, { open: true, transport: {}, commandList: COMMANDS, me: { chat_enabled: true, spend_visible: false, over_budget: true } });
  assert.match(over, /this month&#39;s AI allowance|this month's AI allowance/);
  assert.match(over, /Who owes us/);
  assert.match(over, /<textarea[^>]*disabled/);
  const off = renderKit(ShellTanyai, { open: true, transport: {}, commandList: { ...COMMANDS, chat_enabled: false }, me: { chat_enabled: false, spend_visible: false } });
  assert.match(off, /chat is switched off right now/);
  assert.match(off, /<textarea[^>]*disabled/);
});

test('a thread renders the person\'s words and the answer, with the headings a reader needs', () => {
  const view = answerView({ text: 'Hello.', results: [], limit_reached: false, ran: [] });
  const markup = renderKit(ShellTanyai, {
    open: true,
    transport: {},
    commandList: COMMANDS,
    turns: [{ role: 'user', text: 'hi', label: 'hi' }, { role: 'assistant', text: 'Hello.', view }],
  });
  assert.match(markup, /You asked/);
  assert.match(markup, /TanyAI answered/);
  assert.match(markup, /role="log"/);
  assert.match(markup, /Start a new conversation/);
});

test('every control in the panel has a name', () => {
  const view = answerView({ text: 'x', results: [], limit_reached: false, ran: [] });
  const markup = renderKit(ShellTanyai, { open: true, transport: {}, commandList: COMMANDS, turns: [{ role: 'user', text: 'a' }, { role: 'assistant', text: 'x', view }] }).replace(/<!--.*?-->/g, '');
  assert.match(markup, /aria-label="Close TanyAI"/);
  assert.match(markup, /<label[^>]*for="tanyai-ask"[^>]*>Ask TanyAI<\/label>/);
  for (const button of markup.match(/<button[\s\S]*?<\/button>/g) ?? []) {
    const named = /aria-label="[^"]+"/.test(button) || />\s*[A-Za-z][^<]*</.test(button) || /<span class="t">/.test(button);
    assert.ok(named, `unnamed button: ${button.slice(0, 80)}`);
  }
});

test('the panel source carries no em or en dash', () => {
  const text = readFileSync(fileURLToPath(new URL('../src/shell-tanyai.ts', import.meta.url)), 'utf8');
  assert.ok(!/[–—]/.test(text));
});

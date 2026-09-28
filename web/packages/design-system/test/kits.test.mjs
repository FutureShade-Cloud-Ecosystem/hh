// The kits' conduct gates: the two carried rules and the coverage rule, as
// structural assertions over what the kits actually render.
//
//   COLOUR NEVER ENCODES STATE ALONE: every designed state's indicator pairs
//   its colour with an icon AND a word AND a pattern change (data-pattern), so
//   the state survives with the colour removed. Asserted structurally: the
//   rendered markup must carry all three, and no two states of a kit may share
//   an indicator shape.
//
//   DISABLED CHANGES COLOUR, NOT OPACITY: held controls are the
//   --hh-disabled-* token swap, stated once in primitives.ts. No shipped source
//   may declare an opacity or a filter, and the held-action states must render
//   real disabled buttons.
//
//   COVERAGE: every kit renders every designed state (the five from the source
//   brief) and the ready state beside them.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderTemplate, html, HHRenderError } from '../src/render.js';
import { KIT_STATES, STATE_META, kitShell, stateIndicator } from '../src/state.js';
import { baseStyles } from '../src/primitives.js';
import { STATE_ICONS, icon } from '../src/state-icons.js';
import { parseDeclarations, readPkg, shippedStyleFiles, sheetBlocks, stripComments } from './_lib/sheet.mjs';

import { ScanConfirmKit } from '../src/kits/scan-and-confirm.js';
import { ListDetailKit } from '../src/kits/list-detail.js';
import { GuidedFlowKit } from '../src/kits/guided-flow.js';
import { DashboardKit } from '../src/kits/dashboard.js';
import { InboxKit } from '../src/kits/inbox.js';

const KITS = [
  ['hh-scan-and-confirm', ScanConfirmKit],
  ['hh-list-detail', ListDetailKit],
  ['hh-guided-flow', GuidedFlowKit],
  ['hh-dashboard', DashboardKit],
  ['hh-inbox', InboxKit],
];

const render = (Kit, state) => Kit.renderToString({ state });

const attr = (markup, name) => {
  const m = markup.match(new RegExp(`${name}="([^"]*)"`));
  return m ? m[1] : null;
};

// ── coverage ────────────────────────────────────────────────────────────────────

test('the state system declares exactly the five designed states from the brief', () => {
  assert.deepEqual(
    [...KIT_STATES].sort(),
    ['conflict', 'empty', 'error', 'loading', 'offline'],
    'the designed states are fixed: empty, loading, offline, conflict, error',
  );
  for (const state of KIT_STATES) {
    const meta = STATE_META[state];
    assert.ok(meta.label.length > 0, `${state} needs a word`);
    assert.ok(meta.pattern.length > 0, `${state} needs a pattern marker`);
    assert.ok(STATE_ICONS[meta.icon].paths.length > 0, `${state} needs an icon`);
  }
});

// One test per matrix, loops inside: bun (CI's drift gate) refuses the second
// and later test() calls made from a top-level for-of (oven-sh/bun#5090).
test('kit coverage: every kit renders its every designed state and its ready state', () => {
  const failures = [];
  for (const [tag, Kit] of KITS) {
    for (const state of KIT_STATES) {
      try {
        const markup = render(Kit, state);
        assert.equal(attr(markup, 'data-kit'), tag, 'the root must name the kit');
        assert.equal(attr(markup, 'data-state'), state, 'the root must name the state');
        assert.equal(attr(markup, 'data-pattern'), STATE_META[state].pattern, 'the root must carry the state pattern');
        assert.ok(markup.includes('state-label') || markup.includes('banner-text'), `${tag}/${state} lost its word`);
        assert.ok(markup.includes('<svg'), `${tag}/${state} lost its icon`);
      } catch (e) {
        failures.push(`${tag}/${state}: ${e.message}`);
      }
    }
    try {
      const markup = render(Kit, 'ready');
      assert.equal(attr(markup, 'data-state'), 'ready');
      assert.equal(attr(markup, 'data-pattern'), 'ready');
      assert.ok(markup.includes('data-live="true"'), 'ready actions must be live');
    } catch (e) {
      failures.push(`${tag}/ready: ${e.message}`);
    }
  }
  assert.deepEqual(failures, [], `kit coverage failures:\n  ${failures.join('\n  ')}`);
});

// ── colour never encodes state alone ─────────────────────────────────────────────

// Every render's icons must carry exactly the geometry the state system
// declares for that state: distinctness is judged on the PATH DATA a user is
// shown, not on an invisible attribute, so five identical icons fail here.
const iconGeometry = (markup) => {
  const svgs = [...markup.matchAll(/<svg[^>]*>[\s\S]*?<\/svg>/g)].map((m) => m[0]);
  return svgs.map((svg) => [...svg.matchAll(/ d="([^"]+)"/g)].map((d) => d[1]).join(' ')).join(' || ');
};

test(`state indicators survive with colour removed: every kit`, () => {
  for (const [tag, Kit] of KITS) {
      const shapes = new Set();
      for (const state of KIT_STATES) {
        const markup = render(Kit, state);
        // The word, with content.
        const label = markup.match(/class="[^"]*(state-label|banner-text|review-title)[^"]*"\s*>([^<]+)</);
        assert.ok(label && label[2].trim().length > 0, `${tag}/${state}: no word beside the colour`);
        // The icon, as the geometry the user is shown: the state's declared
        // path data, byte for byte, not just some path on the page.
        const expected = STATE_META[state].icon;
        const geometry = iconGeometry(markup);
        const declared = STATE_ICONS[expected].paths.join(' ');
        assert.ok(geometry.includes(declared), `${tag}/${state}: the icon shown is not the ${expected} geometry (got: ${geometry || 'none'})`);
        assert.ok(!geometry.includes('data:image'), `${tag}/${state}: the icon must not be an embedded image`);
        // The pattern change, machine-checkable.
        const pattern = attr(markup, 'data-pattern');
        assert.ok(pattern && pattern !== 'ready', `${tag}/${state}: no pattern change marker`);
        shapes.add(`${pattern}|${label[2]}|${geometry}`);
      }
      // No two states of a kit may present the same shape: five distinct
      // pattern+word+geometry triples, so the states are tellable apart with
      // colour gone and with the invisible attributes gone too.
      assert.equal(
        shapes.size,
        KIT_STATES.length,
        `${tag}: two states present the same indicator shape (${[...shapes].join(' ; ')}), which means they are colour-only apart`,
      );
  }
});

test('the state icons are pairwise distinct geometry', () => {
  // The direct half of the gate above: identical path data across the marks
  // fails here even before any kit renders them.
  const seen = new Map();
  for (const [name, spec] of Object.entries(STATE_ICONS)) {
    const key = spec.paths.join('|');
    assert.ok(!seen.has(key), `the ${name} mark is byte-identical to the ${seen.get(key)} mark`);
    seen.set(key, name);
  }
});

test('the shared indicator helper is the one implementation every kit gets', () => {
  // Designed ONCE: every indicator on the page is this module's output, byte
  // for byte, not a per-kit re-derivation.
  const once = renderTemplate(stateIndicator('offline'));
  assert.match(once, /data-state="offline"/);
  assert.match(once, /data-pattern="cached"/);
  for (const state of KIT_STATES) {
    assert.match(renderTemplate(stateIndicator(state)), new RegExp(`data-pattern="${STATE_META[state].pattern}"`));
  }
  for (const file of ['src/kits/scan-and-confirm.ts', 'src/kits/list-detail.ts', 'src/kits/guided-flow.ts', 'src/kits/dashboard.ts', 'src/kits/inbox.ts']) {
    const src = readPkg(file);
    assert.ok(src.includes('kitShell'), `${file} must build on the shared kitShell scaffold`);
    assert.ok(!src.includes('STATE_META'), `${file} must not re-derive state metadata; the scaffold owns it`);
  }
});

test('offline and conflict are pattern changes, not recolourings: the body stays, the actions change', () => {
  // The offline pattern shows the cached body with held actions; the conflict
  // pattern shows the local body under a review card with the review live. That
  // is a change a reader can see with colour removed.
  const kit = InboxKit;
  const offline = render(kit, 'offline');
  assert.ok(offline.includes('banner-offline'), 'offline must carry the banner pattern');
  assert.ok(offline.includes('msg-main'), 'offline must show the cached body');
  assert.ok(offline.includes('data-live="false"'), 'offline must hold the actions');
  assert.match(offline, /<button[^>]*disabled[^>]*>/, 'offline must render really disabled buttons');

  const conflict = render(kit, 'conflict');
  assert.ok(conflict.includes('review-card'), 'conflict must carry the review pattern');
  assert.ok(conflict.includes('msg-main'), 'conflict must show the local body');
  const buttons = conflict.match(/<button[^>]*>[^<]*</g) ?? [];
  const review = buttons.find((b) => b.includes('data-action="review"'));
  assert.ok(review && !review.includes('disabled'), 'conflict must leave the scaffold review action live');
  assert.ok(buttons.filter((b) => b.includes('disabled')).length >= 2, 'conflict must hold the kit actions');

  const ready = render(kit, 'ready');
  for (const b of ready.match(/<button[^>]*>/g) ?? []) {
    assert.ok(!b.includes('disabled'), 'ready must hold nothing');
  }
});

// The five designed states are not the only places a kit shows state: the
// guided-flow rail shows done/current/todo on every render, chips carry tones,
// a row carries its selection. This walks EVERY kit in EVERY state and holds
// each state-showing spot to the rule, not only the five kit states.
test('every place a kit shows state carries a non-colour cue', () => {
  const allStates = ['ready', ...KIT_STATES];
  for (const [tag, Kit] of KITS) {
    for (const state of allStates) {
      const markup = render(Kit, state);
      // A DONE rail step: the check icon is the visible cue, the visually
      // hidden word is the AT cue; the green rides beside them, never alone.
      for (const m of markup.matchAll(/<li class="rail-step rail-step-done">[\s\S]*?<\/li>/g)) {
        assert.match(m[0], /<path d="/, `${tag}/${state}: a done rail step has no visible icon cue`);
        assert.match(m[0], /class="visually-hidden"[^>]*>\(done\)</, `${tag}/${state}: a done rail step has no word cue`);
      }
      // The CURRENT rail step: aria-current is the non-colour cue.
      for (const m of markup.matchAll(/<li class="rail-step rail-step-current"[^>]*>/g)) {
        assert.match(m[0], /aria-current="step"/, `${tag}/${state}: the current rail step is colour-only`);
      }
      // A toned chip carries its word.
      for (const tone of ['chip-good', 'chip-watch', 'chip-stop']) {
        const at = markup.indexOf(`chip ${tone}`);
        if (at === -1) continue;
        const window = markup.slice(at, at + 400);
        assert.match(window, /chip-text">[^<]+</, `${tag}/${state}: a ${tone} chip carries no word`);
      }
    }
  }
  // The selected row's cue is structural: the sheet must change something
  // besides colour on .row-selected, and the selection border must be the
  // logical property so a right-to-left reading mirrors it.
  const selected = baseStyles.match(/\.row-selected\s*\{[^}]*\}/);
  assert.ok(selected, 'the sheet must carry the .row-selected rule');
  assert.match(selected[0], /font-weight/, 'the selected row must carry a weight cue, not colour alone');
  assert.match(selected[0], /border-inline-start/, 'the selection border must be the logical property');
  assert.doesNotMatch(selected[0], /border-left/, 'the selection border must not be the physical left');
});

test('the selected row carries an ARIA state, and only it does', () => {
  // The bar and the weight are for eyes; aria-current is the same fact for
  // assistive tech. Exactly one row per list may carry it.
  for (const state of ['ready', 'offline', 'conflict']) {
    for (const tag of ['hh-list-detail', 'hh-inbox']) {
      const Kit = KITS.find(([t]) => t === tag)[1];
      const markup = render(Kit, state);
      const lis = markup.match(/<li class="row[^"]*"[^>]*>/g) ?? [];
      assert.ok(lis.length >= 3, `${tag}/${state}: expected the row list to render`);
      const withAria = lis.filter((li) => li.includes('aria-current="true"'));
      assert.equal(withAria.length, 1, `${tag}/${state}: exactly one row must carry aria-current`);
      assert.match(withAria[0], /row-selected/, `${tag}/${state}: the ARIA row must be the visually selected one`);
    }
  }
  // Unselected rows carry no aria-current, and the kits without row lists
  // carry none at all.
  const plain = render(KITS[0][1], 'ready');
  assert.doesNotMatch(plain, /aria-current="true"/, 'scan-and-confirm has no row selection to mark');
  assert.match(render(KITS[2][1], 'ready'), /aria-current="step"/, 'the rail keeps its own aria-current on the current step');
});

// ── trusted markup is a brand, never a shape ─────────────────────────────────────

test('a caller object and a caller string are text, never markup', () => {
  const poison = '<img src=x onerror=alert(1)>';
  // A caller's object that carries an unsafe-looking key: parsed JSON shaped
  // like the old UnsafeHtml must land as TEXT (or nothing), never as markup.
  const fromObject = renderTemplate(html`<div>${{ unsafe: poison }}</div>`);
  assert.ok(!fromObject.includes('<img'), `a caller object with an unsafe key became markup: ${fromObject}`);
  assert.ok(!fromObject.includes('onerror'), `a caller object with an unsafe key became markup: ${fromObject}`);
  // The same object handed straight to the renderer: escaped text.
  const direct = renderTemplate({ unsafe: poison });
  assert.ok(!direct.includes('<img') && !direct.includes('onerror='), `renderTemplate emitted a caller's object raw: ${direct}`);
  // A caller's string: escaped.
  const fromString = renderTemplate(html`<div>${poison}</div>`);
  assert.match(fromString, /&lt;img src=x onerror=alert\(1\)&gt;/, "a caller's string was not escaped");
  // A caller's object shaped like the old Template (strings key): shape is
  // never trust, so this is escaped text too.
  const fakeTemplate = renderTemplate(html`<div>${{ strings: ['<b>bold</b>'], values: [] }}</div>`);
  assert.ok(!fakeTemplate.includes('<b>'), `an object with a strings key became markup: ${fakeTemplate}`);
  const fakeDirect = renderTemplate({ strings: ['<b>bold</b>'], values: [] });
  assert.ok(!fakeDirect.includes('<b>'), `renderTemplate emitted a fake template raw: ${fakeDirect}`);
  // The icon path data is bound as an ATTRIBUTE value: a poisoned label or a
  // poisoned d cannot close the attribute and inject. The label lands as
  // ESCAPED text (inert inside the title), never as a second element.
  const labelled = renderTemplate(icon('check', `${poison}`));
  assert.ok(!/<img/.test(labelled), `a poisoned icon label became markup: ${labelled}`);
  assert.match(labelled, /&lt;img src=x onerror/, 'the poisoned label must appear escaped, not stripped and not raw');
  // And no raw-markup escape hatch may return, under any name.
  assert.ok(!readPkg('src/index.ts').includes('unsafeSvg'), 'the public surface exports unsafeSvg again');
  assert.ok(!/export\s+(function|const|class)\s+unsafeSvg/.test(readPkg('src/render.ts')), 'src/render.ts exports an unsafeSvg escape hatch');
});

test('html() accepts only a real template strings array', () => {
  // The engine hands a tagged call a TemplateStringsArray, which carries `raw`.
  // A plain array (say parsed from JSON) has none, and its items would be
  // emitted as literal markup: the reviewer's probe ran script through exactly
  // this door. Both shapes are refused at the factory, by name.
  const fromJsonArray = () => html(JSON.parse('["<img src=x onerror=window.__pwn=1>"]'));
  assert.throws(fromJsonArray, HHRenderError, 'a plain array as the strings argument must be refused');
  assert.throws(fromJsonArray, /template strings array/, 'the refusal must name what was wrong');
  const fromJsonObject = () => html(JSON.parse('{"0":"<img src=x onerror=window.__pwn=1>","raw":["<img src=x onerror=window.__pwn=1>"]}'));
  assert.throws(fromJsonObject, HHRenderError, 'a plain object shaped like a strings array must be refused');
  assert.throws(() => html('<img src=x onerror=alert(1)>'), HHRenderError, 'a bare string must be refused');
  // A real tagged call keeps working, branded and composable.
  assert.equal(renderTemplate(html`<b>safe</b>`), '<b>safe</b>');
});

test('html() refuses a binding in an unquoted attribute, by name', () => {
  // Escaping makes a QUOTED attribute value inert; an unquoted one the value
  // can close and escape into. The factory reads the position from the static
  // text before the binding.
  const unquoted = () => html`<div class=${'x onmouseover=window.__pwn=1'}>text</div>`;
  assert.throws(unquoted, HHRenderError);
  assert.throws(unquoted, /unquoted attribute class/, 'the refusal must name the attribute');
  // The same value in the SAME attribute, quoted, is the ordinary escaped path.
  assert.equal(
    renderTemplate(html`<div class="${'x onmouseover=window.__pwn=1'}">text</div>`),
    '<div class="x onmouseover=window.__pwn=1">text</div>',
  );
});

test('html() refuses a value in an event attribute, by name', () => {
  const handler = () => html`<button onclick="${'window.__pwn=1'}">go</button>`;
  assert.throws(handler, HHRenderError);
  assert.throws(handler, /event attribute onclick/, 'the refusal must name the event attribute');
  const mouseover = () => html`<span onmouseover='${'window.__pwn=1'}'>x</span>`;
  assert.throws(mouseover, HHRenderError);
});

test('html() refuses an address that is not http, https or relative, by name', () => {
  for (const bad of ['javascript:alert(1)', 'JAVASCRIPT:alert(1)', ' java\nscript:alert(1)', 'data:text/html,<script>alert(1)</script>', 'vbscript:x']) {
    const refuse = () => html`<a href="${bad}">x</a>`;
    assert.throws(refuse, HHRenderError, `${JSON.stringify(bad)} must be refused as an address`);
    assert.throws(refuse, /address/, 'the refusal must name it as an address');
  }
  // The other navigation attributes are held to the same rule.
  assert.throws(() => html`<img src="${'javascript:alert(1)'}">`, HHRenderError);
  assert.throws(() => html`<form action="${'javascript:alert(1)'}">`, HHRenderError);
  // A value that is not an address-shaped string (a template, an object) is
  // refused too: markup has no business inside a navigation target.
  assert.throws(() => html`<a href="${html`<b>x</b>`}">x</a>`, HHRenderError);
  assert.throws(() => html`<a href="${{ evil: 'yes' }}">x</a>`, HHRenderError);
  // The safe set keeps working: http, https, scheme free, fragment, protocol
  // relative, and the empty address.
  for (const good of ['https://example.com/a', 'http://example.com/a', '/relative/path', 'rows/one', '#fragment', '//example.com/a', '']) {
    assert.doesNotThrow(() => html`<a href="${good}">x</a>`, `${JSON.stringify(good)} must keep rendering`);
  }
});

// The r3 review's five probes: each ran script through the previous position
// check, which read only the static text immediately before a value and so
// saw a plain attribute wherever the binding did not sit directly after the
// opening quote. One refusal per probe below.

test('html() refuses a binding anywhere inside an event attribute, not only directly after the quote', () => {
  // Probe B2: a value after other text inside the handler. Escaping cannot
  // save it: whatever lands in an event attribute is code.
  const prefixed = () => html`<button onclick="void 0;${'window.__pwn=1'}">go</button>`;
  assert.throws(prefixed, HHRenderError, 'a value after a static prefix inside onclick must be refused');
  assert.throws(prefixed, /event attribute onclick/, 'the refusal must name the event attribute');
  // A second binding later in the same handler is refused with the first.
  const second = () => html`<span onmouseover="${'a'};void 0;${'b'}">x</span>`;
  assert.throws(second, HHRenderError, 'the later binding inside the same handler must be refused');
  // The direct form keeps its refusal too.
  assert.throws(() => html`<button onclick="${'window.__pwn=1'}">go</button>`, HHRenderError);
});

test('html() refuses a value in srcdoc: the parser entity-decodes it back into a document', () => {
  // Probe B4: an escaped value inside srcdoc is unescaped again when the
  // iframe's document is parsed, so the script tags come back to life.
  const frame = () => html`<iframe srcdoc="${'<script>window.__pwn=1</script>'}"></iframe>`;
  assert.throws(frame, HHRenderError, 'a binding inside srcdoc must be refused');
  assert.throws(frame, /srcdoc/, 'the refusal must name srcdoc');
  // A static prefix inside the attribute does not save it either.
  const prefixed = () => html`<iframe srcdoc="<p>static ${'<img src=x onerror=window.__pwn=1>'}"></iframe>`;
  assert.throws(prefixed, HHRenderError, 'a value after a static prefix inside srcdoc must be refused');
});

test('html() refuses an address attribute whose value is not wholly one binding', () => {
  // Probe B1: the scheme split over two bindings reassembles in the browser,
  // where no per-value check can ever see it.
  const split = () => html`<a href="${'java'}${'script:alert(1)'}">x</a>`;
  assert.throws(split, HHRenderError, 'an address split over two bindings must be refused');
  assert.throws(split, /not wholly one value/, 'the refusal must say the value must be whole');
  // Probe B6: a binding after a literal prefix inside the address.
  const prefixed = () => html`<a href="javascript:${'alert(1)'}">x</a>`;
  assert.throws(prefixed, HHRenderError, 'a value after a static prefix inside an address must be refused');
  assert.throws(prefixed, /not wholly one value/, 'the refusal must say the value must be whole');
  // A suffix after the binding is refused the same way.
  const suffixed = () => html`<a href="${'/x'}?next">x</a>`;
  assert.throws(suffixed, HHRenderError, 'an address with a static suffix after the binding must be refused');
  // The whole address as one binding keeps rendering, scheme checked as before.
  assert.equal(renderTemplate(html`<a href="${'/x?next'}">x</a>`), '<a href="/x?next">x</a>');
});

test('html() refuses a binding in an unquoted attribute even after a static prefix', () => {
  // Probe B3: the old tail read saw title=x and no name=quote pattern at the
  // end, so the value rode through: a space ends an unquoted value and the
  // next word becomes a new attribute, here an event handler.
  const afterLetter = () => html`<div title=x${' onmouseover=window.__pwn=1'}>text</div>`;
  assert.throws(afterLetter, HHRenderError, 'a value riding after an unquoted prefix must be refused');
  assert.throws(afterLetter, /unquoted attribute title/, 'the refusal must name the unquoted attribute');
  // The sanctioned whole-attribute composition (the button spec's pattern:
  // closed-union constants and trusted sub-templates between attributes) keeps
  // rendering, attributes and all.
  assert.equal(
    renderTemplate(html`<button type="button" class="btn"${' disabled'}${html` data-x="1"`}>go</button>`),
    '<button type="button" class="btn" disabled data-x="1">go</button>',
  );
});


test('an unknown state fails loudly, never renders a quietly empty kit', () => {
  // A typo'd state name is a defect at the call site; an empty region would
  // hide it from everyone but the person using the screen.
  for (const [tag, Kit] of KITS) {
    assert.throws(
      () => render(Kit, 'nonsense'),
      (e) => e instanceof Error && e.message.includes(tag) && e.message.includes('nonsense'),
      `${tag}: an unknown state must throw, naming the kit and the state`,
    );
  }
  assert.match(render(KITS[0][1], 'ready'), /data-state="ready"/, 'the known states must keep rendering');
});

test('the kits answer the sheet: color-scheme is applied, not just declared', () => {
  const host = baseStyles.match(/:host\s*\{[^}]*\}/);
  assert.ok(host, 'the primitives must carry the :host rule');
  assert.match(host[0], /color-scheme:\s*var\(--hh-color-scheme\)/, ':host must follow the sheet per theme');
});

// ── disabled changes colour, not opacity ────────────────────────────────────────

test('the disabled rule is a token swap, stated once in the primitives', () => {
  const styles = baseStyles;
  const rule = styles.match(/button:disabled[\s\S]*?\}/);
  assert.ok(rule, 'the primitives must carry a button:disabled rule');
  assert.match(rule[0], /var\(--hh-disabled-fill\)/, 'the swap must set the disabled fill token');
  assert.match(rule[0], /var\(--hh-disabled-ink\)/, 'the swap must set the disabled ink token');
  assert.match(rule[0], /var\(--hh-disabled-line\)/, 'the swap must set the disabled line token');
});

test('no shipped source fades a control: no opacity declaration, no filter, anywhere', () => {
  // The counterexample that earned this rule was a raw "Accept and sign"
  // button at reduced opacity on the one screen where someone decides to spend
  // money. The rule is package-wide and the scan is comment-stripped, so a
  // comment that QUOTES the word is not a violation but a declaration is. The
  // scan matches a declaration after whitespace, a statement break, a brace,
  // OR a quote, so an inline style="opacity:.." on a rendered control is
  // caught by the same net as a stylesheet rule.
  const offenders = [];
  for (const file of shippedStyleFiles()) {
    const src = stripComments(readPkg(file));
    for (const m of src.matchAll(/(?:^|[\s;{"'`])(opacity|filter)\s*:/gm)) {
      offenders.push(`${file}: ${m[1]} declaration`);
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a shipped source declares opacity or filter. Disabled controls change COLOUR through the ` +
      `--hh-disabled-* swap; a fade is not a state:\n  ${offenders.join('\n  ')}`,
  );
});

test('no rendered control carries an inline fade: the user-visible half of the rule', () => {
  // A stylesheet can be clean while a template interpolates a fade onto one
  // control; this holds the RENDERED markup, every kit in every state.
  const allStates = ['ready', ...KIT_STATES];
  const offenders = [];
  for (const [tag, Kit] of KITS) {
    for (const state of allStates) {
      const markup = render(Kit, state);
      for (const m of markup.matchAll(/style="[^"]*(opacity|filter)[^"]*"/gi)) {
        offenders.push(`${tag}/${state}: ${m[0]}`);
      }
      for (const b of markup.match(/<button[^>]*>/g) ?? []) {
        if (b.includes('style=')) offenders.push(`${tag}/${state}: a button carries an inline style: ${b}`);
      }
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `rendered markup carries an inline fade. Disabled controls change COLOUR through the ` +
      `--hh-disabled-* swap; a fade is not a state:\n  ${offenders.join('\n  ')}`,
  );
});

test('the state word is never hidden: no hiding rule may target the word classes', () => {
  // The word is the state's cue when colour is gone; CSS must not be able to
  // delete it while the colours stay. The sanctioned hiding pattern
  // (.visually-hidden) is reserved for words paired with a VISIBLE cue, and it
  // is not allowed to touch these classes.
  const wordClasses = ['state-label', 'banner-text', 'review-title'];
  const hiding = /display:\s*none|visibility:\s*hidden|content-visibility|font-size:\s*0(?![.\d])|clip-path|position:\s*absolute|(?<![.\w-])width:\s*0(?![.\d])|(?<![.\w-])height:\s*0(?![.\d])|opacity/;
  for (const file of shippedStyleFiles()) {
    const src = stripComments(readPkg(file));
    // The selector is the text since the previous block's closing brace, so it
    // is sliced from the previous match's end rather than captured by a leading
    // [^{}]* group: on the long brace-free runs in the committed harness pages
    // that group backtracks quadratically and costs bun's whole test timeout
    // (three pages made this one gate exceed five seconds on its own). The
    // slice below is the same text the captured group held, in linear time.
    let at = 0;
    for (const m of src.matchAll(/\{([^{}]*)\}/g)) {
      const [selector, body] = [src.slice(at, m.index), m[1]];
      at = m.index + m[0].length;
      for (const cls of wordClasses) {
        if (!selector.includes(`.${cls}`)) continue;
        assert.doesNotMatch(
          body,
          hiding,
          `${file}: the rule for .${cls} hides the state word (colour never encodes state alone, and the word is the cue)`,
        );
      }
    }
  }
  // And the rendered word never carries an inline hiding style. Only the five
  // designed states carry a state word; ready shows the plain body.
  for (const [tag, Kit] of KITS) {
    for (const state of KIT_STATES) {
      const markup = render(Kit, state);
      const word = markup.match(/class="[^"]*(state-label|banner-text|review-title)[^"]*"[^>]*>/);
      assert.ok(word, `${tag}/${state}: no state word element at all`);
      assert.ok(!word[0].includes('style='), `${tag}/${state}: the state word carries an inline style`);
    }
  }
});

test('the disabled pairs the swap produces clear AA in both halves', () => {
  // WCAG exempts disabled controls; this package does not. The gate in
  // contrast.test.mjs holds the painted pair; this holds the sheet's halves to
  // declaring the family in BOTH, each half its own decision.
  const blocks = sheetBlocks();
  for (const [name, body] of [['dark half', blocks.dark], ['light half', blocks.light]]) {
    const parsed = parseDeclarations(body);
    for (const t of ['--hh-disabled-fill', '--hh-disabled-ink', '--hh-disabled-line']) {
      assert.ok(t in parsed, `${t} must be declared in the ${name}`);
    }
  }
});

// ── the icon geometry gate, held locally until the registry lands ────────────────

test('state-icons.ts is path geometry only: the non-geometry reject list', () => {
  // The registry format this package will adopt (src/icons/README.md) rejects
  // anything that is not path data. The five marks here are held to the same
  // list now: no script, no event attribute, no style, no external reference,
  // no embedded image, no animation element.
  const src = readPkg('src/state-icons.ts');
  for (const banned of [
    /<script/i,
    /\son[a-z]+\s*=/i,
    /<style/i,
    /(href|xlink:href|url\()/i,
    /data:image/i,
    /<(animate|animateTransform|animateMotion|set)\b/i,
  ]) {
    assert.ok(!banned.test(src), `state-icons.ts matched the non-geometry reject list: ${banned}`);
  }
  // Every quoted string inside a paths array is path data starting with a
  // moveto; strings outside the arrays are not judged.
  const arrays = src.match(/paths:\s*\[[^\]]*\]/g) ?? [];
  assert.ok(arrays.length >= 5, 'expected the five state marks to declare paths');
  for (const arr of arrays) {
    for (const d of arr.matchAll(/'([^']+)'/g)) {
      assert.match(d[1], /^[Mm]/, `a geometry string does not start as path data: ${d[1].slice(0, 30)}`);
    }
  }
});

test('every icon renders as currentColor stroke geometry with a text alternative available', () => {
  for (const name of Object.keys(STATE_ICONS)) {
    const markup = renderTemplate(icon(name, `label for ${name}`));
    assert.match(markup, /stroke="currentColor"/, `${name} must draw in currentColor`);
    assert.ok(!/#[0-9a-f]{3,8}/i.test(markup), `${name} must carry no colour of its own`);
    assert.match(markup, /<title>/, `${name} rendered with a label must carry a text alternative`);
    const bare = renderTemplate(icon(name));
    assert.match(bare, /aria-hidden="true"/, `${name} rendered bare must be hidden from assistive tech`);
  }
});

// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-add-dedupe.ts; adapted for HH.

// _pos-add-dedupe.ts: DIB-553. Is this activation a NEW request, or the echo of the one before it?
// Pure; no DOM, no timers, no ambient clock.
//
// THE MEASUREMENT THAT PUT THIS HERE. The ticket reads "single click adds a product", and a single
// click already did: there is no `dblclick` handler anywhere in this frontend. What reproducing it
// on the real till found is the other half: a genuine DOUBLE-click is two `click` events with
// nothing between them, and both add paths dispatched `pos-add-product` twice. Measured on the
// seeded counter at 1440: ONE double-click on a hot-list tile put the product on the ticket at
// **quantity 2**. A person who double-clicks out of habit: which the tester says they do: was
// selling two of something every time they meant one, on the surface that takes the money.
//
// WHY A SEPARATE MODULE. All THREE add surfaces need the identical rule (`pos-search-drop.ts`,
// `pos-hot-list.ts` and `pos-product-grid.ts`). Two copies of a timing rule is two places for it to
// drift; three inline copies is what actually happened, and the one that got missed was the default
// surface.
//
// WHAT THEY ACTUALLY DISPATCH FROM (round 3: the first version of this comment said "every one of
// them dispatches from a real `<button>`" and that was asserted, not checked). Counting render sites
// rather than surfaces there are SIX guarded add affordances, and only TWO are real buttons:
//
// <button> pos-search-drop.ts result row · pos-hot-list.ts tile
// <div role="button" tabindex=0> pos-product-grid.ts search-result CARDS and ROWS, and browse
// ROWS and CARDS: four sites, each with a hand-rolled
// Enter/Space keydown that calls the same add() the click does
//
// The point the sentence was making survives the correction, and this is why it survives rather
// than being assumed: a mouse double-click and two fast Enter presses have to arrive at the guard
// identically, and on the hand-rolled sites they do: `@click` fires for both mouse and the
// synthesised activation, and the `keydown` branch calls the same `add()`/`addHit()` the click does.
// That is a structural fact about these four sites, checked by the census test below, which asserts
// every `role="button"` here carries `tabindex="0"` AND its own Enter/Space branch. The 260ms → 1 /
// 305ms → 2 boundary sweep was run on the search-drop `<button>`; it has not been re-run on the
// grid's divs, and this comment does not claim it has. What was wrong was stating a fact about the
// markup that nobody had walked. And a rule written inline in a Lit component cannot
// be unit-tested at all in this repo: decorators are a syntax error under the type-stripping runner.
// Here, both the rule and the dispatch are exercised against a DRIVEN clock, which is also the house
// rule about time.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not stop you adding the same product twice on purpose;
// it stops the SECOND HALF OF ONE GESTURE. The window is short enough that a deliberate second
// press is through it, and the quantity stepper on the line is the affordance for "make that four".

/**
 * The echo window, in milliseconds.
 *
 * WHAT IS MEASURED AND WHAT IS NOT. The lower bound is measured: a genuine double-click is two
 * `click` events with an inter-click gap in the low hundreds of milliseconds (platform double-click
 * thresholds sit at ~500ms), and on this till one double-click on a tile rang up quantity 2. The
 * upper bound: "no deliberate second press ever lands inside 300ms": is NOT measured, and the
 * first version of this comment asserted it as though it were. It is a judgement, and the honest
 * form of it is: a second press inside 300ms is AMBIGUOUS, and the guard resolves the ambiguity
 * toward one unit because a wrong extra unit is money and a missing one is a keystroke.
 *
 * That trade is only defensible because the swallowed press is ANSWERED. It is not: on the keyboard
 * especially, two Enter presses on a focused tile is a natural way to ask for two, it is inherently
 * fast, and silence there reads as a dropped keystroke. `AddOnce.add` therefore fires
 * `pos-add-echo`, which the terminal renders as the same line flash a real add gets plus one
 * sentence in the role="status" line: see `echoAddMessage` in `_pos.ts`.
 *
 * Cheapest reversal if a yard disagrees: this constant. `isEchoAdd` already takes the window as a
 * parameter, so a per-surface value costs nothing if the mouse and the keyboard ever need to differ.
 */
export const ADD_DEDUPE_MS = 300;

/** The last activation a surface dispatched. A fresh surface has never dispatched one. */
export interface LastAdd {
  id: string;
  at: number;
}

export const NO_LAST_ADD: LastAdd = { id: '', at: 0 };

/**
 * Is this activation the echo of the previous one?
 *
 * ONLY for the SAME product id, and only inside the window. Two different products in quick
 * succession are a person working fast: a counter picking three items off one category board does
 * it in well under a second, and swallowing any of those would be a far worse bug than the one this
 * fixes. An EMPTY id is never an echo: a row the wire did not identify cannot be matched against
 * anything, and refusing to add it would lose the line entirely.
 *
 * `now` is passed in. There is no `Date.now()` in this file, so every case below is decidable on a
 * driven clock and the tests do not sleep.
 */
export function isEchoAdd(last: LastAdd, id: string, now: number, window = ADD_DEDUPE_MS): boolean {
  if (!id || !last.id || id !== last.id) return false;
  const gap = now - last.at;
  // A clock that went BACKWARDS (a system time change, a test handing an older instant) must not be
  // read as "0ms ago" and swallow a real press: only a gap that is genuinely inside the window is
  // an echo.
  if (gap < 0) return false;
  return gap < window;
}

/** The state to carry forward after an activation was dispatched. */
export function recordAdd(id: string, now: number): LastAdd {
  return { id, at: now };
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// THE SURFACE-SIDE ADDER: the rule, the state and the dispatch in ONE place.
//
// WHY THIS EXISTS AND WHY THE COMPONENTS NO LONGER HOLD THE DECISION (bounce 2, foreman D6/D7).
// The first cut left each component holding `lastAdd` and writing `if (isEchoAdd(this.lastAdd,…))`
// inline. Two things were wrong with that and both were found by a refuter, not by a test:
//
// 1. A THIRD add surface existed: pos-search-drop's result rows, which the terminal's own
// comment calls "the default surface … a glance": and it was never guarded. One double-click
// on a search row sold TWO units. Three inline copies of a rule is three chances to miss one,
// and the one missed was the one the counter types into.
// 2. The rule was defended by `assert.match(src, /isEchoAdd\(this\.lastAdd,/)`. Applying
// `if (false && isEchoAdd(this.lastAdd, …))` to both components turned the behaviour off with
// the source text intact and ALL SIX tests stayed green. That is D6 verbatim.
//
// Moving the decision AND the dispatch in here fixes both. A component now has no expression of
// its own to short-circuit: the only component-level mutation left is to stop calling `add()` and
// construct the event by hand, which is a STRUCTURAL change that the tree-walk in
// _pos-add-dedupe.test.mjs sees (it walks every.ts in app/src, not two named files). Everything
// inside `add()` is exercised against a real EventTarget on a driven clock, so a behavioural
// mutation: `isEchoAdd` forced false, the echo notice dropped, `reset()` made a no-op: turns
// tests RED rather than leaving them green.
//
// WHAT AN ECHO NOW SAYS. It used to say nothing at all: a real add flashes the line and announces
// "Black Mulch · 1 → 2 CYD", a suppressed one was silent, so a deliberate fast second press looked
// like the till had dropped it. `add()` dispatches `pos-add-echo` on the swallowed press and the
// terminal turns that into the same flash on the line that IS there plus one sentence in the
// role="status" line the counter is already reading. Silence was the weakest part of the guard,
// and it is the part that mattered most on the keyboard: two Enter presses on a focused tile is a
// natural way to ask for two, it is inherently fast, and it must not be answered with nothing.

/** The event a surface fires when it actually adds. */
export const ADD_EVENT = 'pos-add-product';
/** The event a surface fires when it SWALLOWED an add as the second half of one gesture. */
export const ADD_ECHO_EVENT = 'pos-add-echo';

/**
 * One add surface's echo guard: the last activation, the rule, and the dispatch.
 *
 * `now` is injectable for the same reason the pure rule takes it: every case is decidable on a
 * driven clock and no test sleeps.
 */
export class AddOnce {
  private last: LastAdd = NO_LAST_ADD;
  /** The ticket generation the held activation belongs to. -1 = this surface has not been told yet. */
  private epoch = -1;

  /**
   * Dispatch this activation, unless it is the echo of the one before it.
   *
   * Returns `true` when `pos-add-product` went out, `false` when the press was swallowed (and
   * `pos-add-echo` went out instead, so the swallow is never silent).
   */
  add(host: EventTarget, id: string, product: unknown, now: number = Date.now()): boolean {
    if (isEchoAdd(this.last, id, now)) {
      host.dispatchEvent(
        new CustomEvent(ADD_ECHO_EVENT, {
          detail: { id, product },
          bubbles: true,
          composed: true,
        }),
      );
      return false;
    }
    this.last = recordAdd(id, now);
    host.dispatchEvent(
      new CustomEvent(ADD_EVENT, { detail: { product }, bubbles: true, composed: true }),
    );
    return true;
  }

  /**
   * The ticket changed underneath this surface, so the line this guard is protecting may not be
 * there any more: forget the last activation.
   *
   * WHY (the scoping smell the refuter labelled, now closed): `last` is surface state and the cart
 * is the terminal's. Driving the component directly: click, then `term.cart = []`, then click:
   * produced ZERO lines, because the guard was still holding a press whose line no longer existed.
   * New Sale and Park re-render enough to reset it in practice; "in practice" is not a rule. The
   * terminal now passes a `ticketEpoch` that it bumps on every cart change that is NOT the add
   * itself (New Sale, remove line, edit quantity, resume a parked ticket, apply a pop-out
   * snapshot), and each surface calls this when it changes. Reprice is deliberately NOT one of
   * them: reprice runs inside addProduct, so bumping there would cancel the guard on the very
   * gesture it exists for.
   */
  reset(): void {
    this.last = NO_LAST_ADD;
  }

  /**
   * The ticket this surface is guarding, as the terminal currently understands it.
   *
 * WHY THE COMPARISON MOVED IN HERE (round 3, D6 again: and the bounce was right).
   * Round 2's hand-in said "no component holds an expression a `false &&` can switch off", and
   * every one of the three guarded surfaces held exactly one:
   *
   * if (changed.has('ticketEpoch')) this.adder.reset();
   *
   * Mutated to `if (false && changed.has('ticketEpoch'))`, all 3311 frontend tests stayed green,
 * because the census defended that line with `assert.match(code, /this\.adder\.reset\(\)/)`:
   * a regex `false &&` satisfies perfectly. And it is not an equivalent mutant: measured, a re-add
   * at a 121ms gap after the line was removed LANDS at baseline and is SWALLOWED under the
   * mutation. A real press lost to a stale latch, one line downstream of where D6 was closed.
   *
   * So the comparison is here, where a driven clock and a real EventTarget can reach it, and the
   * components call this unconditionally. They now genuinely hold no expression to short-circuit:
   * the only mutation left to them is DELETING the call, which is structural, which the tree-walk
   * in _pos-add-dedupe.test.mjs sees and the browser probe measures.
   *
 * Idempotent on purpose: `willUpdate` runs on every render, and only a CHANGED generation is a
   * ticket that moved. A surface's first sighting resets nothing that exists.
   */
  ticketAt(epoch: number): void {
    if (epoch === this.epoch) return;
    this.epoch = epoch;
    this.reset();
  }
}

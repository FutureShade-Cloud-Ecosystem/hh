-- 0210 — the four demo flows stop at the interesting part, and this carries them through the write.
--
-- APPROVED by the commander, 3 Sep 2026 (max(branch) 0209 + 1, confirmed by `ls backend/migrations
-- | tail -1` at the moment of writing, which answered
-- 0209_how_long_a_driver_stays_signed_in_is_a_dealers_knob.sql).
--
-- ── WHAT THIS IS FOR, IN ONE SENTENCE ───────────────────────────────────────────────────────────
--
-- The demo landing is PRE-AUTH and picking a flow signs you in as that flow's role (the commander's
-- second ruling), and there is no chapter chooser — 0192's tour opens on the first chapter the
-- caller's role owns. So a landing tile is only as good as the chapter behind it, and the four that
-- matter all stopped one press short of the thing a buyer came to see:
--
--   role                  chapter        seeded  stopped at
--   INSIDE_SALES          sixty-seconds       5  the customer and the F12 keycap — never a tender,
--                                               never a completed sale
--   LOGISTICS_MANAGER     requested           3  three steps, all anchored `tag:hos-dispatch-surface`
--                                               — the whole screen, three times, no assignment
--   YARD_TEAM             pick-to-proof       3  three steps, all `tag:hos-yard-console`, likewise
--   SUPER_ADMIN           your-rules          3  the keys. Its own promise names three things —
--                                               "The keys, the columns and the limits" — and the
--                                               limits were never reached.
--
-- The commander's third ruling is that a flow RUNS END TO END INCLUDING THE WRITE: "A checkout
-- completes a sale; assignment assigns; staging stages; the AR setting saves." Every one of the
-- four below was driven twice in a real browser as the role that owns it — once by hand to find
-- the anchors, and once THROUGH THIS LAYER, pressing the tour's own controls and the app's, in the
-- order these rows put them in. The second pass is the one that counts, and its writes landed:
-- order #SO-TRT-12 / invoice #INV-11 PAID against Scrubname0002 Ridge Scrubname0017, run #DEMO-RUN-TRT-R6
-- crewed TRK-1 · Owen Sharpe and dispatched, DEMO-KGN-O06 staged at Scrubname0002 5 · STG-8ANF69 with both
-- lines pulled in full, and "Escalate after, days past due · 95 · 90" in the receivables change
-- history with the operator's name against it. Every anchor resolved; not one bubble rested. The
-- transcripts and their frames are in verification/demo-flows/<flow>/.
--
-- ── DEEPENED IN PLACE, NOT RIVALLED ─────────────────────────────────────────────────────────────
--
-- No new chapter. Four chapters already own this ground and a fifth beside any of them would split
-- a role's tour in two — which the pill's ordering would then have to arbitrate, and 0192's ids are
-- add-only precisely so a person's bookmark keeps meaning what it meant. Every one of the fourteen
-- step_keys that existed on 3 Sep is still here, with its id unchanged; nineteen steps are added
-- around them, and the four chapters run 10, 8, 7 and 8 steps.
--
-- ONE OF THE FOUR IS NOT WHAT ITS BRIEF CALLED IT, and it is recorded here rather than smoothed
-- over. The AR flow's stated ancestor was `your-rules`, described as "the AR settings flow". On disk
-- `your-rules` is the PLATFORM KEY configurator, owned by SUPER_ADMIN, routed at `#admin/hotkeys`.
-- Two ways out, and the second is taken:
--
--   * re-point the chapter at receivables and re-role it to FINANCIAL_CONTROLLER. That is a rename
--     in everything but the id — it would leave the platform administrator with no chapter of their
--     own, and it would retire, silently, the only walkthrough this deployment has of the
--     platform → dealer → person ladder.
--   * CARRY IT THROUGH TO ITS OWN THIRD NOUN. "The keys, the columns and the limits are yours to
--     set" — the keys were seeded, the limits never were. So the chapter now runs keys → limits →
--     the change history: the same override ladder, applied to money, ending at the row that
--     records who moved it. The role stays SUPER_ADMIN, which is also what makes the landing tile
--     work: FINANCIAL_CONTROLLER already owns `chase-invoice` at position 6, so a controller-owned
--     AR-settings chapter would have had to out-sort it, and arranging that is a worse thing to
--     depend on than a chapter simply being the only one its role owns.
--
--   The screen is reached identically either way: the "Receivables rules" door is drawn on
--   isAdmin(), which is tier 1 + GLOBAL scope — SUPER_ADMIN and FINANCIAL_CONTROLLER both.
--
-- ── THE ANCHORS ARE THE WHOLE JOB, AND MOST OF THE OLD ONES WERE DEAD ───────────────────────────
--
-- 0192's header says the anchors "were each checked to exist in the tree before being written here
-- — a chapter that points at an element that does not exist is worse than no chapter". They were.
-- The screens then moved. Measured on the live stack on 3 Sep, before this file:
--
--   FOUR OF THE FIVE `sixty-seconds` ANCHORS NO LONGER RESOLVED. The till is now a four-step
--   accordion (hos-pos-flow-steps / hos-pos-step-card), and `pos-search-drop-host`, `pos-browse`,
--   `customer-card` and `pay-hotkey-chip` live inside panels that are COLLAPSED unless that step is
--   the open one. getClientRects() answers zero for all four, which is exactly the test
--   _tour.ts:isVisibleMatch applies, so the bubble rested in the corner for four steps out of five
--   and nobody would have called it broken — it just pointed at nothing.
--
--   SIX OF THE SIX dispatch/yard ANCHORS RESOLVED AND SAID NOTHING. `tag:hos-dispatch-surface` and
--   `tag:hos-yard-console` are the whole screen; a bubble aimed at a 1328×507 box is a bubble aimed
--   at the viewport. 0192 chose those tags because "THREE OF THE SURFACES THE NINE CHAPTERS VISIT
--   HAVE ZERO data-testid ATTRIBUTES … a testid-only grammar would have meant editing those files
--   to make a tour possible, which is a tour changing the app to describe it." That was the right
--   call for a chapter that NARRATES a screen and the wrong one for a flow that has to be FOLLOWED,
--   so this lane did the additive half of the work instead: twenty data-testid names across
--   twenty-one edit sites in seven files, every one of them an attribute added to a control that
--   already existed — none renamed, none removed, no behaviour, no style and no layout touched.
--   They are listed in §0 below so a reader of this file never has to go hunting for the other
--   half.
--
-- ── WHY THE EXISTING ROWS ARE UPSERTED AND NOT LEFT ALONE ───────────────────────────────────────
--
-- 0200 used ON CONFLICT DO NOTHING, and for accounts that is right. Here every statement is
-- ON CONFLICT … DO UPDATE, which will overwrite a line a platform operator edited live — the one
-- thing 0192 built this substrate to allow. It is still correct, for a reason that is about the
-- column and not about the row: an ANCHOR is not copy. It is the join between a row and a DOM node,
-- and four of these chapters currently name nodes that are gone. Leaving an edited row alone would
-- preserve somebody's better sentence attached to an anchor that resolves to nothing, which is the
-- worse of the two failures. The lines move with them because a line and its anchor are one thought
-- — "Type only where what you pulled differed" is meaningless pointed at anything but the lines
-- table.
--
-- REVERT COST: this file writes ROWS ONLY — no CREATE, no ALTER, no DROP, and it touches no table
-- outside 0192's two. A revert is a DELETE of the nineteen added (chapter_key, step_key) pairs plus
-- a re-run of 0192's own INSERT for the fourteen it restates, and it loses the four finished flows
-- and nothing else. Re-running this file changes nothing that was not already changed.

-- ── §0 · the attributes this file depends on, and where they were added ────────────────────────
--
-- Recorded here because a step is a row and its anchor is a promise about a file this migration
-- cannot see. Every one is an ADDITION to an element that already existed and already had its own
-- listener, id or class; nothing was renamed, moved or restyled.
--
--   app/src/pos-terminal.ts        pos-customer-mode      the Walk-in / Account tiles
--                                  pos-paytiles           the four tender tiles
--                                  pos-complete-sale      the CTA that rings the sale
--   app/src/dispatch-surface.ts    dispatch-tab-<sub>      all six sub-tabs, mirroring their ids
--   app/src/dispatch-loadboard.ts  dispatch-card-<id>      each quick card, mirroring data-card
--   app/src/dispatch-board.ts      dispatch-truck-pick     the run's truck select
--                                  dispatch-driver-pick    the run's driver select
--                                  dispatch-assign-stop    "Put on a run…" on a waiting load
--                                  dispatch-run-dispatch   the run's Dispatch button
--                                  dispatch-next-day       the day stepper
--   app/src/yard-console.ts        yard-board              the working queue
--                                  yard-advance            the pane's ONE act (Claim/Start/Stage)
--                                  yard-lines              the lines table
--                                  yard-stage-location     "Scrubname0032 are you putting it?"
--                                  yard-staged-at          the bay and reference, after staging
--                                  yard-put-on-board       an unstarted order's only button
--                                  yard-mint-tag           the will-call rack tag
--   app/src/ar-landing.ts          ar-settings-open        the "Receivables rules" door
--   app/src/ar-landing-settings.ts as-<field>              every numeric rule, mirroring its id
--                                  as-save-edges           the Save that writes the two edges
--
-- AND ONE THING THE LAYER ITSELF DID NOT DO. §4's last step points at the receivables change
-- history, which sits about 1300px down a settings pane. The anchor resolved; placeBubble clamped
-- the bubble to the bottom of the WINDOW, because a bubble may never leave the viewport — a coach
-- mark on screen, readable, aimed five hundred pixels below the fold. No arithmetic could have
-- saved it, so tour-layer.ts now scrolls an anchor into view before it becomes the anchor, using
-- scrollPlan (the app's one piece of scroll arithmetic, already shared by the till's accordion and
-- yard-console.ts:838) and _tour.ts's new anchorOffScreen — which is narrower than scrollPlan's own
-- question on purpose: two thirds of this grammar's anchors ARE the surface and are taller than the
-- window by design, and asking to reveal all of one moved the till's opening bubble from y=16 to
-- y=773 for a 21px scroll that revealed nothing. Nothing moves unless the person cannot see the
-- anchor at all.
--
-- TWO OF THEM ARE NOT SPENT BY ANY STEP BELOW, and both were still checked to resolve rather than
-- written on faith: `yard-put-on-board` is the only control an order nobody has started offers, and
-- `yard-mint-tag` is the will-call rack tag — measured on DEMO-KGN-O19 and DEMO-KGN-O10
-- respectively. They are the two ends of the yard's queue that §3's flow deliberately does not
-- walk, and §3 says why.
--
-- ONE ANCHOR WAS WANTED AND COULD NOT BE ADDED: the yard's ticket pane. Its opening tag is asserted
-- verbatim by yard-wave4-fixes.test.mjs (F21), and the only place an attribute fits is inside that
-- literal; wrapping the pane's children in a div instead would sit between `.ticket` and them and
-- break the print rule that puts a rack tag on paper (`.ticket > *:not(.tag)`). The step that
-- wanted it was cut rather than aimed somewhere weaker — see §3.

-- ═════════════════════════ 1 · counter-checkout — an empty till to a completed sale ═════════════
--
-- `sixty-seconds`, INSIDE_SALES, #sales/pos. Five seeded steps kept by id; five added.
--
-- THE SHAPE OF THIS CHAPTER IS THE SHAPE OF THE SCREEN, and that is new. The till is now a numbered
-- accordion — Customer, Items, Payment, Summary — and only one panel is open at a time, so a step
-- whose anchor lives in a closed panel cannot resolve however correct the id is. Two consequences
-- are designed for rather than worked around:
--
--   * `customer` is the ONLY step in this chapter that does not need to open anything, because
--     answering it opens the next panel for you: choosing Walk-in or an account marks step 1 done
--     and expands Items with the caret in the scan box (pos-terminal.ts's own behaviour, not the
--     tour's). Measured — after picking an account, pos-scan-input, pos-hot-list and pos-browse are
--     all visible in the same frame.
--   * `payment` is a CLICK step on the panel HEAD, not on anything inside it. The head is always
--     visible; the tiles are not until it is pressed. That is why the step exists at all: without
--     it the tender step would open onto a collapsed panel and rest in the corner.
--
-- WHY `customer` ADVANCES ON NEXT AND NOT ON CLICK. Its anchor covers both answers, and they behave
-- differently: Walk-in resolves in the same frame, Account opens a modal picker and the person is
-- then searching for a customer for several seconds — far past the twenty-frame anchor hunt. A
-- CLICK step would advance into a step whose anchor is behind a dialog. NEXT lets the person finish
-- the thought first, which is also the honest instruction: the line asks a question with two right
-- answers.
INSERT INTO platform_tour_steps (chapter_key, step_key, position, route, anchor, line, advance) VALUES
  ('sixty-seconds', 'open',     0, '#sales/pos', 'tag:hos-pos-terminal',      'This is the till. Everything here happens on one screen.', 'NEXT'),
  ('sixty-seconds', 'customer', 1, '',           'testid:pos-customer-mode',  'Put it on an account, or leave it a cash sale.',           'NEXT'),
  ('sixty-seconds', 'scan',     2, '',           'testid:pos-scan-input',     'Scan or type a code. The line prices itself.',             'NEXT'),
  ('sixty-seconds', 'browse',   3, '',           'testid:pos-browse',         'No code to hand? Browse the yard from here.',              'NEXT'),
  ('sixty-seconds', 'lines',    4, '',           'testid:pos-lines',          'Every line lands here, priced, with its weight beside it.', 'NEXT'),
  -- The one press that opens the money. CLICK, because the panel it opens is where the next two
  -- steps live and it opens in the same frame.
  ('sixty-seconds', 'payment',  5, '',           'testid:step-head-payment',  'Step three is the money. Open it.',                        'CLICK'),
  ('sixty-seconds', 'tender',   6, '',           'testid:pos-paytiles',       'Cash, card, on account — or split it across two.',         'NEXT'),
  ('sixty-seconds', 'pay',      7, '',           'testid:pay-hotkey-chip',    'F12 takes the payment. F4 moves you along.',               'NEXT'),
  -- THE WRITE. One press posts the order, the invoice and the payment together; the confirmation
  -- panel painted 106 ms later on the measured run, well inside the twenty frames the layer waits.
  ('sixty-seconds', 'ring',     8, '',           'testid:pos-complete-sale',  'One press. Order, invoice and payment post together.',     'CLICK'),
  ('sixty-seconds', 'sold',     9, '',           'testid:sold-panel',         'Sold. The sale number and the invoice, on the screen.',    'NEXT')
ON CONFLICT (chapter_key, step_key) DO UPDATE
  SET position = EXCLUDED.position, route = EXCLUDED.route, anchor = EXCLUDED.anchor,
      line = EXCLUDED.line, advance = EXCLUDED.advance, updated_at = now();

-- ═════════════════════════ 2 · truck-assignment — an uncrewed run, dispatched ═══════════════════
--
-- `requested`, LOGISTICS_MANAGER, #dispatch/dispatch. Three seeded steps kept by id; five added.
--
-- THE ASSIGNMENT IS NOT ON THE LOAD BOARD, and that is why this chapter had none. The load board
-- moves a load BETWEEN trucks (Move → a list of runs); the thing that gives a run a truck and a
-- driver is the DAY BOARD, whose own refusal states the rule in words the tour did not have to
-- invent: "A run needs a truck and a driver before it can be dispatched." So the chapter crosses
-- from one sub-tab to the other, and it crosses by POINTING AT THE TAB rather than by routing:
-- `#dispatch/dispatch` is the whole address the frozen hash contract has for this screen (a sub-tab
-- is not in the grammar and this file is not the place to widen it), so a route step could not
-- express the move. A CLICK step on the tab can, and it is the better teaching anyway — the person
-- presses the tab themselves.
--
-- WHY TOMORROW. The demo world plants exactly the situation the flow is about: #DEMO-RUN-TRT-R6,
-- tomorrow, PLANNED, no truck and no driver, with one committed load sitting in "Not on a truck
-- yet". Today's board has no uncrewed run, so the day stepper is a real step and not a detour. The
-- stepper is NEXT rather than CLICK because pressing it refetches the board, and the crew selects
-- do not exist until that answer lands.
--
-- WHAT "COMMIT" MEANS HERE, since the chapter is named for the distinction. The seeded line is kept
-- verbatim and now points at the Dispatch button, which is the moment the promise is made: before
-- it, a run is an intention with a truck on it; after it, the yard loads and a driver has a sheet.
-- The date-commit control the chapter's first two steps talk about is elsewhere on the load board
-- and only appears on a load nobody has committed yet — the demo world has none, which is itself
-- the point those two steps make.
INSERT INTO platform_tour_steps (chapter_key, step_key, position, route, anchor, line, advance) VALUES
  ('requested', 'open',     0, '#dispatch/dispatch', 'tag:hos-dispatch-surface',          'Every load for the day, in the order it runs.',           'NEXT'),
  ('requested', 'ask',      1, '',                   'testid:dispatch-card-card.requested', 'A salesperson can ask for a truck. Only dispatch commits.', 'NEXT'),
  ('requested', 'dayboard', 2, '',                   'testid:dispatch-tab-board',         'The crews live on the day board. Open it.',               'CLICK'),
  ('requested', 'tomorrow', 3, '',                   'testid:dispatch-next-day',          'Step to tomorrow. One run there has nobody on it.',       'NEXT'),
  ('requested', 'truck',    4, '',                   'testid:dispatch-truck-pick',        'Give the run its truck.',                                 'NEXT'),
  ('requested', 'driver',   5, '',                   'testid:dispatch-driver-pick',       'And its driver. A run needs both before it can roll.',    'NEXT'),
  ('requested', 'load',     6, '',                   'testid:dispatch-assign-stop',       'Now the load. Put the one that is waiting onto the run.', 'NEXT'),
  -- THE WRITE, and the last step: pressing it both dispatches the run and finishes the chapter.
  ('requested', 'commit',   7, '',                   'testid:dispatch-run-dispatch',      'Committing is the promise. It is one person''s to make.', 'CLICK')
ON CONFLICT (chapter_key, step_key) DO UPDATE
  SET position = EXCLUDED.position, route = EXCLUDED.route, anchor = EXCLUDED.anchor,
      line = EXCLUDED.line, advance = EXCLUDED.advance, updated_at = now();

-- ═════════════════════════ 3 · pick-to-stage — a queued ticket, staged ═════════════════════════
--
-- `pick-to-proof`, YARD_TEAM, #yard/yard. Three seeded steps kept by id; four added.
--
-- THE YARD CONSOLE HAS ONE BUTTON AND IT CHANGES ITS NAME. Claim → Start picking → Stage → Hand
-- over is one element (`yard-advance`) relabelled at each status, which is the pane's own stated
-- rule ("ONE ACT, ONE BUTTON"). Three steps below therefore share an anchor, deliberately: the
-- person presses the same place three times and the button tells them what it is doing. That also
-- makes the CLICK chain safe, which the tour needs — the element survives each reload, so the next
-- step's anchor is already resolved the instant the previous one is pressed.
--
-- WHERE THIS CHAPTER STOPS, AND WHY IT IS NOT THE RACK TAG. The brief asked for "ending at the rack
-- tag". It cannot end there and be runnable, and the reason is a real property of the product
-- rather than a missing testid: `yard-mint-tag` is drawn only for a WILL_CALL ticket that is
-- already STAGED (isWillCall && pickupState === 'STAGED'), and every WILL_CALL ticket in the demo
-- world — DEMO-TRT-O08, DEMO-TRT-O11, DEMO-KGN-O10 — is already staged, so no ticket in this world
-- can be picked from the queue AND end at a tag. Measured on the ticket this flow actually runs:
-- after staging, `yard-mint-tag` has no match at all. The attribute is added anyway, because it is
-- the right anchor the day a will-call arrives unpicked.
--
-- So the chapter ends where the WRITE ends: `yard-staged-at`, the bay and the reference. That is
-- not a consolation — it is the sentence the counter reads back down the phone, and the rack tag
-- has its own chapter (`will-call`, step `tag`) already saying the line about it. The one testid
-- carries both arms the pane can draw — "Staged at Scrubname0002 5 · STG-8ANF69" on a delivery, "Ref
-- DEMO-STG-015" on a will-call — because they are the same fact and the tour must not have to know
-- which branch rendered.
--
-- AND THERE IS NO STEP ABOUT THE PANE, which there was in the first cut of this file. Its anchor
-- could not be added (see §0), and aiming that step somewhere weaker would have been an
-- explanatory step buying an anchor rather than the other way round. Cutting it costs nothing:
-- measured, the pane's one act paints two frames — 13 ms — after a queue row is pressed, so the
-- CLICK on the queue lands straight on a resolved Claim with nineteen frames of the layer's hunt
-- to spare. The chapter is seven steps and every one of them is an act.
INSERT INTO platform_tour_steps (chapter_key, step_key, position, route, anchor, line, advance) VALUES
  -- CLICK on the queue itself: any row inside it is on the click's composed path, so pressing the
  -- ticket a picker actually wants is what advances. That is the whole idiom — the person does the
  -- thing — and it is why the line now ends with the instruction it was missing.
  ('pick-to-proof', 'open',   0, '#yard/yard', 'testid:yard-board',           'Everything waiting to be picked, whoever queued it. Open one.', 'CLICK'),
  ('pick-to-proof', 'claim',  1, '',           'testid:yard-advance',         'Claim it. A ticket has one picker at a time.',                  'CLICK'),
  ('pick-to-proof', 'start',  2, '',           'testid:yard-advance',         'Start picking. Now the lines take what you pulled.',            'CLICK'),
  ('pick-to-proof', 'lines',  3, '',           'testid:yard-lines',           'Type only where what you pulled differed.',                     'NEXT'),
  ('pick-to-proof', 'where',  4, '',           'testid:yard-stage-location',  'Scrubname0032 are you putting it? The counter reads this back.',        'NEXT'),
  -- THE WRITE. The seeded line said "Staging mints the rack tag, and not before", which is true of
  -- a will-call and of nothing else on this screen; this says what staging does on every ticket.
  ('pick-to-proof', 'stage',  5, '',           'testid:yard-advance',         'Staging writes the lines and parks the pallet, in one press.',  'CLICK'),
  ('pick-to-proof', 'parked', 6, '',           'testid:yard-staged-at',       'The bay and the reference. That is what the counter reads.',    'NEXT')
ON CONFLICT (chapter_key, step_key) DO UPDATE
  SET position = EXCLUDED.position, route = EXCLUDED.route, anchor = EXCLUDED.anchor,
      line = EXCLUDED.line, advance = EXCLUDED.advance, updated_at = now();

-- ═════════════════════════ 4 · ar-settings — a receivables rule, and who moved it ══════════════
--
-- `your-rules`, SUPER_ADMIN, #admin/hotkeys → #finance/ar. Three seeded steps kept by id; five
-- added. See the header for why this chapter and not a new one.
--
-- THE THREE KEY STEPS NOW POINT AT CONTROLS. All three were `tag:hos-platform-hotkeys` — the whole
-- screen, three times — while the screen itself carries sixty-odd published testids. `tiers` points
-- at Set (where an override is made) and `reset` at Unbind (where a level is cleared), so the two
-- sentences about the ladder are said next to the two buttons that walk it. `open` keeps the tag,
-- because that step IS about the screen.
--
-- THE CROSSING IS BY ROUTE, not by a tab press, because these are two different surfaces in the
-- frozen contract and both are addressable. Every step carries its own route rather than only the
-- first: Back is a control this layer draws, and a chapter that spans two screens must put the
-- reader back on the screen the earlier step was talking about. Re-stating the route a person is
-- already on is a no-op — the layer writes the hash only when it differs.
--
-- WHY THE ESCALATION EDGE AND NOT THE TIER. `ar_tier` is a select and its history rows read
-- "Everything, including promises and disputes" → "Aging and reminders", which is a sentence about
-- a feature set. The escalation edge is a NUMBER a controller genuinely argues about, its history
-- row reads "90 → 95", and the screen states the constraint before the save rather than after it
-- ("The warning edge comes first; escalation is the later of the two"). A demo of a change trail is
-- only as good as the change being obviously a change.
INSERT INTO platform_tour_steps (chapter_key, step_key, position, route, anchor, line, advance) VALUES
  ('your-rules', 'open',    0, '#admin/hotkeys', 'tag:hos-platform-hotkeys',        'The keys this deployment ships with, before any dealer edits.', 'NEXT'),
  ('your-rules', 'tiers',   1, '#admin/hotkeys', 'testid:ph-set-pos.counter-pay',   'A dealer overrides these. A person overrides the dealer.',      'NEXT'),
  ('your-rules', 'reset',   2, '#admin/hotkeys', 'testid:ph-clear-pos.counter-pay', 'Reset clears your own level, so the one beneath answers.',      'NEXT'),
  ('your-rules', 'limits',  3, '#finance/ar',    'testid:ar-landing',               'Same ladder, different rule: what this dealer calls late.',     'NEXT'),
  ('your-rules', 'rules',   4, '#finance/ar',    'testid:ar-settings-open',         'The receivables rules live behind this one door.',              'NEXT'),
  ('your-rules', 'edge',    5, '#finance/ar',    'testid:as-aging_escalate_days',   'Escalation is the day a late account becomes a phone call.',    'NEXT'),
  -- THE WRITE.
  ('your-rules', 'save',    6, '#finance/ar',    'testid:as-save-edges',            'Save. The landing re-buckets every account on its next read.',  'CLICK'),
  ('your-rules', 'history', 7, '#finance/ar',    'testid:as-history',               'Every change is kept: the rule, the old value, the new, who.',  'NEXT')
ON CONFLICT (chapter_key, step_key) DO UPDATE
  SET position = EXCLUDED.position, route = EXCLUDED.route, anchor = EXCLUDED.anchor,
      line = EXCLUDED.line, advance = EXCLUDED.advance, updated_at = now();

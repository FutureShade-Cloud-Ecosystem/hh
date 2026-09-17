-- 0192 — THE GUIDED DEMO WALKTHROUGH, AS DATA.
--
-- THE RULING, verbatim (Scrubname0005, 26 Aug): "the demo in your hand Scrubname0025 for all these core workflows —
-- a guided walkthrough where you're actually in a demo deployed app but there's a little bit of a
-- facade on it that helps you walk through step by step. You've seen it a million times, you sign up
-- for a new SaaS app…"
--
-- ── WHY TYPED TABLES AND NOT 0172's surface_role_items ──────────────────────────────────────────
--
-- 0172 is where PRESENTATION CURATION lives, and a tour is presentation, so that was the obvious
-- home. Three things end it, and any one of them would:
--
--  1. THERE IS NO PLATFORM TIER IN 0172, and 0191:15-23 refused in writing to add one. `profile`
--     means JOB SHAPE — the five 0140 shapes — so a sixth value would file the DEPLOYMENT'S floor
--     alongside the job shapes rather than beneath them. That argument is unchanged here, so this
--     follows 0191's other half instead: a typed table whose rows ARE the platform default, gated on
--     IsSuperAdmin, with its own routes rather than a widened CHECK.
--  2. A STEP IS NOT (item_id, position, visible). It carries a route, an anchor, a line of copy and
--     an advance rule. Encoding those inside 0172's opaque 64-character `item_id` would be a second
--     schema hiding inside a string — the exact drift 0172's shape-only validation exists to prevent.
--  3. THE COPY MUST BE EDITABLE WITHOUT A DEPLOY. 0172's own doctrine (settings/settings.go:95-97) is
--     that item ids are opaque and their MEANING lives in a frontend registry module. Copy in a
--     frontend registry is copy that needs a deploy, which is the one thing item 1 must not need.
--
-- WHAT 0172 STILL OWNS: which chapters a DEALER shows, per job shape. That is ordinary curation and
-- it needs no new route — the settings kit's PUT/DELETE already do it under surface id
-- `tour.chapters`, kind `chip`, item_id = chapter_key, and the kind-scoped DELETE is already the
-- reset (0191's lesson: a reset is a DELETE of your own level's rows, never a write of the defaults
-- into the level below).
--
-- ── PLATFORM SCOPE IS THE ABSENCE OF A COLUMN ───────────────────────────────────────────────────
--
-- `platform_tour_chapters` and `platform_tour_steps` carry no branch_id and no dealer id. There is
-- one deployment's worth of rows and nothing in this schema can express a second, which is the same
-- shape 0191 chose for `platform_hotkeys` and for the same reason: a tier you can only have one of
-- should not have a column suggesting otherwise.
--
-- ── IDS ARE ADD-ONLY (0140's rule, restated at 0172:22) ─────────────────────────────────────────
--
-- `chapter_key` and `step_key` are half the primary key of every progress row somebody left behind
-- (0193). Add ids; retire them with `visible = false`; NEVER rename one. A rename orphans the place a
-- person had got to, silently.
--
-- ── THE RULE THIS SCHEMA CANNOT ENFORCE ─────────────────────────────────────────────────────────
--
-- `line` is capped at 72 characters here, and the Go validator refuses a longer one in words with a
-- 422 before this CHECK could 500. The cap is the design doctrine expressed where it cannot be
-- forgotten: "No sentences in chrome. UI text is 1–3 words… Helper paragraphs are banned." A tour is
-- the one place in this product that is allowed to speak in sentences at all, and the budget is that
-- each one is no longer than a headline. What the CHECK cannot know is whether the sentence is any
-- GOOD, which is why a platform operator can edit it live — see 0193's note on why that matters.
--
-- REVERT COST: this file is ADD-only — three new tables, no ALTER of anything that exists. A revert
-- is three DROPs and loses the tour's content and the dealer's curation of it. It is safe against a
-- staging database that is never wiped.

-- ── §1 · the two knobs, and why there are two ───────────────────────────────────────────────────
--
-- The 0043/0173 branch-NULL typed shape: the NULL row IS the dealer default and a branch row
-- overrides it.
--
-- `demo_mode` answers "is this deployment a demo?" and belongs to the PLATFORM operator
-- (`IsSuperAdmin` — `IsAdmin` admits the dealer's own Scrubname0012 Scrubname0019, which 0191:44-70 argues at
-- length would make the tier decoration). It defaults to FALSE, so a fresh deployment is silent and
-- a real dealer never sees a tour by accident.
--
-- `dealer_enabled` answers "does this dealer want it?" and belongs to them (`IsAdmin`). It defaults
-- to TRUE, so turning the deployment into a demo is one switch rather than two — but a dealer who
-- has been handed a demo deployment can still turn it off without needing the platform operator.
--
-- THE TOUR RENDERS ONLY WHEN BOTH ARE TRUE. The brief's requirement is that the tour is BYTE-ABSENT
-- from payloads when off, not CSS-hidden, and that is enforced one layer up: the read returns an
-- empty answer, so there is nothing to hide rather than something hidden. See the tour service.
CREATE TABLE IF NOT EXISTS tour_policy (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    branch_id      UUID UNIQUE REFERENCES locations (id) ON DELETE CASCADE,
    demo_mode      BOOLEAN NOT NULL DEFAULT false,
    dealer_enabled BOOLEAN NOT NULL DEFAULT true,
    updated_by     UUID REFERENCES users (id),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seeded so the restrictive default is VISIBLE in the data rather than merely implied by a column
-- default nobody reads (0173:60-61's posture).
INSERT INTO tour_policy (branch_id, demo_mode, dealer_enabled)
VALUES (NULL, false, true)
ON CONFLICT DO NOTHING;

COMMENT ON TABLE tour_policy IS
  'Whether the guided walkthrough renders at all. demo_mode is the PLATFORM operator''s switch (IsSuperAdmin) and defaults to FALSE so a real dealer never meets a tour by accident; dealer_enabled is the dealer''s own (IsAdmin) and defaults to TRUE. The tour is served only when both are true, and when either is false the read answers EMPTY — the payload carries no tour, rather than a tour something is hiding.';

-- ── §2 · the chapters ───────────────────────────────────────────────────────────────────────────
--
-- `role` is the role that OWNS the chapter — the person whose day it is. It is not an authorization
-- fence (the API is authorization; this is editorial), so it is a free CHECK over the role codes
-- rather than a foreign key into `roles`: a chapter about the counter is about the counter whether or
-- not that dealer has staffed an INSIDE_SALES seat.
CREATE TABLE IF NOT EXISTS platform_tour_chapters (
    chapter_key TEXT NOT NULL CHECK (chapter_key ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    position    INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    title       TEXT NOT NULL CHECK (char_length(title) BETWEEN 4 AND 48),
    -- One line, the chapter's own promise. Same 72-character budget as a step's line.
    promise     TEXT NOT NULL DEFAULT '' CHECK (char_length(promise) <= 72),
    role        TEXT NOT NULL CHECK (role IN ('INSIDE_SALES','YARD_TEAM','DRIVERS','SALES_MANAGER',
                                              'LOGISTICS_MANAGER','YARD_MANAGER','GENERAL_MANAGER',
                                              'FINANCIAL_CONTROLLER','SUPER_ADMIN')),
    -- The shell deep link the chapter opens on. The shell's hash contract is FROZEN
    -- (dashboard-nav.ts:25 NAV_REGISTRY), so this CHECK is its shape and nothing more.
    route       TEXT NOT NULL CHECK (route ~ '^#[a-z-]+(/[a-z-]+)?$'),
    visible     BOOLEAN NOT NULL DEFAULT true,
    updated_by  UUID REFERENCES users (id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chapter_key)
);

CREATE INDEX IF NOT EXISTS platform_tour_chapters_order_idx
    ON platform_tour_chapters (position, chapter_key);

COMMENT ON TABLE platform_tour_chapters IS
  'The guided walkthrough''s chapters, at PLATFORM scope — one deployment''s worth, no branch column, curated by a platform administrator. `role` is the role whose day the chapter is about; it is editorial, not an authorization fence. Ids are ADD-ONLY: retire a chapter with visible=false, never rename it, because chapter_key is half the primary key of every progress row somebody left behind.';

-- ── §3 · the steps ──────────────────────────────────────────────────────────────────────────────
--
-- THE ANCHOR GRAMMAR, and it is deliberately only two shapes.
--
--   'testid:<value>'  a data-testid. 480 of them already exist and several surfaces are thick with
--                     them (ar-invoice-register 73, pos-terminal 64, quote-workarea 61), so where one
--                     exists it is the most precise thing to point at.
--   'tag:<hos-…>'     a custom element. THREE OF THE SURFACES THE NINE CHAPTERS VISIT HAVE ZERO
--                     data-testid ATTRIBUTES — dispatch-loadboard.ts, yard-console.ts and
--                     dashboard-page.ts — so a testid-only grammar would have meant editing those
--                     files to make a tour possible, which is a tour changing the app to describe it.
--                     A tag anchor always resolves, because the shell mounted the element.
--   ''                no anchor: the step speaks about the screen rather than about a control, and
--                     the bubble sits in its resting place.
--
-- A CSS CLASS IS NOT IN THE GRAMMAR, on purpose. `.brand`, `.home-btn`, `.who` are private to the
-- component that renders them and the union-train-12 copy passes have already rewritten several of
-- them once. A published attribute and a registered element name are contracts; a class is not.
--
-- `advance` is how the person moves on. 'NEXT' is an explicit control the tour draws. 'CLICK' waits
-- for the anchor itself to be pressed, which is what makes this a walkthrough rather than a slide
-- deck — the person does the thing. 'ROUTE' waits for the shell to arrive somewhere.
CREATE TABLE IF NOT EXISTS platform_tour_steps (
    chapter_key TEXT NOT NULL REFERENCES platform_tour_chapters (chapter_key) ON DELETE CASCADE,
    step_key    TEXT NOT NULL CHECK (step_key ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    position    INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    -- '' means "wherever you already are"; otherwise a shell deep link and only a shell deep link.
    route       TEXT NOT NULL DEFAULT '' CHECK (route = '' OR route ~ '^#[a-z-]+(/[a-z-]+)?$'),
    anchor      TEXT NOT NULL DEFAULT '' CHECK (anchor = '' OR anchor ~ '^(testid|tag):[A-Za-z0-9._:-]{1,64}$'),
    -- ONE LINE. The doctrine's chrome budget, expressed where it cannot be forgotten. The Go
    -- validator refuses a longer one in words at 422 before this CHECK could 500.
    line        TEXT NOT NULL CHECK (char_length(line) BETWEEN 1 AND 72),
    advance     TEXT NOT NULL DEFAULT 'NEXT' CHECK (advance IN ('NEXT','CLICK','ROUTE')),
    updated_by  UUID REFERENCES users (id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chapter_key, step_key)
);

CREATE INDEX IF NOT EXISTS platform_tour_steps_order_idx
    ON platform_tour_steps (chapter_key, position, step_key);

COMMENT ON COLUMN platform_tour_steps.anchor IS
  'What the step points at, in exactly two shapes: "testid:<value>" for a published data-testid, or "tag:<hos-element>" for a registered custom element. A CSS class is deliberately not in the grammar — a class is private to the component that renders it. Empty means the step speaks about the screen rather than a control.';
COMMENT ON COLUMN platform_tour_steps.line IS
  'The one line the step says, capped at 72 characters because the design doctrine bans sentences in chrome and a tour''s budget is a headline. Editable live by a platform administrator — that is the point of the copy being data.';

-- ── §4 · the nine chapters ──────────────────────────────────────────────────────────────────────
--
-- The titles are the client's own, verbatim. The ROUTES are real entries in the shell's frozen hash
-- contract (dashboard-nav.ts:25) and the ANCHORS were each checked to exist in the tree before being
-- written here — a chapter that points at an element that does not exist is worse than no chapter.
--
-- ROLE NOTE, recorded rather than glossed: the seeded database has NO DRIVERS account and NO
-- YARD_TEAM account (eight of the fourteen roles have a login). Chapters owned by those two roles are
-- seeded because the role is the editorial truth about whose day it is; a deployment that wants to
-- RUN them as that role needs the accounts, which 0193 plants for a demo deployment only.
INSERT INTO platform_tour_chapters (chapter_key, position, title, promise, role, route) VALUES
  ('sixty-seconds',  0, 'In and out in sixty seconds', 'A cash sale, start to receipt, without leaving the keyboard.', 'INSIDE_SALES',         '#sales/pos'),
  ('what-we-keep',   1, 'How much am I saving?',       'What the yard keeps on this ticket, and who may see it.',      'GENERAL_MANAGER',      '#sales/pos'),
  ('bulk-board',     2, 'The bulk board',              'The material you sell every day, one key away.',               'INSIDE_SALES',         '#sales/pos'),
  ('requested',      3, 'Requested is not committed',  'A salesperson asks for a truck. A dispatcher decides.',        'LOGISTICS_MANAGER',    '#dispatch/dispatch'),
  ('pick-to-proof',  4, 'Pick to proof',               'One job from the pick list to a signature at the door.',       'YARD_TEAM',            '#yard/yard'),
  ('will-call',      5, 'Will-call without the wait',  'The pallet is tagged and waiting before they park.',           'YARD_MANAGER',         '#yard/yard'),
  ('chase-invoice',  6, 'Chase the invoice without the blast', 'One overdue account, one named contact, one notice.',  'FINANCIAL_CONTROLLER', '#finance/ar'),
  ('own-tile',       7, 'Build your own tile',         'The number you check first, on the screen you open first.',    'GENERAL_MANAGER',      '#home'),
  ('your-rules',     8, 'Your yard, your rules',       'The keys, the columns and the limits are yours to set.',       'SUPER_ADMIN',          '#admin/hotkeys')
ON CONFLICT (chapter_key) DO NOTHING;

INSERT INTO platform_tour_steps (chapter_key, step_key, position, route, anchor, line, advance) VALUES
  -- 1 · In and out in sixty seconds
  ('sixty-seconds', 'open',     0, '#sales/pos', 'tag:hos-pos-terminal',        'This is the till. Everything here happens on one screen.', 'NEXT'),
  ('sixty-seconds', 'scan',     1, '',           'testid:pos-search-drop-host', 'Scan or type a code. The line prices itself.',              'NEXT'),
  ('sixty-seconds', 'browse',   2, '',           'testid:pos-browse',           'No code to hand? Browse the yard from here.',               'NEXT'),
  ('sixty-seconds', 'customer', 3, '',           'testid:customer-card',        'Put it on an account, or leave it a cash sale.',            'NEXT'),
  ('sixty-seconds', 'pay',      4, '',           'testid:pay-hotkey-chip',      'F12 takes the payment. F4 moves you along.',                'NEXT'),
  -- 2 · How much am I saving?
  ('what-we-keep',  'open',     0, '#sales/pos', 'tag:hos-pos-terminal',        'Margin lives on the ticket, not in a report you run later.', 'NEXT'),
  ('what-we-keep',  'eye',      1, '',           'testid:margin-eye',           'One eye hides every cost figure on the screen.',            'NEXT'),
  ('what-we-keep',  'gated',    2, '',           'tag:hos-pos-terminal',        'A clerk never gets these numbers. They are not sent.',      'NEXT'),
  -- 3 · The bulk board
  ('bulk-board',    'open',     0, '#sales/pos', 'testid:pos-cards',            'Your busiest material, on the first screen of the day.',    'NEXT'),
  ('bulk-board',    'curate',   1, '',           'testid:pos-browse',           'What sits here is yours to choose, in Screen settings.',    'NEXT'),
  -- 4 · Requested is not committed
  ('requested',     'open',     0, '#dispatch/dispatch', 'tag:hos-dispatch-surface', 'Every load for the day, in the order it runs.',        'NEXT'),
  ('requested',     'ask',      1, '',           'tag:hos-dispatch-surface',    'A salesperson can ask for a truck. Only dispatch commits.', 'NEXT'),
  ('requested',     'commit',   2, '',           'tag:hos-dispatch-surface',    'Committing is the promise. It is one person''s to make.',   'NEXT'),
  -- 5 · Pick to proof
  ('pick-to-proof', 'open',     0, '#yard/yard', 'tag:hos-yard-console',        'Everything waiting to be picked, whoever queued it.',       'NEXT'),
  ('pick-to-proof', 'lines',    1, '',           'tag:hos-yard-console',        'Type only where what you pulled differed.',                'NEXT'),
  ('pick-to-proof', 'stage',    2, '',           'tag:hos-yard-console',        'Staging mints the rack tag, and not before.',               'NEXT'),
  -- 6 · Will-call without the wait
  ('will-call',     'open',     0, '#yard/yard', 'tag:hos-yard-console',        'A pickup is a job too. It just leaves on their truck.',     'NEXT'),
  ('will-call',     'tag',      1, '',           'tag:hos-yard-console',        'The tag is live from staged until they drive away.',        'NEXT'),
  -- 7 · Chase the invoice without the blast
  ('chase-invoice', 'open',     0, '#finance/ar', 'testid:ar-landing',          'The accounts that need a call today, worst first.',         'NEXT'),
  ('chase-invoice', 'queue',    1, '',            'testid:ar-escalation-queue', 'Past your escalation line. These are the real ones.',       'NEXT'),
  ('chase-invoice', 'contact',  2, '',            'testid:ar-ap-name',          'One named contact. Never a blast to every address.',        'NEXT'),
  -- 8 · Build your own tile
  ('own-tile',      'open',     0, '#home',      '',                            'Your first screen. It is yours, not ours.',                 'NEXT'),
  ('own-tile',      'add',      1, '',           '',                            'Add the number you check first. Drop the ones you never do.', 'NEXT'),
  -- 9 · Your yard, your rules
  ('your-rules',    'open',     0, '#admin/hotkeys', 'tag:hos-platform-hotkeys', 'The keys this deployment ships with, before any dealer edits.', 'NEXT'),
  ('your-rules',    'tiers',    1, '',               'tag:hos-platform-hotkeys', 'A dealer overrides these. A person overrides the dealer.',  'NEXT'),
  ('your-rules',    'reset',    2, '',               'tag:hos-platform-hotkeys', 'Reset clears your own level, so the one beneath answers.',  'NEXT')
ON CONFLICT (chapter_key, step_key) DO NOTHING;

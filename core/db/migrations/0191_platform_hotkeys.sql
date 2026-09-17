-- 0191_platform_hotkeys.sql — D-111: the PLATFORM tier of the hotkey model.
--
-- THE RULING, verbatim: "i think we need a platform admin configurator for hotkeys". What was
-- missing was not an editor — train 14 shipped the dealer's editor — but the LEVEL ABOVE IT. Until
-- now the deepest level any hotkey had was a `var` in Go source, which means the only way for
-- Hardscape House to change what a key does across the deployment was a release. This table is the
-- level a platform operator can actually edit, and it sits between the dealer's override and the
-- code-level BisTrack floor:
--
--     user legend preference   (exists — dismiss/restore; the keys stay live either way)
--     dealer override          (exists — train 14's counter editor, per-action rebinding)
--   → PLATFORM default roster  (THIS TABLE — new)
--     shipped BisTrack floor   (pos/hotkeys.go defaultHotkeys, dashboard/hotkeys.go DefaultHotkeys)
--
-- WHY A TYPED TABLE AND NOT A NEW `profile` VALUE IN 0172. The obvious cheap move was to widen
-- surface_role_items' profile CHECK with a sixth value 'platform' and read it as the layer below
-- the role rows. It was rejected: `profile` means JOB SHAPE everywhere else in this tree — 0140
-- maps every role_code (SUPER_ADMIN included) onto one of counter/yard/driver/manager/admin, and
-- settings.ValidateProfile's own 422 sentence enumerates them as job shapes. A 'platform' value
-- would put the deployment's floor in a PK slot ALONGSIDE the job shapes rather than BENEATH them,
-- and would make that sentence a lie about what a profile is. So this follows the other house
-- idiom instead — the 0043 pos_policy / 0173 quote_policy_terms shape: a typed table whose rows
-- ARE the default, CHECK-constrained on shape, that a narrower row overrides.
--
-- WHY ONE TABLE FOR BOTH SURFACES, AND WHY THAT IS NOT A MERGE. There are two hotkey substrates in
-- this product and they must stay two: the app shell's app-wide NAVIGATION map (dealer_hotkeys,
-- 0164/0171 — chord spelling "F1", "Ctrl+Shift+K", action ids like sale.cash.new) and the counter's
-- in-sale ACTION roster (surface_role_items kind `action` on pos.counter, 0172 — chord spelling
-- "f4", "shift-f4", action ids like step-next). They have different action namespaces, different
-- chord grammars and different F8 answers, and a previous conflation of them is on record as a
-- real defect (the till's CTA advertised F8 while the counter actually paid on F12). The `surface`
-- column here keeps them apart BY CONSTRUCTION: the primary key is (surface, action_id), every read
-- is per-surface, and nothing in this schema can express a row that belongs to both. What the one
-- table buys is a single place a platform operator edits and a single registry to keep in step —
-- which is exactly what "bring the app shell's dealer_hotkeys map under the same registry" asked
-- for, and no more than that.
--
-- THE UNIQUE INDEX IS THE COLLISION REFUSAL, LAST. A roster where two actions claim one key
-- resolves by row order, and the loser is dropped silently — a key that simply stopped working,
-- with nothing on any screen to say why. The service refuses the clash in words first, naming the
-- action it would break; this index is the floor under that, so a hand-written row cannot do what
-- the editor refuses to do.
--
-- ── WHAT ELSE THIS MIGRATION DOES TO dealer_hotkeys, AND WHY ──────────────────────────────────
--
-- (1) RETIRE F8 → sale.pay FROM THE SHELL MAP. This is the standing recommendation carried since
--     train 14 (NOTES-UNION-TRAIN14.md) and restated in train 15. Pay lives on the COUNTER roster
--     (F12, by the client's own BisTrack ruling). The shell advertising a second, different pay key
--     is how the till's CTA came to display a stale one. Retiring the SHELL's copy leaves exactly
--     one surface that can ever name a pay key.
--
--     The DELETE is scoped to BOTH columns on purpose. 0171's `ON CONFLICT DO NOTHING` was an
--     explicit promise to a dealer who had already claimed F8 for something else: that row is
--     theirs and survives. Only the row this product put there is removed.
--
-- (2) LIFT THE UNTOUCHED 0164 SEED UP TO THE PLATFORM TIER. The five rows 0164 seeded were never a
--     dealer's choices — 0164's own header calls them "PLACEHOLDERS AWAITING THE CLIENT'S OFFICIAL
--     LIST" and records that no edit endpoint exists, so nothing has ever written this table but
--     the migrations. They are the PLATFORM's defaults that happened to be stored in the dealer's
--     table because the platform tier had nowhere to live. Now it does, so they move.
--
--     `updated_by IS NULL` is the discriminator, and it is a sound one rather than a convenient
--     one: dealer_hotkeys has carried updated_by since 0164 precisely so an admin edit would be
--     attributable, and every row an editor ever writes stamps it. A NULL there means "no human
--     ever chose this" — which is the definition of a shipped default. Anything a person wrote
--     stays exactly where it is and keeps winning, which is what a dealer override means.
--
--     After this runs, a fresh deployment's dealer_hotkeys is EMPTY, which is the honest state: the
--     dealer has overridden nothing. The shell map they see is the platform roster, and a platform
--     operator's edit reaches them without a release — the whole point of D-111.

CREATE TABLE IF NOT EXISTS platform_hotkeys (
    surface    text    NOT NULL CHECK (surface IN ('app.shell', 'pos.counter')),
    action_id  text    NOT NULL CHECK (char_length(action_id) BETWEEN 1 AND 64),
    binding    text    NOT NULL CHECK (char_length(binding) BETWEEN 1 AND 32),
    position   integer NOT NULL DEFAULT 0 CHECK (position >= 0),
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (surface, action_id)
);

-- One key, one action, per surface. See the header: this is the floor under the service's
-- in-words refusal, not a substitute for it.
CREATE UNIQUE INDEX IF NOT EXISTS platform_hotkeys_binding_uq
    ON platform_hotkeys (surface, binding);

-- ── THE SHIPPED PLATFORM ROSTERS ──────────────────────────────────────────────────────────────
--
-- Both are copies of the code-level floors that already exist, so day one behaviour is unchanged
-- and the only thing this migration adds is the ABILITY to change them. The Go copies stay: a
-- build running ahead of its migrations, or a database read that fails, still gets keys.
--
-- SHELL — the 0164 five, spelled in dashboard/hotkeys.go's grammar (canonical "F1", "Ctrl+Shift+K").
-- F8 IS ABSENT: sale.pay is retired here, and nothing else is rebound onto F8. An unbound key
-- teaches nothing; a rebound one teaches a trained hand the wrong action.
INSERT INTO platform_hotkeys (surface, action_id, binding, position) VALUES
    ('app.shell', 'sale.cash.new',    'F1', 0),
    ('app.shell', 'sales.tab.new',    'F2', 1),
    ('app.shell', 'order.quick.open', 'F3', 2),
    ('app.shell', 'search.focus',     'F4', 3),
    ('app.shell', 'assist.open',      'F9', 4)
ON CONFLICT (surface, action_id) DO NOTHING;

-- COUNTER — the BisTrack roster (ruling R1, 27 Aug, the client's own words: "bistrack does F4 to
-- move to the next step and then F12 to take a payment"), spelled in pos/hotkeys.go's grammar
-- (lowercase, modifiers in shift-ctrl-alt order). F8 is absent here too, and has been since R1.
INSERT INTO platform_hotkeys (surface, action_id, binding, position) VALUES
    ('pos.counter', 'step-next', 'f4',       0),
    ('pos.counter', 'step-back', 'shift-f4', 1),
    ('pos.counter', 'pay',       'f12',      2),
    ('pos.counter', 'park',      'f9',       3),
    ('pos.counter', 'search',    'f2',       4)
ON CONFLICT (surface, action_id) DO NOTHING;

-- (1) The retirement, scoped to both columns — see the header.
DELETE FROM dealer_hotkeys WHERE binding = 'F8' AND action_id = 'sale.pay';

-- (2) The lift — see the header. Runs LAST so the platform rows above already exist when the
-- dealer's copies of them go: at no instant between these two statements is the shell map empty.
DELETE FROM dealer_hotkeys WHERE updated_by IS NULL;

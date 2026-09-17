-- 0159: DIB-67 (SP-04) — quote scope-gap rules, AS DATA.
-- Owner: internal/quoteassist. Additive, non-breaking, self-contained. Follows 0157.
-- (0158 belongs to the fix-472 lane and is deliberately not taken here.)
--
-- ═══════════════════════════════════════════════════════════════════════════════
-- WHY A TABLE AND NOT A GO SLICE
-- ═══════════════════════════════════════════════════════════════════════════════
-- The ticket asks for "AI quote completeness checking". The AI part is a distraction: what a
-- hardscape counter actually needs is the yard's own installation knowledge, written down —
-- a paver surface needs a base, a bedding course, jointing sand and an edge restraint; a
-- retaining wall needs a levelling pad and drainage stone behind it. That knowledge is not a
-- model's opinion and it is not a programmer's opinion either. It belongs to whoever runs the
-- yard, it differs between yards, and it changes when the product mix changes.
--
-- A hardcoded rule set has three failure modes this table does not have:
--
--   1. THE YARD CANNOT CORRECT IT. Dibbits sells porcelain pavers that are set on a pedestal
--      system with no jointing sand at all. A wrong rule that cannot be switched off trains
--      reps to ignore the whole panel — and a suggestion panel people have learned to ignore
--      is worse than no panel, because it is now also taking up space.
--   2. IT CANNOT BE AUDITED. "Why did it tell me that?" has to be answerable by pointing at a
--      row, not by reading Go.
--   3. IT DRIFTS FROM THE CATALOGUE. Applications are catalogue data (products.application);
--      a rule set that names them in code is a second copy of a vocabulary somebody else owns.
--
-- So the rules are rows, the evaluator is a pure function over those rows (quoteassist/scope.go
-- Evaluate), and the ONE place `enabled` is honoured is that function. Deleting every row in
-- this table produces exactly zero suggestions and changes nothing else — which is the property
-- TestScope_NoRules_NoFindings pins.

CREATE TABLE IF NOT EXISTS quote_scope_rules (
    -- A stable code, not a uuid. It is what the API, the admin listing, the dismissal state and
    -- the tests all name a rule by, and a rule that is renamed is a different rule.
    code       TEXT PRIMARY KEY,

    -- What the rep reads. Both are DATA rather than templates: a rule whose sentence is
    -- generated in Go from its categories cannot be made polite, and politeness is the whole
    -- point of a suggestion (DIB-425: a gap is advice, not a fault).
    title      TEXT NOT NULL,
    advice     TEXT NOT NULL,

    -- WHEN: the quote carries a line whose product application is ANY of these → the rule is in
    -- play. EXPECT: the quote must also carry a line whose application is ANY of these, or the
    -- rule reports a gap.
    --
    -- ARRAYS, NOT ONE ROW PER PAIR. "A paver surface needs jointing sand" is one piece of
    -- knowledge, and products.application spells that surface two ways in the real catalogue
    -- ('Paver' for the pavers, 'Patio' for the slabs). Two rows would fire twice for one gap and
    -- a rep would be told the same thing twice; one row with a set fires once.
    --
    -- Values are compared UPPER-CASE and trimmed on both sides (scope.go normaliseApp), because
    -- products.application is free text that reads 'Paver'/'Jointing' today and could read
    -- 'PAVER' after any import. A rule set that breaks on capitalisation is a rule set that
    -- silently stops firing.
    when_apps   TEXT[] NOT NULL,
    expect_apps TEXT[] NOT NULL,

    -- The search term the one-click "add it" affordance drops into the product picker.
    --
    -- A TERM, NOT A CATEGORY FILTER, on purpose: it goes through the SAME ranked counter search
    -- (product.CounterSearch) the rep already uses and already trusts, including its synonyms and
    -- its typo net. A second, rule-only product-selection path would be a second thing that can
    -- disagree with the picker about what is in the catalogue.
    --
    -- It is also allowed to match NOTHING, and that is information rather than a bug: this
    -- catalogue has no edge-restraint SKU today, so SURFACE_NEEDS_EDGING opens a picker that
    -- honestly says "No products match 'edge restraint'." — which is the signal purchasing needs,
    -- surfaced by the people who keep hitting it.
    suggest_query TEXT NOT NULL DEFAULT '',

    -- enabled is the admin's switch and the ONLY thing that silences a rule. It is a column
    -- rather than a delete so that switching a rule off is reversible and legible: a missing row
    -- is "we never had this rule", enabled=false is "we decided against it".
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,

    -- Presentation order. Deterministic ordering matters more than it looks: a suggestion list
    -- that reshuffles between two reads of the same quote reads as a flickering bug.
    sort_order INTEGER NOT NULL DEFAULT 100,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Scrubname0033 last touched it. Nullable because the SEEDED rows were nobody's decision — they are
    -- the shipped defaults, and stamping them with a fabricated actor would be a lie in an audit
    -- column. ON DELETE SET NULL: removing a login does not delete the yard's rule.
    updated_by UUID REFERENCES users (id) ON DELETE SET NULL,

    -- An empty set on either side would be a rule that either never fires or can never be
    -- satisfied. Both are nonsense, and nonsense in config data is how a panel starts lying.
    CONSTRAINT quote_scope_rules_when_nonempty   CHECK (cardinality(when_apps) > 0),
    CONSTRAINT quote_scope_rules_expect_nonempty CHECK (cardinality(expect_apps) > 0),
    CONSTRAINT quote_scope_rules_title_nonempty  CHECK (btrim(title) <> ''),
    CONSTRAINT quote_scope_rules_advice_nonempty CHECK (btrim(advice) <> '')
);

-- The only read: every rule, in order, including the disabled ones (the admin listing needs
-- them, and Evaluate is the one place `enabled` is applied). Small table, one index for order.
CREATE INDEX IF NOT EXISTS quote_scope_rules_order_idx
    ON quote_scope_rules (sort_order, code);

-- ═══════════════════════════════════════════════════════════════════════════════
-- THE SEEDED DEFAULTS, AND THE ONE TEST THEY ALL HAD TO PASS
-- ═══════════════════════════════════════════════════════════════════════════════
-- A rule earns a row only if LEAVING THE ITEM OUT MEANS THE INSTALLATION FAILS. Not "the yard
-- sold less" — fails. That single filter is what keeps this panel worth reading:
--
--   jointing sand omitted   → the joints wash out in the first storm. FAILS.
--   granular base omitted   → the surface settles and heaves. FAILS.
--   bedding course omitted  → the units cannot be levelled. FAILS.
--   edge restraint omitted  → the field spreads and the perimeter walks. FAILS.
--   wall drainage omitted   → hydrostatic pressure pushes the wall out. FAILS.
--   wall levelling pad      → the courses run out of level and the wall leans. FAILS.
--   cap adhesive omitted    → the caps come off. FAILS.
--
-- DELIBERATELY NOT SEEDED, though the catalogue would happily sell them:
--
--   sealer        (DBS-SIKA-SEALER)  — a sealed patio and an unsealed patio both work.
--   geotextile    (DBS-GEO-FABRIC)   — good practice, site-dependent, not universal.
--   path lighting (DBS-INLITE-LED)   — nobody's patio failed for want of a light.
--
-- Those three are UPSELLS. A completeness checker that flags upsells is a sales-target widget
-- wearing an accessibility role, and the first rep who works that out stops reading all of it.
-- The yard can add them here in one INSERT if it disagrees; that is what the table is for.
--
-- The applications named below are the ones the REAL seeded catalogue uses
-- (SELECT DISTINCT application FROM products): Base, Bedding, Coping, Drainage, Jointing,
-- Landscape, Lighting, Patio, Paver, Sealing, Step, Wall. EDGING, RESTRAINT and ADHESIVE are
-- named by rules whose product does not exist yet — see suggest_query above for why that is
-- deliberate and not an oversight.
INSERT INTO quote_scope_rules (code, title, advice, when_apps, expect_apps, suggest_query, enabled, sort_order) VALUES
  ('SURFACE_NEEDS_BASE',
   'Granular base for the paved area',
   'This quote has a paved surface but no granular base. A paver or slab field set without a compacted base settles — worth checking whether the crew is supplying their own.',
   ARRAY['PAVER','PATIO'], ARRAY['BASE'], 'limestone screenings', TRUE, 10),

  ('SURFACE_NEEDS_BEDDING',
   'Bedding course under the units',
   'There is a paved surface here with no bedding layer. Units are screeded onto HPB or concrete sand — without it they cannot be brought to level.',
   ARRAY['PAVER','PATIO'], ARRAY['BEDDING'], 'HPB bedding', TRUE, 20),

  ('SURFACE_NEEDS_JOINTING',
   'Jointing sand for the paved area',
   'A paved area is quoted with no jointing sand. Joints left unfilled wash out in the first heavy rain — one bag covers roughly 100 sq ft on a standard joint.',
   ARRAY['PAVER','PATIO'], ARRAY['JOINTING'], 'polymeric sand', TRUE, 30),

  ('SURFACE_NEEDS_EDGING',
   'Edge restraint around the perimeter',
   'Nothing on this quote restrains the edge of the paved area. Without it the outer courses spread and the field walks apart over a season or two.',
   ARRAY['PAVER','PATIO'], ARRAY['EDGING','RESTRAINT'], 'edge restraint', TRUE, 40),

  ('WALL_NEEDS_BASE',
   'Levelling pad under the wall',
   'A retaining wall is quoted with no base material for the levelling pad. The first course sets the whole wall — it needs compacted aggregate under it.',
   ARRAY['WALL'], ARRAY['BASE'], 'limestone screenings', TRUE, 50),

  ('WALL_NEEDS_DRAINAGE',
   'Drainage stone behind the wall',
   'This wall has no drainage stone behind it. Water trapped behind a retaining wall is what pushes walls over; clear stone in the chimney is how that pressure gets out.',
   ARRAY['WALL'], ARRAY['DRAINAGE'], '3/4 clear stone', TRUE, 60),

  ('CAPS_NEED_ADHESIVE',
   'Adhesive for the caps and steps',
   'Caps or steps are quoted with no adhesive. They are not held down by anything else — the first person to sit on an unglued cap moves it.',
   ARRAY['COPING','STEP'], ARRAY['ADHESIVE'], 'concrete adhesive', TRUE, 70)
ON CONFLICT (code) DO NOTHING;

-- ON CONFLICT DO NOTHING, not DO UPDATE. Re-running this migration must never overwrite a
-- yard's edit — an admin who reworded SURFACE_NEEDS_JOINTING for their own crews keeps their
-- wording, and an admin who switched CAPS_NEED_ADHESIVE off keeps it off. The whole file is
-- re-runnable: every CREATE is IF NOT EXISTS and the INSERT is idempotent by key.

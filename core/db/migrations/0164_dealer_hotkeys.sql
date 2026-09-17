-- 0164_dealer_hotkeys.sql — DIB-496 (ruling D-095): keyboard shortcuts are DEALER-level DATA.
--
-- The ruling: hotkeys are configured ONCE for the whole company and rolled across every terminal —
-- not per-user preference. The client's example is the contract: "F1 always opens a cash sale",
-- meaning F1 does the same thing at every desk in the building, which is the entire value of a
-- muscle-memory key. So the map lives in ONE table with no user column, the same shape the other
-- dealer-wide chrome settings take (quick_action_settings, 0151): a keyed row an admin can change
-- without a release.
--
--   dealer_hotkeys   binding → action id. The binding is a canonical chord spelling
--                    ("F1", "Ctrl+Shift+K" — internal/dashboard/hotkeys.go owns the grammar and
--                    normalises on read, so a hand-written row in a sloppier spelling still lands).
--                    The action id names an entry in the FRONTEND action registry
--                    (app/src/hotkey-logic.ts). Stored as an opaque string and validated only for
--                    SHAPE, deliberately — the same decision 0149 made for pinned tab keys: the
--                    client owns what a surface/action IS, so the server does not keep a copy of
--                    that list to drift out of date. A row naming an action this build does not
--                    know does NOTHING, silently, which is also what a hotkey aimed at a screen
--                    the caller's role cannot open does.
--
-- WHY THERE IS NO EDIT ENDPOINT YET. D-095 asked for the MECHANISM with seeded defaults; the admin
-- editing surface is explicitly out of scope. Until it ships, changing a dealer's map is an
-- UPDATE/INSERT here — which is why every column an admin surface would need (updated_by,
-- updated_at) already exists, so the surface is an endpoint away, not a migration away.
--
-- ── SEED DEFAULTS — PLACEHOLDERS AWAITING THE CLIENT'S OFFICIAL LIST ───────────────────────────
-- These five are sensible guesses assembled from the walkthrough conversation, NOT the client's
-- signed-off bindings. They exist so the mechanism demonstrably works end to end on day one.
-- Replace freely; ON CONFLICT DO NOTHING means a re-run never stomps a dealer's edits.
--
-- Key choices avoid chords the browser will not give up: F5 (reload), F6 (address bar), F11
-- (fullscreen) and F12 (devtools) are skipped on purpose. F1's browser-help default is suppressible
-- from a keydown handler, and the client asked for F1 by name.
--
-- The action ids are the frontend registry's spellings (app/src/hotkey-logic.ts):
--   sale.cash.new     F1 — jump to the till and start a fresh cash sale (the client's example)
--   sales.tab.new     F2 — open a NEW Scrubname0027 tab (a second instance, DIB-457's explicit duplicate)
--   order.quick.open  F3 — open the quick order write-up surface
--   search.focus      F4 — jump to the product catalogue with the search box focused
--   assist.open       F9 — open the AI assistant

CREATE TABLE IF NOT EXISTS dealer_hotkeys (
    binding    text PRIMARY KEY,
    action_id  text NOT NULL,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO dealer_hotkeys (binding, action_id) VALUES
    ('F1', 'sale.cash.new'),
    ('F2', 'sales.tab.new'),
    ('F3', 'order.quick.open'),
    ('F4', 'search.focus'),
    ('F9', 'assist.open')
ON CONFLICT (binding) DO NOTHING;

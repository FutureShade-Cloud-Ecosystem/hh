-- 0151_quick_action_visibility.sql — DIB-434: whether a global quick-action RENDERS is DATA.
--
-- The ticket asked for one button ("+ Scrubname0026" in the top bar: jump to the till and start a fresh
-- ticket from anywhere). Scrubname0005's caveat is the reason this file exists: a tech admin must be able
-- to turn that button off — globally, or for one job shape — WITHOUT a release. So the visibility
-- of a chrome affordance is stored the same way the cockpit stores what a role's home screen looks
-- like (0140/0141): two tables and a resolver, not an `if role == …` in a Lit template.
--
--   quick_action_settings            action id → the GLOBAL on/off switch. Off here beats every
--                                    per-profile row: one toggle takes the button off every screen
--                                    in the building.
--   quick_action_profile_visibility  action id + job shape → whether that shape sees it. The five
--                                    shapes are 0140's, and they are the same CHECK set, because a
--                                    sixth shape would have to be added to both or the two configs
--                                    would disagree about who a person is.
--
-- Action ids are the strings in internal/dashboard/quickaction.go and they are the primary key of
-- every row here: a rename orphans an admin's saved configuration and silently restores the
-- default. Add actions, never rename them — the same rule tile ids carry.
--
-- WHY THE DEFAULT IS WHAT IT IS. The ruling: selling roles see it, yard/driver do not. counter and
-- manager are the selling shapes. admin is ON for the reason 0140 gives for its own admin row —
-- "the person who wires it is the person who checks it": a tech admin who cannot see the button
-- cannot verify the switch they were just given. yard and driver are OFF: a picker's screen is not
-- a till, and a driver's phone is even less of one.

CREATE TABLE IF NOT EXISTS quick_action_settings (
    action_id  text PRIMARY KEY,
    enabled    boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quick_action_profile_visibility (
    action_id  text NOT NULL,
    profile    text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    visible    boolean NOT NULL,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (action_id, profile)
);

-- ── the shipped action ─────────────────────────────────────────────────────────────────────────
-- ONE row, and that is the point of building it as a registry: the palette this is the seed of does
-- not exist yet, and adding the second entry is an INSERT plus a catalog line, not a new component.
INSERT INTO quick_action_settings (action_id, enabled) VALUES
    ('sale.new', true)
ON CONFLICT (action_id) DO NOTHING;

INSERT INTO quick_action_profile_visibility (action_id, profile, visible) VALUES
    ('sale.new', 'counter', true),   -- the desk the ticket was written from
    ('sale.new', 'manager', true),   -- branch/sales managers ring sales too
    ('sale.new', 'admin',   true),   -- configures it, therefore must be able to see it
    ('sale.new', 'yard',    false),
    ('sale.new', 'driver',  false)
ON CONFLICT (action_id, profile) DO NOTHING;

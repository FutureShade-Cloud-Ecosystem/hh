-- 0141_dashboard_profile_home.sql — DIB-354: the role-based screen takeover, as CONFIG.
--
-- A driver opening the app on a phone should land on the run sheet. A yard hand on a tablet should
-- land on the pick queue. Neither should have to find the cockpit, read tiles they cannot act on,
-- and then navigate. That is one line of behaviour and it could have been one line of code —
-- `if role == "DRIVERS" { … }` — which is exactly the line that gets copied, drifts, and then has
-- to be edited in three places when the branch hires a second kind of driver.
--
-- So it is a table. landing_unit / landing_module are the SHELL's own keys, so a takeover resolves
-- to an ordinary #unit/module hash: the frozen deep-link contract, not a second router.
--
-- mobile_only is the honest default. The yard manager at a desk wants the cockpit; the same person
-- on the tablet in the yard wants the queue. Same role, same login, different screen — and the
-- thing that differs is the viewport, not the person.

CREATE TABLE IF NOT EXISTS dashboard_profile_home (
    profile        text PRIMARY KEY CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    landing_unit   text,
    landing_module text,
    takeover       boolean NOT NULL DEFAULT false,
    mobile_only    boolean NOT NULL DEFAULT true,
    updated_by     uuid REFERENCES users (id),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- A takeover with nowhere to land is a blank screen on someone's phone. The service refuses it
    -- too (SaveProfileHome); this is the copy of that rule the database can enforce on its own.
    CONSTRAINT dashboard_profile_home_landing_required
        CHECK (NOT takeover OR landing_unit IS NOT NULL)
);

INSERT INTO dashboard_profile_home (profile, landing_unit, landing_module, takeover, mobile_only) VALUES
    ('driver',  'dispatch', 'dispatch', true,  true),
    ('yard',    'yard',     'yard',     true,  true),
    ('counter', NULL,       NULL,       false, true),
    ('manager', NULL,       NULL,       false, true),
    ('admin',   NULL,       NULL,       false, true)
ON CONFLICT (profile) DO NOTHING;

-- 0153_assist_commands_config.sql — DIB-431 (B): the structured command layer's config-as-data.
--
-- WHAT THIS IS FOR. Scrubname0031 asked "can TanyaAI answer this question?". The answer shipped in two
-- halves, and this migration is the floor under the first one: a registry of DETERMINISTIC READ
-- COMMANDS ("what's the balance on Northgate?", "where is order #SO-4?") that runs with no AI in
-- the picture at all. The commands themselves live in code (internal/assist/commands.go) because a
-- command is a function, not a row. What lives here is the part an administrator must be able to
-- change without a release: whether a command is switched ON, and how far down the tiers it reaches.
--
-- THE RULE THAT MAKES THIS SAFE: CONFIG NARROWS, IT NEVER WIDENS.
-- Every command declares its own authority floor in Go. A row in assist_command_policy can make a
-- command STRICTER (disable it, or raise the bar from frontline to manager) and can never make it
-- looser than the code says — Registry.Run intersects the two and takes the tighter of the pair
-- (internal/assist/registry.go, TestRegistry_PolicyCanNarrowButNeverWiden). So the worst an admin
-- can do by editing this table — or the worst an attacker can do by writing to it — is turn the
-- assistant off. Nobody gets a new permission out of a config row.
--
-- And underneath BOTH of those: every command executes through the app's existing read services as
-- the ASKING USER. The tier gate here is a coarse pre-filter that keeps commands out of a menu
-- where they would be useless; the real fence is the same branch scoping and the same RBAC path
-- that the ordinary screens use. A command can never show a person something the app itself would
-- not show them.
--
-- TIER NUMBERS ARE INHERITED, NOT INVENTED. identity.Tier is 1=leadership, 2=manager, 3=frontline —
-- LOWER IS MORE AUTHORITY (internal/identity/rbac.go). max_tier is therefore the numerically
-- HIGHEST (least privileged) tier still allowed: 3 = everyone with a role, 2 = manager and above,
-- 1 = leadership only. A caller with tier 0 (no role assigned) is refused by the service before the
-- comparison is ever reached, which is why 0 is not a legal value here.

CREATE TABLE IF NOT EXISTS assist_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The four keys the pilot reads. Every one of them is documented in NOTES-FIX-431.md; a key that is
-- ABSENT resolves to the same default the code carries, so a half-seeded database still boots.
--
--   provider                    'stub' | 'anthropic'. Which Provider implementation answers chat.
--                               'stub' is deterministic, needs no network and no key, and is what
--                               the tests run against. Selecting 'anthropic' with no API key in the
--                               environment falls back to the stub AND SAYS SO in the boot log —
--                               the app does not refuse to start over a missing optional key.
--   model                       the model name recorded on every ledger row. For the stub this is a
--                               label ('hos-stub-1'); for a real provider it is the model id.
--   chat_enabled                'true' | 'false'. The kill switch for half C. Turning it off leaves
--                               the whole command layer (half B) running, because the commands are
--                               free — they are ordinary reads.
--   spend_visible_to_non_admin  'true' | 'false'. Whether a non-admin may see their OWN month's
--                               spend. Off by default: the house pattern from DIB-434 is that a
--                               visibility question is itself a tech-admin toggle, not a guess made
--                               in a template.
--
-- NO SECRET EVER LANDS IN THIS TABLE. The provider API key is read from an environment variable at
-- boot and is never written to a config row, a log line, or a commit (SOP-1). The key's NAME is
-- documented; its value lives only in the process environment.
INSERT INTO assist_settings (key, value) VALUES
    ('provider',                   'stub'),
    ('model',                      'hos-stub-1'),
    ('chat_enabled',               'true'),
    ('spend_visible_to_non_admin', 'false')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS assist_command_policy (
    command_code text PRIMARY KEY,
    enabled      boolean NOT NULL DEFAULT true,
    -- The least-privileged tier still allowed. See the header: lower number = more authority.
    max_tier     integer NOT NULL DEFAULT 3 CHECK (max_tier BETWEEN 1 AND 3),
    updated_by   uuid REFERENCES users (id),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- The five shipped commands, seeded at the SAME authority their code declares. Seeding them looser
-- than the code would be pointless (the intersection wins) and seeding them tighter here would hide
-- the code's own floor from anyone reading only the migration; identical values make the two
-- readable as one statement, and TestRegistry_SeededPolicyMatchesCodeFloor pins them together.
--
--   payment.lookup    find a payment by reference / amount / customer      manager and above
--   order.lookup      find an order by its number                          everyone
--   stock.lookup      on-hand / available for a product                    everyone
--   customer.balance  a customer's open AR balance                         manager and above
--   customer.activity a customer's recent orders and payments              everyone
--
-- Money is gated at tier 2 and the operational reads are open, which mirrors the surfaces these
-- commands read through: a yard hand can already look up stock and find an order, and cannot
-- already open the AR console. The command layer copies the app's answer instead of inventing one.
INSERT INTO assist_command_policy (command_code, enabled, max_tier) VALUES
    ('payment.lookup',    true, 2),
    ('order.lookup',      true, 3),
    ('stock.lookup',      true, 3),
    ('customer.balance',  true, 2),
    ('customer.activity', true, 3)
ON CONFLICT (command_code) DO NOTHING;

-- 0410_ar_reminder_paylink_enrol.sql
--
-- Walk change 8 S8: the reminder rules configuration the Money Desk AR office renders as the
-- 5-rule table (S5M). Each rule is one row: the canonical AR-landing reminder schedule the
-- application layer already understands, plus the S8 fields that join the rule to the rest of the
-- office: paylink URL template, last-sent stamp, cumulative amount the rule has brought in, and
-- the count of accounts ever re-enrolled. Exemptions are the per-account inverse of enrolment
-- and live in their own table so the same rule may exempt one customer without touching the
-- count for everyone else.
--
-- The boards pick five default rules: BUCKET_CURRENT, BUCKET_30, BUCKET_60, BUCKET_90, and
-- BLACK_HOLE. The seed below is mandatory; without it the office renders an empty page, which
-- is the wrong first impression. The seed is idempotent (ON CONFLICT DO NOTHING) so re-running
-- the migration against a populated database does not dup the rows.
--
-- Note: this table follows the pattern of ar_landing_policy (0183) and ar_reminder_sends and
-- does NOT carry an explicit org_id. Tenant isolation rides on customer_id (which joins back to
-- the org through the customers table) and on the XOrg header at the application layer; adding
-- an org_id column here would diverge from the reminder store's neighbour tables for no gain.

create table if not exists ar_reminder_rules (
    id                       uuid        primary key default gen_random_uuid(),
    code                     text        not null unique,
    label                    text        not null,
    channel                  text        not null,
    days_past_due            integer     not null default 0
        check (days_past_due >= 0),
    max_per_window           integer     not null default 1
        check (max_per_window > 0),
    paylink_url_template     text        not null default '/paylink/{token}',
    last_sent_at             timestamptz,
    amount_brought_in_cents  bigint      not null default 0
        check (amount_brought_in_cents >= 0),
    enrolled_count           integer     not null default 0
        check (enrolled_count >= 0),
    created_at               timestamptz not null default now(),
    updated_at               timestamptz not null default now(),
    check (paylink_url_template like '%{token}%')
);

create index if not exists ar_reminder_rules_code_idx on ar_reminder_rules (code);

create table if not exists ar_reminder_exempts (
    id           uuid        primary key default gen_random_uuid(),
    customer_id  uuid        not null references customers (id) on delete cascade,
    rule_id      uuid        not null references ar_reminder_rules (id) on delete cascade,
    exempt       boolean     not null default true,
    note         text        not null default '',
    actor_id     uuid        references users (id) on delete set null,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now(),
    unique (customer_id, rule_id)
);

create index if not exists ar_reminder_exempts_customer_idx
    on ar_reminder_exempts (customer_id);
create index if not exists ar_reminder_exempts_rule_idx
    on ar_reminder_exempts (rule_id);

comment on table ar_reminder_rules is
    'Reminder rules (walk change 8 S8): one row per rule in the office 5-rule table; the columns drive both the cadence and the office view.';
comment on column ar_reminder_rules.paylink_url_template is
    'Token-bearing URL template; must include {token} (check constraint). The application layer substitutes the customer token before rendering.';
comment on column ar_reminder_rules.last_sent_at is
    'Last successful dispatch of this rule, used to drive the "Last sent" stamp in the office view.';
comment on column ar_reminder_rules.amount_brought_in_cents is
    'Cumulative payments attributable to this rule''s sends; drives the "Brought in" line in the office view.';
comment on column ar_reminder_rules.enrolled_count is
    'Distinct accounts re-enrolled in this rule (S8 enrol route). Increments on every enrol call.';
comment on table ar_reminder_exempts is
    'Per-account opt-out for one reminder rule (S8): the inverse of enrolled. exempt=true opts the account out; enrol flips it back to false without losing the row.';

-- Seed the 5 default rules. Idempotent so re-running on a populated database does not dup rows.
insert into ar_reminder_rules (code, label, channel, days_past_due, max_per_window)
values
    ('CURRENT',     'On-the-day reminder',    'EMAIL',   0, 1),
    ('BUCKET_30',   'Past 30 days',           'EMAIL',  30, 2),
    ('BUCKET_60',   'Past 60 days',           'EMAIL',  60, 2),
    ('BUCKET_90',   'Past 90 days',           'PHONE',  90, 1),
    ('BLACK_HOLE',  'Paper-path escalation',  'PHONE', 120, 1)
on conflict (code) do nothing;

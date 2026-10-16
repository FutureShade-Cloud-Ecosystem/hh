-- 0409_ar_account_payment_policy.sql
--
-- The per-account payment policy (walk change 8 S6, Account payment policy in the Money Desk
-- AR office): which tender the take-a-payment panel offers first, whether the card tender carries
-- a surcharge, and below what amount the customer is required to settle by EFT rather than by card.
-- One row per customer; absent row means "use the house default" (ach_first true, surcharge 240
-- bps, no minimum), so legacy accounts and brand new accounts both read the same sensible default.
-- Surcharge is stored in basis points so 240 bps = 2.40%, the boards' default. Updated_at is
-- maintained by application code, not a trigger, because the row's only writer is one PUT and
-- keeping the trigger out of the picture makes the audit trail obvious from the request alone.
create table if not exists ar_account_payment_policy (
    customer_id        uuid primary key
                       references customers (id) on delete cascade,
    ach_first         boolean     not null default true,
    surcharge_rate_bps integer    not null default 240
                       check (surcharge_rate_bps >= 0 and surcharge_rate_bps <= 10000),
    min_ach_cents     bigint      not null default 0
                       check (min_ach_cents >= 0),
    updated_at        timestamptz not null default now()
);

comment on table ar_account_payment_policy is
    'Per-account payment policy: tender order, card surcharge rate (bps), and minimum charge that forces EFT first.';
comment on column ar_account_payment_policy.surcharge_rate_bps is
    'Card surcharge in basis points (240 = 2.40%). Capped at 10000 (=100%) by check constraint.';
comment on column ar_account_payment_policy.min_ach_cents is
    'Below this balance the take-a-payment panel forces the EFT (ACH) tender first; 0 disables.';

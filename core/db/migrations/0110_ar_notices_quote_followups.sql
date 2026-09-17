-- 0110: the two cadence ledgers — AR collections notices (DIB-76) and quote follow-ups (DIB-58) —
--       plus the two job schedules that drive them, seeded DISABLED.
--       Owner: internal/messagingprovider (AR) + internal/quote (follow-ups). Wave 3, Swarm D.
--
-- Plain-language intent: two things in this business should happen on a clock and currently happen
-- when somebody remembers. An invoice that has been unpaid for thirty days should get a nudge, and
-- again at sixty and ninety — but ONE nudge per bucket, not one per nightly run. A quote that was sent
-- and went quiet should be followed up on day 3, 7 and 14, and should stop the instant the customer
-- accepts it or asks to be left alone.
--
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
-- THESE TABLES ARE LEDGERS, NOT CACHES. THE UNIQUENESS IS THE IDEMPOTENCE.
-- The single most likely way an automation like this hurts a customer relationship is by sending the
-- same message twice — or thirty times, once per nightly run, because "did we already send this?" was
-- answered from a variable in a loop. Both tables therefore carry a UNIQUE constraint that IS the
-- business rule:
--   ar_notice_log      UNIQUE (invoice_id, bucket_days)   — one notice per invoice per aging bucket
--   quote_followup_events UNIQUE (quote_id, step_day, channel) — one follow-up per step per channel
-- The producers INSERT ... ON CONFLICT DO NOTHING and enqueue the text only when the insert actually
-- claimed the row, INSIDE one transaction. Re-running the job is then provably a no-op, and it is the
-- DATABASE that guarantees it, not the ordering of a query the next maintainer may change.
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
--
-- Rollback: additive only (three tables, two disabled schedule rows). Dropping them stops the two
-- automations; nothing else reads them.

-- ── AR collections ledger ───────────────────────────────────────────────────────
-- One row per notice this program decided to send about one invoice at one aging bucket.
CREATE TABLE ar_notice_log (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  -- RECORDED IDENTIFIERS, NOT FOREIGN KEYS — the audit_log / quote_acceptances posture. This is a
  -- record of something we did to a customer; it must stay true and readable even if the invoice is
  -- later voided or the customer row is removed. An ON DELETE CASCADE here would quietly erase the
  -- evidence that we texted somebody, and a re-created invoice id would then be textable again.
  invoice_id    UUID NOT NULL,
  customer_id   UUID NOT NULL,
  invoice_no    TEXT NOT NULL DEFAULT '',   -- snapshot, so the row names the document without a join

  -- Which aging bucket this notice was for. Three buckets, matching the aging report every AR clerk in
  -- the country already reads: 30 / 60 / 90 days past due. The CHECK makes an invented bucket a
  -- constraint violation rather than a silently duplicated cadence.
  bucket_days   INT NOT NULL CHECK (bucket_days IN (30, 60, 90)),

  -- Snapshots of what was true when we sent. The balance moves; what we told the customer does not.
  balance_cents BIGINT NOT NULL DEFAULT 0,
  days_past_due INT NOT NULL DEFAULT 0,

  -- The sms_outbox row this produced, so an operator can walk from "we say we notified them" to the
  -- queue row and its delivery outcome. Nullable: a notice suppressed by cooldown or opt-out is still
  -- recorded (with a reason) so the next run does not reconsider it.
  event_id      UUID,
  suppressed    BOOLEAN NOT NULL DEFAULT FALSE,
  suppress_why  TEXT NOT NULL DEFAULT '',

  decided_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- THE idempotence guarantee. See the header.
  CONSTRAINT ar_notice_log_once_per_bucket UNIQUE (invoice_id, bucket_days)
);
-- The cooldown read: "has this customer heard from collections recently". A contractor with eleven
-- overdue invoices must get one text, not eleven — that is the difference between a reminder and
-- harassment, and it is enforced per CUSTOMER, not per invoice.
CREATE INDEX idx_ar_notice_log_customer_recent ON ar_notice_log (customer_id, decided_at DESC);

-- ── Quote follow-up plan (one row per quote) ────────────────────────────────────
-- The plan is a SETTING; the events below are the history. Keeping them apart is what makes the
-- one-click disable honest: turning the plan off stops future steps and leaves everything already
-- sent visible on the quote, rather than deleting the record of it.
CREATE TABLE quote_followup_plans (
  quote_id      UUID PRIMARY KEY REFERENCES quotes(id) ON DELETE CASCADE,
  enabled       BOOLEAN NOT NULL DEFAULT TRUE,

  -- Which transports this quote's follow-ups use. EMAIL travels the existing notify port; SMS travels
  -- sms_outbox and is additionally subject to consent and quiet hours (0109) — a BOTH plan for a
  -- customer with no SMS consent silently becomes email-only, which is the correct degradation.
  channel       TEXT NOT NULL DEFAULT 'EMAIL' CHECK (channel IN ('EMAIL','SMS','BOTH')),

  -- The cadence, in days after the quote was SENT. An array rather than three columns because the
  -- cadence is genuinely a list a branch may want to shorten or lengthen, and because "day 3, 7, 14"
  -- is one fact, not three. Empty array = a plan that exists but schedules nothing.
  offsets_days  INT[] NOT NULL DEFAULT '{3,7,14}',

  -- The clock the offsets are measured from: when the quote went out. Stored rather than joined
  -- because a re-send should not silently restart a cadence that is already half-run.
  started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

  disabled_at   TIMESTAMPTZ,
  disabled_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  disabled_why  TEXT NOT NULL DEFAULT '',   -- 'staff' | 'ACCEPTED' | 'EXPIRED' | 'opted out' | …

  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- A NULL element would compare as unknown everywhere downstream and produce a step nobody can see
  -- or cancel, so it is rejected here.
  --
  -- POSITIVITY IS NOT CHECKED HERE, AND THAT IS A LIMITATION, NOT AN OVERSIGHT. "every element > 0"
  -- needs unnest, and Postgres forbids a subquery in a CHECK constraint (SQLSTATE 0A000). The
  -- alternatives are a trigger or an IMMUTABLE helper function, both of which put business logic in
  -- the schema where no test in this program would ever read it. So the rule — offsets are strictly
  -- positive, ascending and de-duplicated — is enforced in exactly one place in Go
  -- (quote.NormaliseOffsets) and is covered by a named unit test rather than being half-enforced in
  -- two places that can disagree.
  CONSTRAINT quote_followup_plans_offsets_not_null
    CHECK (array_position(offsets_days, NULL) IS NULL)
);
-- The sweep's read: enabled plans, oldest first.
CREATE INDEX idx_quote_followup_plans_enabled ON quote_followup_plans (started_at) WHERE enabled;

-- ── Quote follow-up history (one row per step per channel) ──────────────────────
-- This is what the quote surface shows the salesperson: what went out, what is scheduled, and what was
-- deliberately not sent and why. A SKIPPED row with a reason is more useful than an absent row, so
-- suppression is recorded rather than implied.
CREATE TABLE quote_followup_events (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id      UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
  step_day      INT NOT NULL CHECK (step_day >= 0),
  channel       TEXT NOT NULL CHECK (channel IN ('EMAIL','SMS')),
  status        TEXT NOT NULL DEFAULT 'SENT'
                  CHECK (status IN ('SENT','SKIPPED','CANCELLED')),
  reason        TEXT NOT NULL DEFAULT '',    -- why a SKIPPED/CANCELLED row is not a send
  event_id      UUID,                        -- the sms_outbox row, when the channel was SMS
  scheduled_for TIMESTAMPTZ NOT NULL,        -- started_at + step_day, i.e. when this step came due
  occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- THE idempotence guarantee for the cadence. See the header.
  CONSTRAINT quote_followup_events_once_per_step UNIQUE (quote_id, step_day, channel)
);
CREATE INDEX idx_quote_followup_events_quote ON quote_followup_events (quote_id, scheduled_for);

-- ── The two schedules, seeded OFF ───────────────────────────────────────────────
-- enabled = FALSE deliberately, and this is the whole safety posture of the bundle: the code is
-- complete, the schedules exist, and NOTHING fires until a human sets enabled = TRUE on the row. An
-- automation that starts texting customers the moment a migration lands is how a deploy becomes an
-- apology. Turning it on is one UPDATE, per environment, by somebody who meant it.
--
-- (Even then nothing leaves the building: the SMS provider defaults to the stub, which logs and sends
-- nothing. Two independent switches, both off.)
INSERT INTO job_schedules (name, kind, nightly_minute, enabled) VALUES
  -- 13:00 UTC ≈ 09:00 Eastern: a collections notice should arrive during business hours, when the
  -- person who receives it can phone the office about it.
  ('ar.sms_collections', 'NIGHTLY', 780, FALSE),
  -- 14:00 UTC ≈ 10:00 Eastern: mid-morning, after the branch has opened and read its email.
  ('quote.followup_cadence', 'NIGHTLY', 840, FALSE)
ON CONFLICT (name) DO NOTHING;

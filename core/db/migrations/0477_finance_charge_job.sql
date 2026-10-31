-- 0477: the finance charge run's worker schedule (wave 3, plan row FC-1b).
-- Owner: internal/billing. Additive, forward only.
--
-- Each night at 09:00 UTC the worker presses the month before the dealer's current house month for
-- every branch, inside the first three house days of the new month (billing/finance_charge_job.go).
-- 09:00 UTC is 05:00 under Eastern daylight time and 04:00 under standard time, so it always falls
-- after the Toronto house midnight on either side of either clock change, and after the house
-- midnight of every North American zone from Newfoundland to the Pacific coast. A dealer further west
-- is served by the three day catch-up window, one day late.
--
-- Seeded ENABLED, like the AP revaluation reversal's row. The run is idempotent by its claim key, so
-- a second night replays and charges nothing. A dealer that wants a Controller to opt in first turns
-- the row off in job_schedules; the Controller's own route still presses a month by hand.
INSERT INTO job_schedules (name, kind, nightly_minute, enabled)
VALUES ('billing.finance_charge_run', 'NIGHTLY', 540, TRUE)
ON CONFLICT (name) DO NOTHING;

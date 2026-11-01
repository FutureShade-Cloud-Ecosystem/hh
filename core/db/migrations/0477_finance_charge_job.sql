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
-- Seeded DISABLED. Unlike the AP revaluation reversal (which only takes back an entry a Controller
-- pressed), this job raises customer facing invoices by itself, at a rate and minimum the dealer's
-- Controller has not yet seen, so the Controller opts in. The run is idempotent by its claim key, so a
-- second night replays and charges nothing. Until the row is enabled the Controller's own route still
-- presses a month by hand. To opt in, enable the row (UPDATE job_schedules SET enabled = TRUE WHERE
-- name = 'billing.finance_charge_run'); the worker arms its next run when it starts.
INSERT INTO job_schedules (name, kind, nightly_minute, enabled)
VALUES ('billing.finance_charge_run', 'NIGHTLY', 540, FALSE)
ON CONFLICT (name) DO NOTHING;

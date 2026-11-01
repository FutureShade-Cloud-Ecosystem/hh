#!/usr/bin/env bash
# shift.sh <evening|target|evening-local|target-local|postgres-url>: print, with an explicit sign, the whole seconds that move the Go
# wall clock (scripts/evening-guard/prepare.sh's overlay, through HH_CLOCK_SHIFT_SECONDS) into the
# house month end window: half past UTC midnight on the 1st of the most recent month whose instant has
# already passed (never ahead of the real clock). At that instant the UTC day has rolled over while the
# house zone (four or five hours behind) is still on the previous evening, so any read that follows the
# UTC day is one day ahead of the dealer's day (the day class); and the UTC month has rolled over while
# the house month is still the previous one, so any read that follows the UTC month is a month ahead of
# the dealer's month (the month class). The 1st's instant is both boundaries at once. The shift is a
# constant: the clock keeps running from there, so the legs still see time pass and a long run stays
# inside the window for hours.
#
#   evening        target minus now: for a run with no database to couple to (the unit packages).
#
#   target         the target instant itself, in whole epoch seconds, unsigned: for the step that pins
#                  the Postgres clock (libfaketime) to the same instant the Go overlay uses.
#
#   <postgres-url> the database's pinned clock minus now: for the integration packages, which run
#                  against a Postgres started under libfaketime at the same target instant (see
#                  ci.yml's guard step). Both clocks then advance at real speed from the same
#                  anchor, so they stay in step for the whole run and the guard's database proof
#                  leg holds; a shift computed against the wall clock at a different moment than
#                  the container's start would drift by the setup gap between them. The URL is
#                  never printed.
#
# The second pin (FROM-PRINCIPAL 233) is the house zone's own first half hour of the 1st: half past
# LOCAL midnight on the 1st of the most recent month whose instant has passed, computed from the
# dealer's zone (HH_GUARD_ZONE, America/Toronto by default) so the daylight saving offset is the zone's
# own (04:30 UTC under EDT, 05:30 UTC under EST), never a constant. There the UTC day and the house day
# are BOTH the 1st, which the first pin never reaches (it sits on the previous house evening): a leg
# that is right on every house evening but reads the 1st wrong (a fixture that opens the closing
# month's books from today's balances, a window stepped in hours across the night the clocks fall back)
# fails only here. "evening-local" and "target-local" mirror "evening" and "target" at this instant.
#
# The database half needs a clock shift and not a zone change: a zone can only make the days
# disagree during the window's own hours, so a daytime run pinned by zone alone would guard
# nothing.
set -euo pipefail
mode="${1:?usage: shift.sh <evening|target|evening-local|target-local|postgres-url>}"
now="$(date -u +%s)"
# Half past UTC midnight on the 1st of this UTC month, or of the month before while this month's is
# still ahead (the first half hour of a month). A pin ahead of the real clock would keep a
# libfaketime Postgres from firing its timers (see ci.yml's guard step), so it is never chosen.
month_target() {
	local first
	first="$(date -u -d "$(date -u -d "@$now" +%Y-%m-01) 00:30:00" +%s)"
	if [ "$first" -gt "$now" ]; then
		first="$(date -u -d "$(date -u -d "$(date -u -d "@$now" +%Y-%m-01) -1 month" +%Y-%m-01) 00:30:00" +%s)"
	fi
	echo "$first"
}
# Half past local midnight on the 1st of this month in the house zone, or of the month before while this
# month's is still ahead.
local_month_target() {
	local zone="${HH_GUARD_ZONE:-America/Toronto}" first_day first
	first_day="$(TZ="$zone" date -d "@$now" +%Y-%m-01)"
	first="$(TZ="$zone" date -d "$first_day 00:30:00" +%s)"
	if [ "$first" -gt "$now" ]; then
		first_day="$(TZ="$zone" date -d "$first_day -1 month" +%Y-%m-01)"
		first="$(TZ="$zone" date -d "$first_day 00:30:00" +%s)"
	fi
	echo "$first"
}
if [ "$mode" = "target" ]; then
	month_target
	exit 0
elif [ "$mode" = "target-local" ]; then
	local_month_target
	exit 0
elif [ "$mode" = "evening" ]; then
	target="$(month_target)"
elif [ "$mode" = "evening-local" ]; then
	target="$(local_month_target)"
else
	# Wait for the server, then read its pinned clock once; it advances while we look at it, so
	# re-read the wall clock in the same breath to keep the difference tight.
	for _ in $(seq 1 60); do
		if db_now="$(psql "$mode" -t -A -v ON_ERROR_STOP=1 -c 'SELECT floor(extract(epoch from now()))' 2>/dev/null)" \
			&& [ -n "$db_now" ]; then
			now="$(date -u +%s)"
			target="$db_now"
			break
		fi
		sleep 2
	done
	[ -n "${db_now:-}" ] || { echo "shift.sh: the database at the given URL never answered" >&2; exit 1; }
fi
shift_s=$(( target - now ))
if [ "$shift_s" -ge 0 ]; then echo "+$shift_s"; else echo "$shift_s"; fi

#!/usr/bin/env bash
# shift.sh <evening|postgres-url>: print, with an explicit sign, the whole seconds that move the Go
# wall clock (scripts/evening-guard/prepare.sh's overlay, through HH_CLOCK_SHIFT_SECONDS) into the
# house evening window: half past UTC midnight of the current UTC day. At that instant the UTC day
# has rolled over while the house zone (four or five hours behind) is still on the previous evening,
# so any read that follows the UTC day is one day ahead of the dealer's day. The shift is a
# constant: the clock keeps running from there, so the legs still see time pass and a long run stays
# inside the window for hours.
#
#   evening        target minus now: for a run with no database to couple to (the unit packages).
#
#   <postgres-url> the database's pinned clock minus now: for the integration packages, which run
#                  against a Postgres started under libfaketime at the same target instant (see
#                  ci.yml's guard step). Both clocks then advance at real speed from the same
#                  anchor, so they stay in step for the whole run and the guard's database proof
#                  leg holds; a shift computed against the wall clock at a different moment than
#                  the container's start would drift by the setup gap between them. The URL is
#                  never printed.
#
# The database half needs a clock shift and not a zone change: a zone can only make the days
# disagree during the window's own hours, so a daytime run pinned by zone alone would guard
# nothing.
set -euo pipefail
mode="${1:?usage: shift.sh <evening|postgres-url>}"
now="$(date -u +%s)"
if [ "$mode" = "evening" ]; then
	target=$(( now - now % 86400 + 1800 ))
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

#!/bin/sh
# The denylist scan (check 5.1 of the image exit test): the scrubbed
# first-dealer words, the planted person names, and the first dealer's
# phone shape may appear neither in the built web bundles of the demo
# image nor in any text column of the seeded database. The bistrack
# token is a wire schema identifier in the bundle JS and is therefore
# scanned on the database's row data only, never on the bundles.
#
# Usage, from a checkout of the branch:
#
#   ./deploy/demo/denylist-scan.sh --bundle-dir ./web/dist
#   ./deploy/demo/denylist-scan.sh --container w5x-happy
#   ./deploy/demo/denylist-scan.sh --pg-container hh-till-l3d-pg --db w5x_happy
#   ./deploy/demo/denylist-scan.sh --container w5x-happy --pg-container hh-till-l3d-pg --db w5x_happy
#   ./deploy/demo/denylist-scan.sh --db-url postgres://.../w5x_happy
#
# Prints denylist_sha256, bundle_matches, and db_matches, and exits 0
# only when both match counts are zero. Every offending file or
# table.column is named on stderr.

set -u

BUNDLE_DIR="${DENYLIST_BUNDLE_DIR:-}"
CONTAINER="${DENYLIST_CONTAINER:-}"
PG_CONTAINER="${DENYLIST_PG_CONTAINER:-}"
DB_NAME="${DENYLIST_DB:-}"
DB_URL="${DENYLIST_DB_URL:-}"

usage() {
	echo "usage: $0 [--bundle-dir dir] [--container name] [--pg-container name --db name] [--db-url url]" >&2
	exit 2
}

while [ $# -gt 0 ]; do
	case "$1" in
	--bundle-dir) [ $# -ge 2 ] || usage; BUNDLE_DIR="$2"; shift 2 ;;
	--container) [ $# -ge 2 ] || usage; CONTAINER="$2"; shift 2 ;;
	--pg-container) [ $# -ge 2 ] || usage; PG_CONTAINER="$2"; shift 2 ;;
	--db) [ $# -ge 2 ] || usage; DB_NAME="$2"; shift 2 ;;
	--db-url) [ $# -ge 2 ] || usage; DB_URL="$2"; shift 2 ;;
	*) usage ;;
	esac
done

# The canonical denylist, one term per line. Its sha256, printed on every
# run, is the denylist's identity on record.
CANONICAL_TERMS='dibbits
quinte
trenton
belleville
kingston
brampton
techo
unilock
permacon
oaks (word)
brown apostrophe s
sika (word)
rymar
alliance gator
in-lite
banas
scrubbed party
scrubname
best.?way
brussels
holland
courtstone
granular a
northline
bay ridge
hastings
613[.-][0-9]{3}[.-][0-9]{4}
priya
nair
ted nurse
ravi raman
owen sharpe
okonkwo
beaupre
villeneuve
okafor
lachance
marchand
bistrack (database only)'

# The bundle scan: first-dealer names, cities, brands, products, the
# planted people, and the phone shape, as one case-insensitive extended
# regex. The bistrack wire schema identifier is excluded here.
BUNDLE_REGEX='dibbits|quinte|trenton|belleville|kingston|brampton|techo|unilock|permacon|\boaks\b|brown.s|\bsika\b|rymar|alliance gator|in-lite|banas|scrubbed party|scrubname|best.?way|brussels|holland|courtstone|granular a|northline|bay ridge|hastings|613[.-][0-9]{3}[.-][0-9]{4}|priya|nair|ted nurse|ravi raman|owen sharpe|okonkwo|beaupre|villeneuve|okafor|lachance|marchand'

# The database scan: the same list plus the bistrack token, with
# PostgreSQL word boundaries (\m start, \M end) where grep uses \b.
DB_REGEX='dibbits|quinte|trenton|belleville|kingston|brampton|techo|unilock|permacon|\moaks\M|brown.s|\msika\M|rymar|alliance gator|in-lite|banas|bistrack|scrubbed party|scrubname|best.?way|brussels|holland|courtstone|granular a|northline|bay ridge|hastings|613[.-][0-9]{3}[.-][0-9]{4}|priya|nair|ted nurse|ravi raman|owen sharpe|okonkwo|beaupre|villeneuve|okafor|lachance|marchand'

denylist_sha256="$(printf '%s\n' "$CANONICAL_TERMS" | sha256sum | cut -d' ' -f1)"
echo "denylist_sha256=$denylist_sha256"

bundle_matches=0
db_matches=0

scan_bundles_local() {
	find "$1" -type f \( -name '*.html' -o -name '*.js' -o -name '*.css' -o -name '*.json' \) -print0 \
		| xargs -r -0 grep -aEil "$BUNDLE_REGEX" 2>/dev/null || true
}

scan_bundles_container() {
	docker exec -e RX="$BUNDLE_REGEX" "$1" sh -c '
		find /srv/app -type f \( -name "*.html" -o -name "*.js" -o -name "*.css" -o -name "*.json" \) -print0 \
		| xargs -r -0 grep -aEil "$RX" 2>/dev/null' || true
}

if [ -n "$BUNDLE_DIR" ]; then
	hits="$(scan_bundles_local "$BUNDLE_DIR")"
elif [ -n "$CONTAINER" ]; then
	hits="$(scan_bundles_container "$CONTAINER")"
else
	hits=""
fi
for f in $hits; do
	bundle_matches=$((bundle_matches + 1))
	echo "bundle word in: $f" >&2
done

psql_scalar() {
	if [ -n "$DB_URL" ]; then
		psql "$DB_URL" -tAq -c "$1"
	else
		docker exec "$PG_CONTAINER" psql -U postgres -d "$DB_NAME" -tAq -c "$1"
	fi
}

if [ -n "$DB_URL" ] || { [ -n "$PG_CONTAINER" ] && [ -n "$DB_NAME" ]; }; then
	sql_regex="$(printf '%s' "$DB_REGEX" | sed "s/'/''/g")"
	columns="$(psql_scalar "SELECT c.table_name || '.' || c.column_name FROM information_schema.columns c JOIN information_schema.tables t ON t.table_name = c.table_name AND t.table_schema = c.table_schema WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE' AND c.table_name <> 'schema_migrations' AND c.data_type IN ('text','character varying','citext','json','jsonb') ORDER BY 1, 2")"
	for col in $columns; do
		t="${col%%.*}"
		c="${col#*.}"
		n="$(psql_scalar "SELECT count(*) FROM public.\"$t\" WHERE \"$c\" ~* '$sql_regex'")"
		case "$n" in
		''|0) ;;
		*)
			db_matches=$((db_matches + 1))
			echo "db word in: $col ($n row(s))" >&2
			;;
		esac
	done
	# The marker suffix is gone from the tenant's own entities, and the
	# rebate's name carries no em or en dash (matched by code point, so
	# the scan script itself stays free of both).
	n="$(psql_scalar "SELECT count(*) FROM projects WHERE name ~* '\(demo\)'")"
	case "$n" in
	''|0) ;;
	*)
		db_matches=$((db_matches + 1))
		echo "db word in: projects.name ($n row(s) still carry the marker suffix)" >&2
		;;
	esac
	n="$(psql_scalar "SELECT count(*) FROM rebate_programs WHERE name ~ E'[\\u2013\\u2014]'")"
	case "$n" in
	''|0) ;;
	*)
		db_matches=$((db_matches + 1))
		echo "db word in: rebate_programs.name ($n row(s) carry a dash)" >&2
		;;
	esac
fi

echo "bundle_matches=$bundle_matches"
echo "db_matches=$db_matches"

if [ "$bundle_matches" -ne 0 ] || [ "$db_matches" -ne 0 ]; then
	exit 1
fi
exit 0

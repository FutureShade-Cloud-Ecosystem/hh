#!/bin/sh
# W5's exit test (plan r7 section 4, item W5): the demo image, end to end,
# against a throwaway local Postgres. Run from a checkout of the branch:
#
#   ./deploy/demo/exit-test.sh
#
# Needs: docker, curl, openssl (or /dev/urandom), and the network access to
# pull the base images the build uses. Every name and port has an
# environment override and a default, so a second party runs the script
# unmodified on its own names:
#
#   W5_EXIT_IMAGE      the image tag to build and drive (hh-demo-till:w5-exit)
#   W5_EXIT_PG         the throwaway Postgres container (hh-till-l3d-pg)
#   W5_EXIT_NET        the docker network (hh-till-l3d-net)
#   W5_EXIT_PG_PORT    the Postgres host port (5599)
#   W5_EXIT_APP_PORT   the happy box's host port (5811; a sibling lane runs on 5801)
#   W5_EXIT_PREFIX     the prefix of every container the script owns (w5x)
#
# The script force-removes only the containers it names with its own
# prefix, never a substring match, so another lane's or a reviewer's
# containers are never touched. An EXIT trap removes, on a passing and on a
# failing run alike, the Postgres container and the network this run
# started (by name, and only if this run started them); ones it found stay.
#
# What is asserted, clause by clause:
#
#   the image builds;
#   on a clean database it answers /healthz ok (no phrase asked);
#   each app serves under its own prefix behind the phrase gate (refused
#     without, served with): the front door at /, the till at /till/, the
#     public desk at /q/ and /quote.html, /quote/ per the desk being present;
#   /api/v1/demo/flows answers the closed table's thirteen staff flows, the five contractor
#     flows and both classes; the three Pro apps serve under /pro-procurement/,
#     /pro-pay/ and /pro-field/ (gated, assets under their own prefixes); the five
#     contractor cards mint a tenant bearer whose role tenant me answers (L-card),
#     the server refuses what a role lacks (L-wall) and no cost key reaches a
#     contractor (L-cost);
#   POST /api/v1/demo/session without the phrase is refused by the gate,
#     and with the phrase mints a token;
#   GET /api/v1/quotes with a valid demo bearer and no phrase answers 200
#     (the gate is the pages and the mint only);
#   /api/v1/payments/config answers simulated: true (G7's served side);
#   the boot leaves the image's own build marker (one row);
#   without the hash value the image exits before serve, and before
#     anything writes;
#   an interrupted build this image started (its marker present, the
#     populate's row absent) resumes and reaches serve;
#   a schema without the marker is refused before anything writes, both
#     shapes: a migrated database holding a non-demo org, and another
#     application's database (its own migration record and tables);
#   a marked build holding a real user or a real payment configuration is
#     refused by the resume refusals before migrate or the seed write;
#   the forwarded address discipline (W2's review): the source pin (the
#     template the image carries holds the four overwrite lines), a
#     client-written X-Forwarded-For never survives the image's Caddy
#     (proven at the traffic level, one capture behind the proxy), and
#     rotating forged values buy the mint's limit no bucket;
#   the phrase's hash sits on no file in the container's writable layer,
#     the scan bites on a planted value, and no served process's
#     environment carries the hash variable.
#
# The one clause the image cannot prove alone is recorded in
# docs/BLOCKERS.md: two distinct client addresses behind the proxy hold
# two separate limit buckets only once the core's limit reads the
# forwarded chain (this branch's core keys the limit on the connection's
# peer only). The mint leg below spends one address's allowance and then
# rotates forged values from the same address, so both clauses it asserts
# hold under either keying, and the leg does not flip when W2's keying
# lands.
#
# The phrase and its hash are generated here, held in shell variables and
# container environments, and never written to a file or printed.
set -eu

cd "$(git rev-parse --show-toplevel)"

IMAGE="${W5_EXIT_IMAGE:-hh-demo-till:w5-exit}"
PG_NAME="${W5_EXIT_PG:-hh-till-l3d-pg}"
NET="${W5_EXIT_NET:-hh-till-l3d-net}"
PG_PORT="${W5_EXIT_PG_PORT:-5599}"
APP_PORT="${W5_EXIT_APP_PORT:-5811}"
PREFIX="${W5_EXIT_PREFIX:-w5x}"
BASE="http://127.0.0.1:${APP_PORT}"
# Database identifiers cannot carry dashes unquoted, so the prefix is
# sanitized once for the throwaway databases' names.
DBPFX="$(printf '%s' "$PREFIX" | tr -cd 'a-zA-Z0-9_')"
HAPPY="$PREFIX-happy"
RESUME="$PREFIX-resume"
CAP="$PREFIX-cap"
CAPCLI="$PREFIX-capcli"

FAILURES=0
net_made=0
pg_started=0

# Runs on every exit, pass or fail: removes by exact name only what this run
# started. The exit status of the run is kept.
cleanup() {
	status=$?
	trap - EXIT
	docker rm -f "$HAPPY" "$RESUME" "$CAP" "$CAPCLI" >/dev/null 2>&1 || true
	if [ "$pg_started" = "1" ]; then
		docker rm -f "$PG_NAME" >/dev/null 2>&1 || true
	elif [ "$pg_found" = "1" ]; then
		for db in "$DBPFX"_happy "$DBPFX"_nohash "$DBPFX"_nomode "$DBPFX"_build "$DBPFX"_foreign "$DBPFX"_appdb "$DBPFX"_realuser "$DBPFX"_realpay; do
			docker exec "$PG_NAME" psql -U postgres -q -c "DROP DATABASE IF EXISTS $db WITH (FORCE)" >/dev/null 2>&1 || true
		done
	fi
	if [ "$net_made" = "1" ]; then
		docker network rm "$NET" >/dev/null 2>&1 || true
	fi
	exit "$status"
}
pg_found=0
trap cleanup EXIT

pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

random_hex() {
	if command -v openssl >/dev/null 2>&1; then
		openssl rand -hex "$1"
	else
		head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'
	fi
}

freshdb() {
	docker exec "$PG_NAME" psql -U postgres -q \
		-c "DROP DATABASE IF EXISTS $1 WITH (FORCE)" -c "CREATE DATABASE $1"
}

dsn_for() {
	printf 'postgres://postgres:hardscape@%s:5432/%s?sslmode=disable' "$PG_NAME" "$1"
}

# The image's own core, migrate role only. With the marker argument the
# helper also writes the image's build marker, which is the state a boot
# reaches when this image stopped between its marker write and the
# populate: the one shape the resume path serves.
migrate_only() {
	db="$1"
	marked="${2:-}"
	if [ "$marked" = "marked" ]; then
		docker run --rm --network "$NET" \
			-e "DATABASE_URL=$(dsn_for "$db")" \
			--entrypoint sh "$IMAGE" -c '
				cd /srv/core && core migrate
				psql "$DATABASE_URL" -qc "CREATE TABLE IF NOT EXISTS demo_build_marker (started timestamptz NOT NULL DEFAULT now())"
				psql "$DATABASE_URL" -qc "INSERT INTO demo_build_marker SELECT now() WHERE NOT EXISTS (SELECT 1 FROM demo_build_marker)"
			' >/dev/null 2>&1
	else
		docker run --rm --network "$NET" \
			-e "DATABASE_URL=$(dsn_for "$db")" \
			--entrypoint sh "$IMAGE" -c 'cd /srv/core && core migrate' >/dev/null 2>&1
	fi
}

tables_count() {
	docker exec "$PG_NAME" psql -U postgres -d "$1" -tAc \
		"SELECT count(*) FROM information_schema.tables
		  WHERE table_schema NOT IN ('pg_catalog', 'information_schema')" | tr -d ' '
}

migrations_count() {
	docker exec "$PG_NAME" psql -U postgres -d "$1" -tAc \
		"SELECT count(*) FROM schema_migrations" 2>/dev/null | tr -d ' '
}

scalar() {
	docker exec "$PG_NAME" psql -U postgres -d "$1" -tAc "$2" | tr -d ' '
}

wait_healthy() {
	probes=0
	while [ "$probes" -lt 90 ]; do
		if curl -sf "$1/healthz" >/dev/null 2>&1; then
			return 0
		fi
		probes=$((probes + 1))
		sleep 2
	done
	return 1
}

wait_box_exited() {
	# The container's own health leg answers only when serve opened; a
	# refusal exits long before. Bounded.
	probes=0
	while [ "$probes" -lt 90 ]; do
		state="$(docker inspect "$1" --format '{{.State.Status}}' 2>/dev/null || true)"
		[ "$state" = "running" ] || return 0
		probes=$((probes + 1))
		sleep 2
	done
	return 1
}

echo "== building the image"
docker build -f deploy/demo/Dockerfile -t "$IMAGE" . >/dev/null || {
	fail "the image build failed"
	exit 1
}
pass "the image builds"

# The throwaway phrase and its hash, made here and held in these shell
# variables only: nothing prints them, nothing writes them to a file.
PHRASE="walnut bramble ledger crevice $(random_hex 8)"
HASH="$(printf '%s\n' "$PHRASE" | docker run --rm -i --entrypoint caddy "$IMAGE" hash-password)"
case "$HASH" in
	'$2a$'*|'$2b$'*|'$2y$'*) pass "the generated gate value is a bcrypt hash" ;;
	*) fail "the generated gate value does not look like bcrypt"; exit 1 ;;
esac

echo "== the throwaway Postgres"
if ! docker network inspect "$NET" >/dev/null 2>&1; then
	docker network create "$NET" >/dev/null
	net_made=1
fi
if docker ps --format '{{.Names}}' | grep -qx "$PG_NAME"; then
	pg_found=1
else
	pg_started=1
	docker rm -f "$PG_NAME" >/dev/null 2>&1 || true
	docker run -d --name "$PG_NAME" --network "$NET" \
		-p "127.0.0.1:$PG_PORT:5432" \
		-e POSTGRES_PASSWORD=hardscape postgres:16-alpine >/dev/null
fi
until docker exec "$PG_NAME" pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
pass "the throwaway Postgres answers"

# The script's own containers, by exact name: another lane's or a
# reviewer's containers are never touched.
docker rm -f "$HAPPY" "$RESUME" "$CAP" "$CAPCLI" >/dev/null 2>&1 || true

# --------------------------------------------------------------------------
# The happy box: a clean database, the whole boot, every served leg.
# --------------------------------------------------------------------------
echo "== the happy box on a clean database"
freshdb "$DBPFX"_happy
docker run -d --name "$HAPPY" --network "$NET" -p "127.0.0.1:$APP_PORT:8080" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_happy)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "CORE_SIGNIN_MODE=demo" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" >/dev/null

if wait_healthy "$BASE"; then
	pass "on a clean database the image answers /healthz ok"
else
	fail "the image never answered /healthz on a clean database"
	docker logs "$HAPPY" 2>&1 | tail -20
fi

code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz")"
[ "$code" = "200" ] && pass "the health leg asks for no phrase" \
	|| fail "the health leg answered $code without a phrase, want 200"

code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/")"
[ "$code" = "401" ] && pass "a page without the phrase is refused" \
	|| fail "a page without the phrase answered $code, want 401"

# The apps the box serves, each under its own prefix behind the one phrase
# gate (the front door at /, the counter till at /till/, the public desk at
# /q/ and at /quote.html where the server's emailed share link lands, HH
# Drive at /drive/, Yard Pick at /yard/, the Quote Desk at /quote/ whenever web/apps/quote-desk
# exists in this tree).
page_has() {
	# path, marker, label
	body="$(curl -s -u "demo:$PHRASE" "$BASE$1")"
	case "$body" in
		*"$2"*) pass "$3 serves with the phrase" ;;
		*) fail "$3 did not serve at $1 (no $2 in the answer)" ;;
	esac
}
page_gated() {
	code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE$1")"
	[ "$code" = "401" ] && pass "$2 is refused without the phrase" \
		|| fail "$2 answered $code without the phrase, want 401"
}
page_has / fd-app-root "the front door at /"
cfg="$(curl -s -u "demo:$PHRASE" "$BASE/config.json")"
case "$cfg" in
	*'"mode":"demo"'*) pass "the front door's config.json carries mode demo" ;;
	*) fail "the front door's config.json is missing or not mode demo (answer: $cfg)" ;;
esac
page_gated /config.json "the front door's config.json"
page_has /till/ ct-app-root "the counter till at /till/"
page_has /buying-and-stock/ buying-and-stock-app "Buying and Stock at /buying-and-stock/"
page_has /drive/ drive-run-app "HH Drive at /drive/"
page_has /home/ manager-home-app "Manager Home at /home/"
page_has /yard/ yard-pick-app "Yard Pick at /yard/"
page_has /money-desk/ money-desk-app "the Money Desk at /money-desk/"
page_has /dispatch/ dispatch-board-app "the dispatch board at /dispatch/"
page_has /messages/ house-messages-app "House Messages at /messages/"
page_has /dealer-admin/ dealer-admin-app "Dealer Admin at /dealer-admin/"
page_has /pro-procurement/ pro-procurement-app "Pro Procurement at /pro-procurement/"
page_has /pro-pay/ pro-pay-app "Pro Pay at /pro-pay/"
page_has /pro-field/ pro-field-app "Pro Field at /pro-field/"
page_has /q/ pd-quote-public "the public desk at /q/"
page_has "/quote.html?t=x" pd-quote-public "the public desk at /quote.html"
page_gated /till/ "the counter till at /till/"
page_gated /buying-and-stock/ "Buying and Stock at /buying-and-stock/"
page_gated /drive/ "HH Drive at /drive/"
page_gated /home/ "Manager Home at /home/"
page_gated /yard/ "Yard Pick at /yard/"
page_gated /money-desk/ "the Money Desk at /money-desk/"
page_gated /dispatch/ "the dispatch board at /dispatch/"
page_gated /messages/ "House Messages at /messages/"
page_gated /dealer-admin/ "Dealer Admin at /dealer-admin/"
page_gated /pro-procurement/ "Pro Procurement at /pro-procurement/"
page_gated /pro-pay/ "Pro Pay at /pro-pay/"
page_gated /pro-field/ "Pro Field at /pro-field/"
page_gated /q/ "the public desk at /q/"
page_gated /quote/ "the /quote/ prefix"
# Never indexed, gate open or shut (decision log row 981): the header rides the
# refusal, a served page, an asset-less prefix and the unauthenticated health leg.
for hp in / /healthz /till/ /yard/; do
	for auth in "" "demo:$PHRASE"; do
		if [ -n "$auth" ]; then who="with"; else who="without"; fi
		if [ -n "$auth" ]; then hdrs="$(curl -sI -u "$auth" "$BASE$hp")"; else hdrs="$(curl -sI "$BASE$hp")"; fi
		case "$(printf '%s' "$hdrs" | tr 'A-Z' 'a-z')" in
			*"x-robots-tag: noindex, nofollow"*) pass "$hp carries X-Robots-Tag noindex, nofollow ($who the phrase)" ;;
			*) fail "$hp lacks X-Robots-Tag noindex, nofollow ($who the phrase)" ;;
		esac
	done
done
if [ -d web/apps/quote-desk ]; then
	code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/quote/")"
	[ "$code" = "200" ] && pass "the Quote Desk serves at /quote/ (the desk is in this tree)" \
		|| fail "the Quote Desk answered $code at /quote/, want 200"
else
	code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/quote/")"
	[ "$code" = "404" ] && pass "/quote/ answers 404 with the phrase while the desk is absent" \
		|| fail "/quote/ answered $code with the phrase while the desk is absent, want 404"
fi
# A till asset resolves under its own prefix (the base flag reached the build).
asset="$(curl -s -u "demo:$PHRASE" "$BASE/till/" | sed -n 's/.*src="\(\/till\/assets\/[^"]*\.js\)".*/\1/p' | head -1)"
if [ -n "$asset" ] && [ "$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE$asset")" = "200" ]; then
	pass "the till's script asset resolves under /till/"
else
	fail "the till's script asset did not resolve under /till/ (found: ${asset:-none})"
fi

# HH Drive's script asset resolves under its own prefix too.
asset="$(curl -s -u "demo:$PHRASE" "$BASE/drive/" | sed -n 's/.*src="\(\/drive\/assets\/[^"]*\.js\)".*/\1/p' | head -1)"
if [ -n "$asset" ] && [ "$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE$asset")" = "200" ]; then
	pass "HH Drive's script asset resolves under /drive/"
else
	fail "HH Drive's script asset did not resolve under /drive/ (found: ${asset:-none})"
fi

# Yard Pick's script asset resolves under its own prefix too.
asset="$(curl -s -u "demo:$PHRASE" "$BASE/yard/" | sed -n 's/.*src="\(\/yard\/assets\/[^"]*\.js\)".*/\1/p' | head -1)"
if [ -n "$asset" ] && [ "$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE$asset")" = "200" ]; then
	pass "Yard Pick's script asset resolves under /yard/"
else
	fail "Yard Pick's script asset did not resolve under /yard/ (found: ${asset:-none})"
fi

# The three Pro apps' script assets resolve under their own prefixes (the base flag reached each build).
for pp in pro-procurement pro-pay pro-field; do
	asset="$(curl -s -u "demo:$PHRASE" "$BASE/$pp/" | sed -n "s/.*src=\"\(\/$pp\/assets\/[^\"]*\.js\)\".*/\1/p" | head -1)"
	if [ -n "$asset" ] && [ "$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE$asset")" = "200" ]; then
		pass "$pp's script asset resolves under /$pp/"
		# The bundle is a demo build: only a --mode demo build carries the demo session mint.
		if curl -s -u "demo:$PHRASE" "$BASE$asset" | grep -q '/api/v1/demo/session'; then
			pass "$pp's bundle is a demo build (it carries the demo session route)"
		else
			fail "$pp's bundle is not a demo build (no demo session route in $asset)"
		fi
	else
		fail "$pp's script asset did not resolve under /$pp/ (found: ${asset:-none})"
	fi
done

flows="$(curl -s -u "demo:$PHRASE" "$BASE/api/v1/demo/flows")"
case "$flows" in
*'"flow":"sales"'*'"flow":"sales-manager"'*'"flow":"yard-manager"'*'"flow":"yard-team"'*'"flow":"driver"'*'"flow":"payables-receivables"'*'"flow":"procurement-manager"'*'"flow":"financial-controller"'*'"flow":"logistics-manager"'*'"flow":"general-manager"'*'"flow":"hr"'*'"flow":"branch-manager"'*'"flow":"outside-sales"'*)
		pass "/api/v1/demo/flows answers the closed table's thirteen staff flows"
		;;
	*)
		fail "/api/v1/demo/flows did not answer the thirteen staff flows: $flows"
		;;
esac

# The listing carries each row's class and the five contractor flows (Summit Ridge Builders): the front door
# routes a tenant row to the Pro app for its role.
case "$flows" in
*'"flow":"contractor-account-admin"'*'"flow":"contractor-buyer"'*'"flow":"contractor-buyer-no-pay"'*'"flow":"contractor-ap-finance"'*'"flow":"contractor-field-crew"'*)
		pass "/api/v1/demo/flows answers the five contractor flows" ;;
	*) fail "/api/v1/demo/flows did not answer the five contractor flows: $flows" ;;
esac
case "$flows" in
	*'"class":"tenant"'*'"class":"staff"'* | *'"class":"staff"'*'"class":"tenant"'*) pass "/api/v1/demo/flows carries both classes, staff and tenant" ;;
	*) fail "/api/v1/demo/flows does not carry both classes: $flows" ;;
esac

code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
	-H 'Content-Type: application/json' -d '{"flow":"sales"}' \
	"$BASE/api/v1/demo/session")"
[ "$code" = "401" ] && pass "the mint without the phrase is refused by the gate" \
	|| fail "the mint without the phrase answered $code, want 401"

mint="$(curl -s -u "demo:$PHRASE" -H 'Content-Type: application/json' \
	-d '{"flow":"sales"}' "$BASE/api/v1/demo/session")"
TOKEN="$(printf '%s' "$mint" | sed 's/.*"token":"\([^"]*\)".*/\1/')"
case "$mint" in
	*'"token"'*) pass "the mint with the phrase answers a token" ;;
	*) fail "the mint with the phrase answered no token: $mint" ;;
esac

code="$(curl -s -o /dev/null -w '%{http_code}' \
	-H "Authorization: Bearer $TOKEN" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/quotes")"
[ "$code" = "200" ] && pass "a staff read with a valid demo bearer and no phrase answers 200" \
	|| fail "GET /api/v1/quotes with a bearer and no phrase answered $code, want 200"

# The Procurement Manager's card (HH-418): the buyer flow mints a branch scoped staff bearer, and
# the Buying and Stock reads answer for it. The mint carries its own visitor id (the sales mint
# above spent no visitor id, and the mint's per-visitor limit is five).
buy="$(curl -s -u "demo:$PHRASE" -H "Cookie: demo_visitor=$(random_hex 16)" \
	-H 'Content-Type: application/json' -d '{"flow":"procurement-manager"}' \
	"$BASE/api/v1/demo/session")"
BUYER="$(printf '%s' "$buy" | sed 's/.*"token":"\([^"]*\)".*/\1/')"
case "$buy" in
	*'"token"'*) pass "the procurement manager mint answers a token" ;;
	*) fail "the procurement manager mint answered no token: $buy" ;;
esac
case "$buy" in
	*'"role_code":"procurement_manager"'*) pass "the procurement manager mint answers the buyer role" ;;
	*) fail "the procurement manager mint answered another role: $buy" ;;
esac
buy_code="$(curl -s -o /dev/null -w '%{http_code}' \
	-H "Authorization: Bearer $BUYER" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/inventory/reorder-points")"
[ "$buy_code" = "200" ] && pass "the procurement manager reads the reorder points" \
	|| fail "GET /api/v1/inventory/reorder-points with the buyer bearer answered $buy_code, want 200"
buy_code="$(curl -s -o /dev/null -w '%{http_code}' \
	-H "Authorization: Bearer $BUYER" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/inventory/shortfall")"
[ "$buy_code" = "200" ] && pass "the procurement manager reads the shortfall worklist" \
	|| fail "GET /api/v1/inventory/shortfall with the buyer bearer answered $buy_code, want 200"

# A contractor persona's card (L-card): the mint answers a tenant class bearer, tenant me answers the role, and the
# role's wall is the server's (L-wall): accounts payable reads invoices and is refused the buying routes, the field
# crew reads orders and is refused billing, the buyer reads the buying routes. Tenant routes carry no X-Org and no
# phrase: the bearer is the credential.
# Each mint carries a fresh visitor id in the shape the listing issues (32 lower case hex), the way each tester's
# browser holds its own: the mint's own limit is five per visitor, so one id for the five cards would be refused.
tenant_mint() {
	curl -s -u "demo:$PHRASE" -H "Cookie: demo_visitor=$(random_hex 16)" -H 'Content-Type: application/json' -d "{\"flow\":\"$1\"}" "$BASE/api/v1/demo/session" \
		| sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
}
tenant_code() {
	# bearer, path
	curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $1" "$BASE$2"
}
for pair in "contractor-account-admin account_admin" "contractor-buyer buyer" "contractor-buyer-no-pay buyer_no_pay" "contractor-ap-finance ap_finance" "contractor-field-crew field_crew"; do
	flow="${pair% *}"
	role="${pair#* }"
	tok="$(tenant_mint "$flow")"
	if [ -z "$tok" ]; then fail "the mint for $flow answered no token"; continue; fi
	me="$(curl -s -H "Authorization: Bearer $tok" "$BASE/api/v1/tenant/me")"
	case "$me" in
		*"\"role\":\"$role\""*) pass "L-card: $flow mints a tenant bearer and tenant me answers the $role role" ;;
		*) fail "L-card: $flow's tenant me did not answer the $role role: $me" ;;
	esac
done
ap="$(tenant_mint contractor-ap-finance)"
crew="$(tenant_mint contractor-field-crew)"
buyer="$(tenant_mint contractor-buyer)"
[ "$(tenant_code "$ap" /api/v1/tenant/invoices)" = "200" ] && pass "L-wall: accounts payable reads invoices" || fail "L-wall: accounts payable could not read invoices"
[ "$(tenant_code "$ap" /api/v1/tenant/procurement/projects)" = "403" ] && pass "L-wall: accounts payable is refused the buying routes by the server" || fail "L-wall: accounts payable was not refused the buying routes"
[ "$(tenant_code "$crew" /api/v1/tenant/orders)" = "200" ] && pass "L-wall: the field crew reads orders" || fail "L-wall: the field crew could not read orders"
[ "$(tenant_code "$crew" /api/v1/tenant/invoices)" = "403" ] && pass "L-wall: the field crew is refused billing by the server" || fail "L-wall: the field crew was not refused billing"
[ "$(tenant_code "$buyer" /api/v1/tenant/procurement/projects)" = "200" ] && pass "L-wall: the buyer reads the buying routes" || fail "L-wall: the buyer could not read the buying routes"
[ "$(tenant_code "" /api/v1/tenant/orders)" = "401" ] && pass "L-wall: a tenant route with no bearer answers 401" || fail "L-wall: a tenant route with no bearer did not answer 401"
# The contractor world (PRO-H): every read below is proved on its own. A leg passes only when the answer carries at
# least the rows the world holds for that read (counted by the key one row of that answer carries once) AND no dealer
# cost, margin or markup key, so an empty list can no longer pass the cost scan (the PRR line from the PR 380 review).
# ROWS: the legs name the rows they count.
tenant_get() {
	# bearer, path
	curl -s -H "Authorization: Bearer $1" "$BASE$2"
}
row_count() {
	# answer, key: how many times the key opens a row
	printf '%s' "$1" | grep -o "\"$2\"[[:space:]]*:" | wc -l | tr -d ' '
}
# tenant_read <label> <bearer> <path> <row key> <least rows>: one read, rows counted, cost keys scanned (L-cost).
tenant_read() {
	label="$1"; bearer="$2"; path="$3"; key="$4"; least="$5"
	answer="$(tenant_get "$bearer" "$path")"
	n="$(row_count "$answer" "$key")"
	echo "   ROWS $label $path: $n x $key (least $least)"
	if [ "$n" -lt "$least" ]; then
		fail "L-cost: $label read $path answered $n rows of $key, want at least $least, so the cost scan proved nothing: $(printf '%s' "$answer" | cut -c1-160)"
	elif printf '%s' "$answer" | grep -qiE '"[a-z_]*(cost|margin|markup)[a-z_]*"[[:space:]]*:'; then
		fail "L-cost: $label read $path carries a cost, margin or markup key"
	else
		pass "L-cost: $label read $path answers $n rows and no cost, margin or markup key"
	fi
}
# tenant_refused <label> <bearer> <path>: the server's wall for a role.
tenant_refused() {
	[ "$(tenant_code "$2" "$3")" = "403" ] && pass "L-wall: $1 is refused $3 by the server" || fail "L-wall: $1 was not refused $3"
}
admin="$(tenant_mint contractor-account-admin)"
# The buyer reads the buying routes and the catalog; the world holds 8 orders, 3 quotes, 3 procurement projects and 4 invoices.
tenant_read "buyer" "$buyer" "/api/v1/tenant/catalog/search?q=paver" product_id 1
tenant_read "buyer" "$buyer" /api/v1/tenant/orders order_no 8
tenant_read "buyer" "$buyer" /api/v1/tenant/quotes quote_no 3
tenant_read "buyer" "$buyer" /api/v1/tenant/procurement/projects line_count 3
tenant_read "buyer" "$buyer" /api/v1/tenant/invoices invoice_no 4
tenant_read "buyer" "$buyer" /api/v1/tenant/payments payment_no 2
tenant_read "buyer" "$buyer" /api/v1/tenant/payment-methods last4 2
tenant_read "buyer" "$buyer" /api/v1/tenant/addresses line1 3
# Accounts payable reads the money: invoices, the payment history, the saved methods, and the orders an invoice answers.
tenant_read "accounts payable" "$ap" /api/v1/tenant/invoices invoice_no 4
tenant_read "accounts payable" "$ap" /api/v1/tenant/payments payment_no 2
tenant_read "accounts payable" "$ap" /api/v1/tenant/payment-methods last4 2
tenant_read "accounts payable" "$ap" /api/v1/tenant/orders order_no 8
tenant_refused "accounts payable" "$ap" /api/v1/tenant/quotes
# The field crew reads orders and the delivery addresses and nothing financial.
tenant_read "field crew" "$crew" /api/v1/tenant/orders order_no 8
tenant_read "field crew" "$crew" /api/v1/tenant/addresses line1 3
tenant_refused "the field crew" "$crew" /api/v1/tenant/quotes
tenant_refused "the field crew" "$crew" /api/v1/tenant/payments
tenant_refused "the field crew" "$crew" /api/v1/tenant/payment-methods
tenant_refused "the field crew" "$crew" /api/v1/tenant/procurement/projects
# The owner reads everything, and the team is the five roles.
tenant_read "owner" "$admin" /api/v1/tenant/users email 5
tenant_read "owner" "$admin" /api/v1/tenant/procurement/projects line_count 3

# Order states and delivery instructions: the list answers every state of the contract's map, and some orders carry a
# delivery note (read per order, the detail is where the note lives).
orders_answer="$(tenant_get "$crew" /api/v1/tenant/orders)"
for st in CONFIRMED READY PARTIALLY_FULFILLED FULFILLED INVOICED; do
	case "$orders_answer" in
		*"\"status\":\"$st\""*) pass "the contractor's order list holds a $st order" ;;
		*) fail "the contractor's order list holds no $st order: $(printf '%s' "$orders_answer" | cut -c1-160)" ;;
	esac
done
noted=0
for oid in $(printf '%s' "$orders_answer" | grep -o '"id":"[^"]*"' | sed 's/"id":"\([^"]*\)"/\1/'); do
	detail="$(tenant_get "$crew" "/api/v1/tenant/orders/$oid")"
	case "$detail" in *'"delivery_instructions":""'*) ;; *'"delivery_instructions":"'*) noted=$((noted + 1)) ;; esac
done
echo "   ROWS field crew orders with a delivery note: $noted"
[ "$noted" -ge 3 ] && pass "at least three orders carry delivery instructions" || fail "only $noted orders carry delivery instructions, want at least 3"

# Money: the invoices carry California tax (775 basis points, half up once) and a balance that follows what was paid.
inv_answer="$(tenant_get "$ap" /api/v1/tenant/invoices)"
bad="$(printf '%s' "$inv_answer" | tr '{' '\n' | sed -n 's/.*"subtotal_cents":\([0-9]*\),"tax_cents":\([0-9]*\).*/\1 \2/p' | while read -r sub tax; do
	want=$(( (sub * 775 + 5000) / 10000 ))
	[ "$tax" = "$want" ] || echo "$sub:$tax:$want"
done)"
[ -z "$bad" ] && pass "every invoice's tax is 7.75 percent of its subtotal, half up" || fail "an invoice's tax is not 7.75 percent of its subtotal (subtotal:tax:want) $bad"
for st in PAID PARTIALLY_PAID FINALIZED; do
	case "$inv_answer" in
		*"\"status\":\"$st\""*) pass "the contractor's invoices hold a $st invoice" ;;
		*) fail "the contractor's invoices hold no $st invoice" ;;
	esac
done

pay="$(curl -s -H "Authorization: Bearer $TOKEN" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/payments/config")"
case "$pay" in
	*'"simulated":true'*) pass "/api/v1/payments/config answers simulated: true (G7)" ;;
	*) fail "/api/v1/payments/config did not answer simulated: true: $pay" ;;
esac

[ "$(scalar "$DBPFX"_happy 'SELECT count(*) FROM demo_build_marker')" = "1" ] \
	&& pass "the boot wrote the image's build marker (one row)" \
	|| fail "the boot left no single build marker row behind"

# --------------------------------------------------------------------------
# The denylist scan (5.1): zero first-dealer or planted words in the
# seeded database and the built bundles, and the scan bites on a planted
# word in a bundle directory of its own.
# --------------------------------------------------------------------------
echo "== the denylist scan over the seeded database and built bundles (5.1)"
denylist_out="$(./deploy/demo/denylist-scan.sh --container "$HAPPY" --pg-container "$PG_NAME" --db "${DBPFX}_happy" 2>&1)" \
	&& pass "the denylist scan is clean ($denylist_out)" \
	|| fail "the denylist scan found a word: $denylist_out"
printf '%s\n' "$denylist_out" | grep -Eq '^denylist_sha256=[0-9a-f]{64}$' \
	&& pass "the scan printed the denylist's hash" \
	|| fail "the scan printed no denylist hash: $denylist_out"
printf '%s\n' "$denylist_out" | grep -q '^bundle_matches=0$' \
	&& pass "the built bundles carry no denylist word" \
	|| fail "the built bundle count is not zero: $denylist_out"
printf '%s\n' "$denylist_out" | grep -q '^db_matches=0$' \
	&& pass "the seeded database carries no denylist word" \
	|| fail "the database match count is not zero: $denylist_out"
denylist_tmp="$(mktemp -d)"
printf 'Such as This month, Trenton\n' > "$denylist_tmp/planted.js"
if ./deploy/demo/denylist-scan.sh --bundle-dir "$denylist_tmp" >/dev/null 2>&1; then
	fail "the denylist scan did not bite on a planted word"
else
	pass "the denylist scan bites on a planted word"
fi
rm -rf "$denylist_tmp"

# --------------------------------------------------------------------------
# The hash's placement: nowhere in the writable layer, the scan bites on a
# planted value, and no served process carries the variable. The scan
# searches the hash's own value as a fixed string (a regex of the bcrypt
# shape would anchor on the dollar signs and see nothing) and the bcrypt
# cost shape over /srv with the migration set's own fixture rows pruned.
# --------------------------------------------------------------------------
leak_files() {
	docker exec -e H="$HASH" "$HAPPY" sh -c '
		{
			grep -rlF -e "$H" /etc /root /tmp /run /var /opt /home /srv 2>/dev/null
			find /srv -type d -name migrations -prune -o -type f -print 2>/dev/null \
				| xargs -r grep -lE "\$2[aby]\\\$[0-9]{2}\\\$" 2>/dev/null
		} | sort -u'
}

if [ -z "$(leak_files)" ]; then
	pass "the phrase's hash sits on no file in the writable layer"
else
	fail "the phrase's hash sits on a file in the writable layer: $(leak_files | tr '\n' ' ')"
fi

docker exec -e H="$HASH" "$HAPPY" sh -c 'printf "%s\n" "$H" > /tmp/w5x-planted-hash'
if leak_files | grep -q "^/tmp/w5x-planted-hash$"; then
	pass "the hash scan bites on a planted value"
else
	fail "the hash scan did not see the planted value"
fi
docker exec "$HAPPY" rm -f /tmp/w5x-planted-hash
[ -z "$(leak_files)" ] && pass "the writable layer is clean again after the plant" \
	|| fail "the writable layer still holds the planted file"

if docker exec "$HAPPY" sh -c '[ -e /root/.config ] && echo present || echo absent' | grep -q absent; then
	pass "Caddy's autosave sits in the shared-memory home, not the writable layer"
else
	fail "Caddy's config home leaked into /root/.config"
fi
leaks="$(docker exec "$HAPPY" sh -c '
	for p in /proc/[0-9]*; do
		pid=${p#/proc/}
		[ "$pid" = "1" ] && continue
		argv0="$(tr "\0" "\n" < "$p/cmdline" 2>/dev/null | head -n 1)"
		case "$argv0" in
			*core|*caddy) ;;
			*) continue ;;
		esac
		tr "\0" "\n" < "$p/environ" 2>/dev/null | grep -c "^DEMO_GATE_BCRYPT_HASH=" || true
	done' | grep -c '^1$' || true)"
[ "$leaks" = "0" ] && pass "the core and Caddy carry no copy of the hash variable" \
	|| fail "$leaks served process(es) carry the hash variable in their environment"

# --------------------------------------------------------------------------
# The G0 pair and the hash refusal.
# --------------------------------------------------------------------------
echo "== without the hash value, before serve and before anything writes"
freshdb "$DBPFX"_nohash
nohash_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_nohash)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "CORE_SIGNIN_MODE=demo" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	"$IMAGE" 2>&1)" && nohash_ok=0 || nohash_ok=1
[ "$nohash_ok" = "1" ] && pass "without the hash the image exits non-zero" \
	|| fail "without the hash the image exited 0"
case "$nohash_log" in
	*DEMO_GATE_BCRYPT_HASH*"is not set"*) pass "the refusal names the missing hash value" ;;
	*) fail "the refusal does not name the missing hash value" ;;
esac
case "$nohash_log" in
	*listening*) fail "the image reached serve without the hash" ;;
	*) pass "the image exited before serve" ;;
esac
[ "$(tables_count "$DBPFX"_nohash)" = "0" ] \
	&& pass "the refused boot wrote nothing to the database" \
	|| fail "the refused boot left user tables behind"

echo "== without the sign in mode, before serve and before anything writes"
freshdb "$DBPFX"_nomode
nomode_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_nomode)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" 2>&1)" && nomode_ok=0 || nomode_ok=1
[ "$nomode_ok" = "1" ] && pass "without the sign in mode the image exits non-zero" \
	|| fail "without the sign in mode the image exited 0"
case "$nomode_log" in
	*CORE_SIGNIN_MODE*"is not set"*) pass "the refusal names the missing sign in mode" ;;
	*) fail "the refusal does not name the missing sign in mode" ;;
esac
case "$nomode_log" in
	*listening*) fail "the image reached serve without the sign in mode" ;;
	*) pass "the image exited before serve" ;;
esac
[ "$(tables_count "$DBPFX"_nomode)" = "0" ] \
	&& pass "the refused boot wrote nothing to the database" \
	|| fail "the refused boot left user tables behind"

echo "== the web runtime config comes from the environment (console copies)"
webcfg="$(docker run --rm \
	-e HH_WEB_MODE=console -e HH_WEB_ORG=hh-demo \
	-e HH_WEB_CONSOLE_ENDPOINT=https://console.example.test/v1 \
	-e HH_WEB_CONSOLE_PROJECT_ID=fake-project \
	-e "HH_WEB_DEALER_NAME=Gable Landscape Supply" -e HH_WEB_DEALER_MARK=gable \
	-e HH_WEB_PERSONAS=true \
	--entrypoint sh "$IMAGE" -c '. /usr/local/bin/write-web-config.sh; write_web_config /tmp/config.json && cat /tmp/config.json' 2>&1)" || webcfg="refused: $webcfg"
case "$webcfg" in
	*'"mode":"console"'*'"org":"hh-demo"'*'"consoleProjectId":"fake-project"'*'"personas":true'*'"name":"Gable Landscape Supply"'*)
		pass "the image's writer makes a console config.json from HH_WEB_*" ;;
	*) fail "the image's writer did not make the console config.json (answer: $webcfg)" ;;
esac
webcfg_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_nomode)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	-e CORE_SIGNIN_MODE=demo \
	-e HH_WEB_MODE=console -e HH_WEB_ORG=hh-demo \
	-e "HH_WEB_CONSOLE_ENDPOINT=a phrase pasted where the endpoint belongs" \
	-e HH_WEB_CONSOLE_PROJECT_ID=fake-project -e HH_WEB_DEALER_NAME=Gable \
	"$IMAGE" 2>&1)" && webcfg_ok=0 || webcfg_ok=1
[ "$webcfg_ok" = "1" ] && pass "a bad web config makes the image exit non-zero" \
	|| fail "a bad web config exited 0"
case "$webcfg_log" in
	*HH_WEB_CONSOLE_ENDPOINT*) pass "the refusal names HH_WEB_CONSOLE_ENDPOINT" ;;
	*) fail "the refusal does not name HH_WEB_CONSOLE_ENDPOINT" ;;
esac
case "$webcfg_log" in
	*"a phrase pasted"*) fail "the refusal repeats the value" ;;
	*) pass "the refusal repeats no value" ;;
esac
[ "$(tables_count "$DBPFX"_nomode)" = "0" ] \
	&& pass "the refused boot wrote nothing to the database" \
	|| fail "the refused boot left user tables behind"

echo "== an interrupted build this image started resumes"
freshdb "$DBPFX"_build
migrate_only "$DBPFX"_build marked
docker run -d --name "$RESUME" --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_build)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "CORE_SIGNIN_MODE=demo" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" >/dev/null
probes=0
until docker exec "$RESUME" wget -q -O /dev/null http://127.0.0.1:8080/healthz 2>/dev/null; do
	probes=$((probes + 1))
	[ "$probes" -lt 90 ] || break
	sleep 2
done
if docker exec "$RESUME" wget -q -O /dev/null http://127.0.0.1:8080/healthz 2>/dev/null; then
	pass "the interrupted build resumed and reached serve"
else
	fail "the interrupted build never reached serve"
fi
resume_log="$(docker logs "$RESUME" 2>&1 || true)"
case "$resume_log" in
	*"resuming the build this image started"*) pass "the resume names itself in the boot's output" ;;
	*) fail "the resume path did not announce itself" ;;
esac

echo "== a schema without this image's marker is refused, both shapes"
# Shape one: the image's own core migrated it, but the marker says another
# boot started the schema (or no boot did): refused before anything writes.
freshdb "$DBPFX"_foreign
migrate_only "$DBPFX"_foreign
docker exec "$PG_NAME" psql -U postgres -q -d "$DBPFX"_foreign \
	-c "INSERT INTO org (slug, name) VALUES ('another-dealer', 'Another Dealer')"
before="$(migrations_count "$DBPFX"_foreign)"
foreign_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_foreign)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "CORE_SIGNIN_MODE=demo" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" 2>&1)" && foreign_ok=0 || foreign_ok=1
[ "$foreign_ok" = "1" ] && pass "a migrated database without the marker is refused" \
	|| fail "a migrated database without the marker was not refused"
case "$foreign_log" in
	*"did not start the build"*"recreate the database resource"*)
		pass "the refusal names the recovery: recreate the database"
		;;
	*)
		fail "the refusal does not name the recovery: $foreign_log" && true
		;;
esac
after="$(migrations_count "$DBPFX"_foreign)"
[ "$before" = "$after" ] && pass "the refusal came before migrate (the record is unchanged)" \
	|| fail "the refused boot changed the migration record ($before to $after)"

# Shape two (the review's P1-1): another application's database, its own
# migration record and its own tables, no marker, no org table at all.
# Every source-product database below the org migration shares this shape.
freshdb "$DBPFX"_appdb
docker exec "$PG_NAME" psql -U postgres -q -d "$DBPFX"_appdb \
	-c "CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())" \
	-c "INSERT INTO schema_migrations (version) VALUES ('someone_else_0001')" \
	-c "CREATE TABLE customers (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT NOT NULL)"
app_before="$(tables_count "$DBPFX"_appdb)"
app_mig_before="$(migrations_count "$DBPFX"_appdb)"
app_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_appdb)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "CORE_SIGNIN_MODE=demo" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" 2>&1)" && app_ok=0 || app_ok=1
[ "$app_ok" = "1" ] && pass "another application's database is refused" \
	|| fail "another application's database was not refused"
app_after="$(tables_count "$DBPFX"_appdb)"
app_mig_after="$(migrations_count "$DBPFX"_appdb)"
if [ "$app_before" = "$app_after" ] && [ "$app_mig_before" = "$app_mig_after" ]; then
	pass "the refused boot wrote nothing into the foreign application's schema"
else
	fail "the refused boot wrote into the foreign schema ($app_before/$app_mig_before to $app_after/$app_mig_after)"
fi

echo "== a marked build holding a real user or a real payment configuration"
# The review's P1-2, shape one: the marker and the migration set are the
# image's own, and one ACTIVE user at a real address sits in the world.
# The resume refusals must refuse before migrate or the seed write.
freshdb "$DBPFX"_realuser
migrate_only "$DBPFX"_realuser marked
docker exec "$PG_NAME" psql -U postgres -q -d "$DBPFX"_realuser \
	-c "INSERT INTO users (id, email, display_name, status) VALUES ('aaaaaaa2-0000-4000-8000-00000000f002', 'f2.real.person@example.org', 'F2 Real Person', 'ACTIVE')"
ru_mig_before="$(migrations_count "$DBPFX"_realuser)"
ru_user_before="$(scalar "$DBPFX"_realuser "SELECT count(*) FROM users")"
realuser_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_realuser)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "CORE_SIGNIN_MODE=demo" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" 2>&1)" && realuser_ok=0 || realuser_ok=1
[ "$realuser_ok" = "1" ] && pass "a marked build holding a real user is refused" \
	|| fail "a marked build holding a real user was not refused"
case "$realuser_log" in
	*"nothing here may write to it"*) pass "the real user refusal names the recovery" ;;
	*) fail "the real user refusal does not name the recovery: $realuser_log" ;;
esac
if [ "$(migrations_count "$DBPFX"_realuser)" = "$ru_mig_before" ] \
	&& [ "$(scalar "$DBPFX"_realuser "SELECT count(*) FROM users")" = "$ru_user_before" ]; then
	pass "the refusal came before the seed (nothing was written)"
else
	fail "the refused boot wrote before refusing (migrations or users moved)"
fi

# The review's P1-2, shape two: the marker, the demo's own org anchor
# alone, and an active payment configuration the populate did not write.
freshdb "$DBPFX"_realpay
migrate_only "$DBPFX"_realpay marked
docker exec "$PG_NAME" psql -U postgres -q -d "$DBPFX"_realpay \
	-c "INSERT INTO org (slug, name) VALUES ('hh-demo', 'HH Demo')" \
	-c "INSERT INTO payment_config (merchant_id, credentials_ciphertext, base_url) VALUES ('REAL-MERCHANT-0001', '\\x00', 'https://payments.example.invalid')"
rp_mig_before="$(migrations_count "$DBPFX"_realpay)"
rp_pay_before="$(scalar "$DBPFX"_realpay "SELECT count(*) FROM payment_config")"
realpay_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_realpay)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "CORE_SIGNIN_MODE=demo" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" 2>&1)" && realpay_ok=0 || realpay_ok=1
[ "$realpay_ok" = "1" ] && pass "a marked build holding a real payment configuration is refused" \
	|| fail "a marked build holding a real payment configuration was not refused"
case "$realpay_log" in
	*"nothing here may write to it"*) pass "the payment configuration refusal names the recovery" ;;
	*) fail "the payment configuration refusal does not name the recovery: $realpay_log" ;;
esac
if [ "$(migrations_count "$DBPFX"_realpay)" = "$rp_mig_before" ] \
	&& [ "$(scalar "$DBPFX"_realpay "SELECT count(*) FROM payment_config")" = "$rp_pay_before" ]; then
	pass "the refusal came before the seed here too (nothing was written)"
else
	fail "the refused boot wrote before refusing (migrations or payment rows moved)"
fi

# --------------------------------------------------------------------------
# The forwarded address discipline (W2's review, routed here).
# --------------------------------------------------------------------------
echo "== the forwarded address discipline, at the source"
pin_template="$(docker run --rm --entrypoint sh "$IMAGE" \
	-c 'grep -c "header_up X-Forwarded-For" /srv/templates/Caddyfile.template' 2>/dev/null || true)"
[ "$pin_template" = "4" ] && pass "the template the image carries holds the four overwrite lines" \
	|| fail "the template in the image holds $pin_template header_up lines, want 4"

echo "== the forwarded address discipline, at the proxy"
# One capture behind a Caddy built from the image's own template: the
# client sends a forged X-Forwarded-For, the capture shows what the
# upstream would have received. The readiness probe targets the page leg
# (not proxied), because the capture socket takes exactly one connection.
docker run -d --name "$CAP" --network "$NET" \
	--entrypoint sh "$IMAGE" -c '
		export XDG_CONFIG_HOME=/dev/shm/ct XDG_DATA_HOME=/dev/shm/ctd
		sed -e "s|__GATE_PORT__|8080|g" \
		    -e "s|__UPSTREAM__|127.0.0.1:9999|g" \
		    -e "s|__GATE_HASH__|$0|g" /srv/templates/Caddyfile.template > /tmp/cf
		tail -f /dev/null | nc -l -p 9999 > /tmp/capture.txt 2>/dev/null &
		exec caddy run --config /tmp/cf --adapter caddyfile
	' "$HASH" >/dev/null
probes=0
ready=0
while [ "$probes" -lt 30 ]; do
	# Ready means the gate answered: the page leg's refusal is the proof
	# Caddy is up (the health and api legs are proxied and would spend the
	# capture socket's one connection).
	if docker exec "$CAP" sh -c \
		'wget -S -q -O /dev/null http://127.0.0.1:8080/ 2>&1' | grep -q " 401 "; then
		ready=1
		break
	fi
	probes=$((probes + 1))
	sleep 1
done
[ "$ready" = "1" ] || fail "the capture proxy never came up"
pin_generated="$(docker exec "$CAP" grep -c "header_up X-Forwarded-For" /tmp/cf 2>/dev/null || true)"
[ "$pin_generated" = "4" ] && pass "the generated configuration carries the four overwrite lines" \
	|| fail "the generated configuration holds $pin_generated header_up lines, want 4"
docker run -d --name "$CAPCLI" --network "$NET" \
	--entrypoint curl \
	curlimages/curl:8.22.0 \
	-s --max-time 8 -o /dev/null \
	-H "X-Forwarded-For: 9.9.9.9" \
	"http://$CAP:8080/api/v1/payments/config" >/dev/null
# The client's address is read while it runs: a stopped container's
# address is released, and this docker renders it as the words invalid IP.
client_ip="$(docker inspect "$CAPCLI" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')"
probes=0
while [ "$(docker inspect "$CAPCLI" --format '{{.State.Running}}')" = "true" ] && [ "$probes" -lt 20 ]; do
	probes=$((probes + 1))
	sleep 1
done
capture="$(docker exec "$CAP" cat /tmp/capture.txt 2>/dev/null || true)"
if [ -n "$client_ip" ] && [ -n "$capture" ] && [ "$client_ip" != "invalid IP" ]; then
	case "$capture" in
		*"X-Forwarded-For: $client_ip"*)
			pass "the proxy replaced the header with the address it saw itself"
			;;
		*)
			fail "the capture does not show the client's real address in X-Forwarded-For (client $client_ip)"
			;;
	esac
	case "$capture" in
		*9.9.9.9*) fail "the client-written forwarded value survived the proxy" ;;
		*) pass "the client-written forwarded value did not survive the proxy" ;;
	esac
else
	fail "the capture leg could not run (client address or capture missing)"
fi

echo "== the forwarded address discipline, at the mint"
# One client address, one session: spend the allowance with honest mints,
# then rotate forged forwarded values from the same address. Under either
# limit keying the forged values buy nothing, because the proxy replaces
# the header (proven above) and the address is already spent.
mint_leg="$(docker run --rm --network "$NET" -e P="$PHRASE" \
	--entrypoint sh curlimages/curl:8.22.0 -c '
		codes=""
		for i in $(seq 1 12); do
			c="$(curl -s -o /dev/null -w "%{http_code}" -u "demo:$P" \
				-H "Content-Type: application/json" -d "{\"flow\":\"sales\"}" \
				http://'"$HAPPY"':8080/api/v1/demo/session)"
			codes="$codes $c"
			case "$c" in 429) break ;; esac
		done
		forged=0
		for i in $(seq 1 12); do
			c="$(curl -s -o /dev/null -w "%{http_code}" -u "demo:$P" \
				-H "Content-Type: application/json" \
				-H "X-Forwarded-For: 10.0.$i.1" \
				-d "{\"flow\":\"sales\"}" \
				http://'"$HAPPY"':8080/api/v1/demo/session)"
			case "$c" in 200) forged=$((forged + 1)) ;; esac
		done
		echo "$codes|$forged"')"
codes="${mint_leg%%|*}"
forged="${mint_leg##*|}"
case "$codes" in
	*429*)
		case "$codes" in
			*200*) pass "one address's allowance is bounded and then refused ($codes)" ;;
			*) fail "the address was refused without any admission: $codes" ;;
		esac
		;;
	*) fail "the mint was never refused from one address: $codes" ;;
esac
[ "$forged" = "0" ] && pass "rotating forged forwarded values bought no bucket" \
	|| fail "$forged mint(s) bought by forged forwarded values"

# Cleanup is the EXIT trap above: the containers, the databases of a
# Postgres that was found, and the Postgres and network this run started.

echo "== done: $FAILURES failure(s)"
[ "$FAILURES" -eq 0 ]

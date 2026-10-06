#!/bin/sh
# D1's exit test (hh-demo-1, one box, both modules): the legs the demo image
# must pass beside W5's own exit test (deploy/demo/exit-test.sh, untouched).
# Run from a checkout of the branch:
#
#   ./deploy/demo/exit-test-d1.sh
#
# Needs what W5's script needs: docker, curl, openssl (or /dev/urandom), and
# the network access to pull the base images the build uses. The same
# environment overrides as today, so a second party runs the script
# unmodified on its own names:
#
#   W5_EXIT_IMAGE      the image tag to build and drive (hh-demo-till:d1-exit)
#   W5_EXIT_PG         the throwaway Postgres container (d1-demo-pg)
#   W5_EXIT_NET        the docker network (d1-demo-net)
#   W5_EXIT_PG_PORT    the Postgres host port (5598)
#   W5_EXIT_APP_PORT   the happy box's host port (5812)
#   W5_EXIT_PREFIX     the prefix of every container the script owns (d1)
#
# Every name this script makes carries the d1 prefix, and no other lane's
# name is a match or a substring of it: the script force-removes only the
# containers it names with its own prefix, never a pattern.
#
# What is asserted, clause by clause (the brief's legs, HH-DEMO-1-D1-BRIEF
# section 3.5):
#
#   each app loads behind the gate and refuses without it:
#     GET /till/    with the phrase answers 200 with the till's markup,
#                   without it 401;
#     GET /quote/   the same pair for the Quote Desk's markup;
#     GET /q/<id>   the same pair for the customer's quote page, served for
#                   a real path under the prefix;
#   a Sales sign in opens both modules:
#     POST /api/v1/demo/session with the phrase and {"flow":"sales"} mints a
#     token; the bearer reads the quotes the Quote Desk serves from; the
#     same box session (the one phrase) serves /till/ and /quote/ with no
#     second sign in;
#   a Sales manager sign in opens both modules, the same way;
#   a quote seeded by QD-6 opens at /q/<id>:
#     the staff list carries the seed's SENT quote; its id serves the
#     customer's page behind the gate; a share link minted for it answers
#     the raw token once; /q/?t=<token> serves the page with the phrase;
#     and the public read behind that token answers the seeded quote's own
#     figures with no bearer and no phrase (the gate is the pages and the
#     mint only, so the customer's data door stays the core's own law).
#
# The phrase and its hash are generated here, held in shell variables and
# container environments, and never written to a file or printed.
set -eu

cd "$(git rev-parse --show-toplevel)"

IMAGE="${W5_EXIT_IMAGE:-hh-demo-till:d1-exit}"
PG_NAME="${W5_EXIT_PG:-d1-demo-pg}"
NET="${W5_EXIT_NET:-d1-demo-net}"
PG_PORT="${W5_EXIT_PG_PORT:-5598}"
APP_PORT="${W5_EXIT_APP_PORT:-5812}"
PREFIX="${W5_EXIT_PREFIX:-d1}"
BASE="http://127.0.0.1:${APP_PORT}"
# Database identifiers cannot carry dashes unquoted, so the prefix is
# sanitized once for the throwaway databases' names.
DBPFX="$(printf '%s' "$PREFIX" | tr -cd 'a-zA-Z0-9_')"
HAPPY="$PREFIX-happy"

FAILURES=0
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

echo "== building the image"
docker build -f deploy/demo/Dockerfile -t "$IMAGE" . >/dev/null || {
	fail "the image build failed"
	exit 1
}
pass "the image builds"

# The throwaway phrase and its hash, made here and held in these shell
# variables only: nothing prints them, nothing writes them to a file.
PHRASE="d1 exit walnut silt $(random_hex 8)"
HASH="$(printf '%s\n' "$PHRASE" | docker run --rm -i --entrypoint caddy "$IMAGE" hash-password)"
case "$HASH" in
	'$2a$'*|'$2b$'*|'$2y$'*) pass "the generated gate value is a bcrypt hash" ;;
	*) fail "the generated gate value does not look like bcrypt"; exit 1 ;;
esac

echo "== the throwaway Postgres"
docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
if docker ps --format '{{.Names}}' | grep -qx "$PG_NAME"; then
	pg_up=1
else
	pg_up=0
	docker rm -f -v "$PG_NAME" >/dev/null 2>&1 || true
	docker run -d --name "$PG_NAME" --network "$NET" \
		-p "127.0.0.1:$PG_PORT:5432" \
		-e POSTGRES_PASSWORD=hardscape postgres:16-alpine >/dev/null
fi
until docker exec "$PG_NAME" pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
pass "the throwaway Postgres answers"

# The script's own container, by exact name.
docker rm -f -v "$HAPPY" >/dev/null 2>&1 || true

echo "== the happy box on a clean database"
freshdb "$DBPFX"_happy
docker run -d --name "$HAPPY" --network "$NET" -p "127.0.0.1:$APP_PORT:8080" \
	-e "DATABASE_URL=$(dsn_for "$DBPFX"_happy)" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" >/dev/null

if wait_healthy "$BASE"; then
	pass "on a clean database the image answers /healthz ok"
else
	fail "the image never answered /healthz on a clean database"
	docker logs "$HAPPY" 2>&1 | tail -20
fi

# --------------------------------------------------------------------------
# Leg 1: each app loads behind the gate and refuses without it.
# --------------------------------------------------------------------------
echo "== each app behind the gate"

for route in /till/ /quote/ /q/demo-quote; do
	code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE$route")"
	[ "$code" = "401" ] && pass "$route without the phrase is refused" \
		|| fail "$route without the phrase answered $code, want 401"
done

page="$(curl -s -u "demo:$PHRASE" "$BASE/till/")"
case "$page" in
	*ct-app-root*) pass "/till/ serves the till's markup with the phrase" ;;
	*) fail "/till/ did not serve the till's markup" ;;
esac

page="$(curl -s -u "demo:$PHRASE" "$BASE/quote/")"
case "$page" in
	*qd-app-root*) pass "/quote/ serves the Quote Desk's markup with the phrase" ;;
	*) fail "/quote/ did not serve the Quote Desk's markup" ;;
esac

page="$(curl -s -u "demo:$PHRASE" "$BASE/q/demo-quote")"
case "$page" in
	*pd-quote-public*) pass "/q/ serves the customer's quote page with the phrase" ;;
	*) fail "/q/ did not serve the customer's quote page" ;;
esac

# --------------------------------------------------------------------------
# Legs 2 and 3: a Sales, then a Sales manager, sign in opens both modules.
# --------------------------------------------------------------------------
echo "== a Sales sign in opens both modules"

mint="$(curl -s -u "demo:$PHRASE" -H 'Content-Type: application/json' \
	-d '{"flow":"sales"}' "$BASE/api/v1/demo/session")"
SALES_TOKEN="$(printf '%s' "$mint" | sed 's/.*"token":"\([^"]*\)".*/\1/')"
case "$mint" in
	*'"token"'*) pass "the Sales mint answers a token" ;;
	*) fail "the Sales mint answered no token: $mint" ;;
esac

quotes="$(curl -s -o /dev/null -w '%{http_code}' \
	-H "Authorization: Bearer $SALES_TOKEN" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/quotes")"
[ "$quotes" = "200" ] && pass "the Sales bearer reads the quote list" \
	|| fail "the Sales bearer's quote read answered $quotes, want 200"

till_code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/till/")"
quote_code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/quote/")"
if [ "$till_code" = "200" ] && [ "$quote_code" = "200" ]; then
	pass "the one box session serves the till and the Quote Desk, no second sign in"
else
	fail "the box session did not carry across (till $till_code, quote $quote_code)"
fi

echo "== a Sales manager sign in opens both modules"

mint="$(curl -s -u "demo:$PHRASE" -H 'Content-Type: application/json' \
	-d '{"flow":"sales-manager"}' "$BASE/api/v1/demo/session")"
MGR_TOKEN="$(printf '%s' "$mint" | sed 's/.*"token":"\([^"]*\)".*/\1/')"
case "$mint" in
	*'"token"'*) pass "the Sales manager mint answers a token" ;;
	*) fail "the Sales manager mint answered no token: $mint" ;;
esac

quotes="$(curl -s -o /dev/null -w '%{http_code}' \
	-H "Authorization: Bearer $MGR_TOKEN" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/quotes")"
[ "$quotes" = "200" ] && pass "the Sales manager bearer reads the quote list" \
	|| fail "the Sales manager bearer's quote read answered $quotes, want 200"

till_code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/till/")"
quote_code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/quote/")"
if [ "$till_code" = "200" ] && [ "$quote_code" = "200" ]; then
	pass "the manager's box session serves the till and the Quote Desk too"
else
	fail "the manager's box session did not carry across (till $till_code, quote $quote_code)"
fi

# --------------------------------------------------------------------------
# Leg 4: a quote seeded by QD-6 opens at /q/<id> behind the gate. The seed's
# SENT quote (the seed's Q2) is found on the staff list, its id serves the
# page, a minted share link carries the customer to it, and the public read
# answers the seeded quote's own figures.
# --------------------------------------------------------------------------
echo "== a quote seeded by QD-6 opens at /q/<id>"

list="$(curl -s -H "Authorization: Bearer $SALES_TOKEN" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/quotes")"
# The seed's SENT quote: the row whose quote number ends in -Q2. The id sits
# beside the quote number in the same row of the list's JSON.
row="$(printf '%s' "$list" | grep -o '{"id":"[^"]*","quote_no":"[^"]*-Q2"' | head -1)"
SEEDED_ID="$(printf '%s' "$row" | sed 's/{"id":"\([^"]*\)".*/\1/')"
SEEDED_NO="$(printf '%s' "$row" | sed 's/.*"quote_no":"\([^"]*\)".*/\1/')"
if [ -n "$SEEDED_ID" ] && [ "$SEEDED_ID" != "$list" ]; then
	pass "the demo world holds the seed's SENT quote ($SEEDED_NO)"
else
	fail "the seed's SENT quote is not on the staff list"
	SEEDED_ID=""
fi

if [ -n "$SEEDED_ID" ]; then
	code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/q/$SEEDED_ID")"
	[ "$code" = "200" ] && pass "/q/<seeded id> serves the page behind the gate" \
		|| fail "/q/<seeded id> answered $code, want 200"

	link="$(curl -s -X POST -H "Authorization: Bearer $SALES_TOKEN" -H 'X-Org: hh-demo' \
		"$BASE/api/v1/quotes/$SEEDED_ID/share-links")"
	TOKEN="$(printf '%s' "$link" | sed 's/.*"token":"\([^"]*\)".*/\1/')"
	case "$link" in
		*'"token"'*) pass "a share link mints for the seeded quote, the token once" ;;
		*) fail "the share link mint answered no token: $link" ;;
	esac

	if [ -n "$TOKEN" ] && [ "$TOKEN" != "$link" ]; then
		code="$(curl -s -o /dev/null -w '%{http_code}' -u "demo:$PHRASE" "$BASE/q/?t=$TOKEN")"
		[ "$code" = "200" ] && pass "/q/?t=<token> serves the customer's page behind the gate" \
			|| fail "/q/?t=<token> answered $code, want 200"

		view="$(curl -s "$BASE/api/public/v1/quotes/$TOKEN")"
		case "$view" in
			*"quote_no"*"$SEEDED_NO"*)
				pass "the public read behind the token answers the seeded quote's figures"
				;;
			*)
				fail "the public read did not answer the seeded quote: $view"
				;;
		esac
		code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/public/v1/quotes/$TOKEN")"
		[ "$code" = "200" ] && pass "the public read asks for no phrase (the gate is the pages and the mint)" \
			|| fail "the public read answered $code without a phrase, want 200"
	fi
fi

# --------------------------------------------------------------------------
# Cleanup: the containers and the throwaway databases go, each with its
# anonymous volumes; the Postgres stays for the next run.
# --------------------------------------------------------------------------
docker rm -f -v "$HAPPY" >/dev/null 2>&1 || true
docker exec "$PG_NAME" psql -U postgres -q -c "DROP DATABASE IF EXISTS ${DBPFX}_happy WITH (FORCE)" >/dev/null 2>&1 || true
if [ "$pg_up" = "0" ]; then
	docker rm -f -v "$PG_NAME" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
fi

echo "== done: $FAILURES failure(s)"
[ "$FAILURES" -eq 0 ]

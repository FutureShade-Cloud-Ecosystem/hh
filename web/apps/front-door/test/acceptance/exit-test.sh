#!/bin/sh
# The front door's exit test (HH-DEMO-1 D2, brief section 3.6): the built app
# behind a local stand-in for the demo image's phrase gate, with the core's
# demo build answering the mint routes against a throwaway Postgres. Run from
# a checkout of the branch:
#
#   npm --prefix web install        (the workspace the app builds from)
#   ./web/apps/front-door/test/acceptance/exit-test.sh
#
# Needs: docker, curl, go (the core's demo build), node and npm. Every name
# and port has an environment override and a default, all of them prefixed
# d2 so a second party runs the script unmodified on its own names and no
# other lane's containers are ever touched:
#
#   D2_FD_PG         the throwaway Postgres container (d2-fd-pg)
#   D2_FD_NET        the docker network (d2-fd-net)
#   D2_FD_PG_PORT    the Postgres host port (5671)
#   D2_FD_APP_PORT   the gated app's host port (5672)
#   D2_FD_CORE_PORT  the core's loopback port (5673)
#
# The script force-removes, with its anonymous volumes, only the container it
# names exactly, and removes the network it created. The phrase is generated
# here, held in this shell and the gate process's environment, and never
# printed or written to a file; the payment and signing keys are random hex
# the same way.
#
# What is asserted, clause by clause:
#
#   the app builds (vite build) and the core's demo build compiles;
#   the gate answers /healthz without a phrase;
#   the picker page is refused without the phrase and served with it;
#   the served bundle carries the picker's structure: the live card
#     labels ("Sales", "Sales manager", "Yard manager", "Yard Team", "Payables
#     and Receivables", "Procurement manager", "Driver", "Financial
#     Controller", "General Manager", "HR", "Branch Manager"), the coming rows' labels, the Coming heading, and
#     the demo badge's copy;
#   /api/v1/demo/flows answers the closed table's flows, every
#     one of the live roles the front door maps, in order;
#   POST /api/v1/demo/session without the phrase is refused by the gate, and
#     with it mints for every flow id the front door maps (sales,
#     sales-manager, yard-manager, yard-team, payables-receivables,
#     procurement-manager, driver, financial-controller, general-manager, hr, branch-manager);
#   the gate stands down on a staff route behind a bearer and no phrase.
#
# Cleanup removes every process, container, network and temporary path the
# script made, and the process listing proves the stops.
set -eu

cd "$(git rev-parse --show-toplevel)"

PG_NAME="${D2_FD_PG:-d2-fd-pg}"
NET="${D2_FD_NET:-d2-fd-net}"
PG_PORT="${D2_FD_PG_PORT:-5671}"
APP_PORT="${D2_FD_APP_PORT:-5672}"
CORE_PORT="${D2_FD_CORE_PORT:-5673}"
BASE="http://127.0.0.1:${APP_PORT}"
CORE_URL="http://127.0.0.1:${CORE_PORT}"
DB="d2fd_exit"
DSN="postgres://postgres:hardscape@127.0.0.1:${PG_PORT}/${DB}?sslmode=disable"
WORK="$(mktemp -d /tmp/d2-fd-exit.XXXXXX)"
CORE_BIN="$WORK/core-demo"
GATE_LOG="$WORK/gate.log"
CORE_LOG="$WORK/core.log"

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

# The demo core's environment, one value set per run: the same vault and
# signing keys seal the populate's world and the serve that reads it, and
# both database variables carry the one DSN (the entrypoint's own discipline:
# the serve role reads CORE_DATABASE_URL and every one-shot reads
# DATABASE_URL, derived from each other so they cannot drift).
VAULT="$(random_hex 32)"
SIGN="$(random_hex 32)"
# The core reads its migration and seed files relative to its own tree (the
# image's entrypoint runs it from /srv/core), so every invocation runs from
# core/.
run_core() {
	(
		cd core &&
			DEMO_MODE=true APP_ENV=demo \
			CORE_STAFF_AUDIENCE="hh-staff" CORE_TENANT_AUDIENCE="hh-tenant" \
				DATABASE_URL="$DSN" CORE_DATABASE_URL="$DSN" \
				PAYMENT_VAULT_KEY="$VAULT" \
				HH_ACCEPTANCE_SIGN_KEY_ID=d2-fd-exit \
				HH_ACCEPTANCE_SIGN_KEY="$SIGN" \
				"$CORE_BIN" "$@"
	)
}

# Every process this script starts is stopped by its own recorded pid, and
# the listing at the end proves the stop by number and name only.
GATE_PID=""
CORE_PID=""
stop_own() {
	[ -n "$GATE_PID" ] && kill "$GATE_PID" 2>/dev/null || true
	[ -n "$CORE_PID" ] && kill "$CORE_PID" 2>/dev/null || true
}
trap stop_own EXIT INT TERM

echo "== building the app and the core's demo build"
# The script's ports must be free before anything starts: a stranger on one
# of them (an earlier run's orphan, another lane's server) would answer the
# walk as if it were ours, so a taken port refuses loudly instead of testing
# someone else's server.
for port in "$PG_PORT" "$APP_PORT" "$CORE_PORT"; do
	if ss -tln 2>/dev/null | grep -q "127.0.0.1:${port} "; then
		fail "port ${port} is already listening: remove that server or set its D2_FD_* override"
		exit 1
	fi
done
if [ ! -d web/node_modules ]; then
	fail "web/node_modules is absent: run npm --prefix web install first"
	exit 1
fi
if (cd web/apps/front-door && PATH="$PWD/../../node_modules/.bin:$PATH" vite build --outDir "$WORK/dist" --emptyOutDir) >/dev/null 2>&1 \
	&& [ -f "$WORK/dist/index.html" ]; then
	pass "the front door builds (vite build, into the run's own work dir, so the tree carries no build output)"
else
	fail "the front door build failed"
	exit 1
fi
if (cd core && go build -tags demo -o "$CORE_BIN" ./cmd/core) 2>/dev/null; then
	pass "the core's demo build compiles"
else
	fail "the core's demo build did not compile"
	exit 1
fi

echo "== the throwaway Postgres"
docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
docker rm -f -v "$PG_NAME" >/dev/null 2>&1 || true
docker run -d --name "$PG_NAME" --network "$NET" \
	-p "127.0.0.1:$PG_PORT:5432" \
	-e POSTGRES_PASSWORD=hardscape postgres:16-alpine >/dev/null
probes=0
# A real query, not pg_isready: the image's init phase runs a temporary
# server on the socket that pg_isready accepts, and a drop or create raced
# into that window dies with the temp server. A SELECT only answers when the
# server that will serve the build is up.
until [ "$(docker exec "$PG_NAME" psql -U postgres -tAc 'SELECT 1' 2>/dev/null)" = "1" ]; do
	probes=$((probes + 1))
	[ "$probes" -lt 60 ] || {
		fail "the throwaway Postgres never answered"
		exit 1
	}
	sleep 1
done
pass "the throwaway Postgres answers"

docker exec "$PG_NAME" psql -U postgres -q \
	-c "DROP DATABASE IF EXISTS $DB WITH (FORCE)" -c "CREATE DATABASE $DB"

echo "== the demo world on the throwaway database"
if run_core migrate >/dev/null 2>&1 \
	&& run_core demoseed -apply >/dev/null 2>&1 \
	&& run_core demo-populate >/dev/null 2>&1; then
	pass "migrate, demoseed and demo-populate built the demo world"
else
	fail "the demo world build failed on the throwaway database"
	exit 1
fi

echo "== the core serving the mint routes"
(
	cd core &&
		DEMO_MODE=true APP_ENV=demo \
			CORE_STAFF_AUDIENCE="hh-staff" CORE_TENANT_AUDIENCE="hh-tenant" \
			DATABASE_URL="$DSN" CORE_DATABASE_URL="$DSN" \
			CORE_LISTEN_ADDR="127.0.0.1:$CORE_PORT" \
			PAYMENT_VAULT_KEY="$VAULT" \
			HH_ACCEPTANCE_SIGN_KEY_ID=d2-fd-exit \
			HH_ACCEPTANCE_SIGN_KEY="$SIGN" \
			exec "$CORE_BIN" serve
) >"$CORE_LOG" 2>&1 &
CORE_PID=$!
probes=0
until curl -sf "$CORE_URL/healthz" >/dev/null 2>&1; do
	probes=$((probes + 1))
	if ! kill -0 "$CORE_PID" 2>/dev/null; then
		fail "the core process exited during startup (is port ${CORE_PORT} taken?)"
		tail -20 "$CORE_LOG"
		exit 1
	fi
	[ "$probes" -lt 60 ] || {
		fail "the core never answered its health"
		tail -20 "$CORE_LOG"
		exit 1
	}
	sleep 1
done
pass "the core answers its health on the loopback port"

echo "== the phrase gate in front of the built app"
PHRASE="d2 exit phrase $(random_hex 12)"
FD_EXIT_DIST="$WORK/dist" \
	FD_EXIT_UPSTREAM="$CORE_URL" \
	FD_EXIT_PORT="$APP_PORT" \
	FD_EXIT_PHRASE="$PHRASE" \
	node web/apps/front-door/test/acceptance/serve.mjs >"$GATE_LOG" 2>&1 &
GATE_PID=$!
probes=0
# The wait proves two things each probe: our own gate process is still alive
# (a bind refusal kills it in the first moment) and the page answers 401.
until [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/")" = "401" ]; do
	probes=$((probes + 1))
	if ! kill -0 "$GATE_PID" 2>/dev/null; then
		fail "the gate process exited during startup (is port ${APP_PORT} taken?)"
		tail -20 "$GATE_LOG"
		exit 1
	fi
	[ "$probes" -lt 30 ] || {
		fail "the gate never came up"
		tail -20 "$GATE_LOG"
		exit 1
	}
	sleep 1
done
pass "the gate refuses the picker page without the phrase (401)"

code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz")"
[ "$code" = "200" ] && pass "the health leg asks for no phrase" \
	|| fail "the health leg answered $code without a phrase, want 200"

page="$(curl -s -u "demo:$PHRASE" "$BASE/")"
case "$page" in
	*fd-app-root*) pass "the picker page serves with the phrase" ;;
	*) fail "the picker page did not serve (no app markup in the answer)" ;;
esac

echo "== the picker's structure in the served bundle"
# Only the matching line's substitution prints (-n with /p), so the variable
# holds the bundle's path alone and not the page with one line rewritten.
BUNDLE_PATH="$(printf '%s' "$page" | sed -n 's/.*src="\/\([^"]*\.js\)".*/\1/p')"
case "$BUNDLE_PATH" in
	assets/*.js) ;;
	*)
		fail "the served page carries no app bundle reference (got: ${BUNDLE_PATH:-nothing})"
		exit 1
		;;
esac
bundle="$(curl -s -u "demo:$PHRASE" "$BASE/$BUNDLE_PATH")"
assert_in_bundle() {
	case "$bundle" in
		*"$1"*) pass "the bundle carries: $1" ;;
		*) fail "the bundle lacks: $1" ;;
	esac
}
assert_in_bundle "Pick a role to walk the demo."
assert_in_bundle "Sales"
assert_in_bundle "Sales manager"
assert_in_bundle "Yard manager"
assert_in_bundle "Yard Team"
assert_in_bundle "Payables and Receivables"
assert_in_bundle "Procurement manager"
assert_in_bundle "Driver"
assert_in_bundle "Financial Controller"
assert_in_bundle "Coming"
assert_in_bundle "Everything on this box is invented demo data: nothing here is real."
assert_in_bundle "Switch role"
assert_in_bundle "Sign out"
for role in "Outside Sales" "Logistics Manager" \
	"Branch Manager" "General Manager" "HR"; do
	assert_in_bundle "$role"
done

echo "== the demo routes through the gate"
flows="$(curl -s -u "demo:$PHRASE" "$BASE/api/v1/demo/flows")"
case "$flows" in
	*'"flow":"sales"'*'"flow":"sales-manager"'*'"flow":"yard-manager"'*'"flow":"yard-team"'*'"flow":"driver"'*'"flow":"payables-receivables"'*'"flow":"procurement-manager"'*'"flow":"financial-controller"'*'"flow":"general-manager"'*'"flow":"hr"'*'"flow":"branch-manager"'*)
		pass "/api/v1/demo/flows answers the closed table's flows, every live role the front door maps"
		;;
	*) fail "/api/v1/demo/flows did not answer the closed table's flows, every live role the front door maps: $flows" ;;
esac

code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
	-H 'Content-Type: application/json' -d '{"flow":"sales"}' \
	"$BASE/api/v1/demo/session")"
[ "$code" = "401" ] && pass "the mint without the phrase is refused by the gate" \
	|| fail "the mint without the phrase answered $code, want 401"

for flow in sales sales-manager yard-manager yard-team driver payables-receivables procurement-manager financial-controller general-manager hr branch-manager; do
	mint="$(curl -s -u "demo:$PHRASE" -H 'Content-Type: application/json' \
		-d "{\"flow\":\"$flow\"}" "$BASE/api/v1/demo/session")"
	case "$mint" in
		*'"token"'*) pass "the mint with the phrase answers a token for $flow" ;;
		*) fail "the mint with the phrase answered no token for $flow: $mint" ;;
	esac
done

echo "== the gate stands down behind a bearer, as the image's gate does"
TOKEN="$(printf '%s' "$mint" | sed 's/.*"token":"\([^"]*\)".*/\1/')"
code="$(curl -s -o /dev/null -w '%{http_code}' \
	-H "Authorization: Bearer $TOKEN" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/branches")"
[ "$code" = "200" ] && pass "a staff read with a valid demo bearer and no phrase answers 200" \
	|| fail "GET /api/v1/branches with a bearer and no phrase answered $code, want 200"

echo "== cleanup: every process, container, network and path this script made"
stop_own
trap - EXIT INT TERM
docker rm -f -v "$PG_NAME" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
rm -rf "$WORK"
pass "the throwaway Postgres container removed with its anonymous volumes"
pass "the docker network removed"
pass "the temporary build and log paths removed ($WORK)"
sleep 1
if [ -n "$(ps -o pid= -p "$GATE_PID" 2>/dev/null)" ] || [ -n "$(ps -o pid= -p "$CORE_PID" 2>/dev/null)" ]; then
	fail "a started process survived the stop"
else
	pass "the gate and the core are stopped (pids $GATE_PID and $CORE_PID absent from the process table)"
fi
ps -eo pid,pgid,etime,comm | grep -E "^ *($GATE_PID|$CORE_PID) " || true

echo "== done: $FAILURES failure(s)"
[ "$FAILURES" -eq 0 ]

#!/bin/bash
# W9's exit test (plan r7 section 4, item W9): the story end to end and the
# testers' page, green on the local image built from the merged state. Run
# from a checkout of the branch:
#
#   web/apps/counter-till/test/acceptance/exit-test.sh
#
# The image is built from the union the lead's merge will carry: the hh
# repository's main tree (MAIN_REF, default origin/main) read through git
# archive, which already holds the demo image machinery and the core, plus
# this branch's counter till app directory (the ported POS surface main does
# not hold yet) and this branch's core directory (the receipt formats work,
# row 666: the core's own printing package changed on this branch). The
# union assembles in the scratch directory (SCRATCH); nothing is ever
# written into the checkout.
#
# Needs: docker, node (22, the version CI names: the stub suite's TypeScript
# import fails under 20), npm, bun, and network access to pull the base
# images.
# The throwaway Postgres is hh-till-l5d-pg on 127.0.0.1:5603, on the lane's
# network, and everything (containers, databases, network, image, scratch
# context) is removed afterwards. The gate phrase and its hash are generated
# here and held in shell variables only: nothing prints them, nothing writes
# them to any file.
#
# What is asserted, in order:
#
#   the image builds from the union context;
#   on a clean database the box answers /healthz, serves the testers' page
#     behind the gate, and answers the flows table's two rows;
#   the acceptance run (the eleven screens as each role, the world leg, and
#     the page's claims held to the ledger) is green, with a screenshot per
#     screen per role in the untracked acceptance folder, no two of them
#     byte-identical, none of them a blank page, and each taken on a settled
#     page (the surface's own in-flight word gone, the reads quiet);
#   the restart leg: the pre restart bearer is refused and a fresh sign in
#     works (a restart signs everyone out);
#   both lanes' existing suites stay green (the stub harness: Playwright and unit);
#   the app typechecks;
#   the root gates pass: bun test, the manifest drift check, the shape check,
#     the scope check;
#   the two source scans and the sink scan print nothing.
set -eu

cd "$(git rev-parse --show-toplevel)"
APP=web/apps/counter-till
MAIN_REF="${MAIN_REF:-origin/main}"
SCRATCH="${W9_SCRATCH:-/home/colton/Desktop/FBHQ/counter-till/scratch/ui4}"
IMAGE=hh-demo-till:ui4-exit
PG_NAME=hh-till-l5d-pg
BOX_NAME=hh-till-l5d-box
NET=hh-till-l5d-net
PG_PORT=5603
BASE=http://127.0.0.1:5801

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

random_hex() { openssl rand -hex "$1"; }

case "$(node --version)" in
	v22.*) ;;
	*) echo "node 22 is required (CI's version); found $(node --version)" >&2; exit 1 ;;
esac

freshdb() {
	docker exec "$PG_NAME" psql -U postgres -q \
		-c "DROP DATABASE IF EXISTS $1 WITH (FORCE)" -c "CREATE DATABASE $1"
}

wait_healthy() {
	probes=0
	while [ "$probes" -lt 150 ]; do
		if curl -sf "$1/healthz" >/dev/null 2>&1; then return 0; fi
		probes=$((probes + 1))
		sleep 2
	done
	return 1
}

cleanup() {
	docker rm -f "$BOX_NAME" >/dev/null 2>&1 || true
	for db in w9_exit; do
		docker exec "$PG_NAME" psql -U postgres -q -c "DROP DATABASE IF EXISTS $db WITH (FORCE)" >/dev/null 2>&1 || true
	done
	if [ "${KEEP:-0}" != "1" ]; then
		# -v: the postgres image keeps its data in an anonymous volume, and a
		# plain rm leaves one behind per run.
		docker rm -f -v "$PG_NAME" >/dev/null 2>&1 || true
		docker network rm "$NET" >/dev/null 2>&1 || true
		docker rmi -f "$IMAGE" >/dev/null 2>&1 || true
		rm -rf "$SCRATCH/context"
	fi
}
trap cleanup EXIT

echo "== the union context from main's tree and this branch's app and core directories"
git rev-parse --verify -q "$MAIN_REF^{commit}" >/dev/null || { fail "$MAIN_REF does not resolve (run git fetch)"; exit 1; }
rm -rf "$SCRATCH/context"
mkdir -p "$SCRATCH/context"
git archive "$MAIN_REF" | tar -x -C "$SCRATCH/context"
[ -f "$SCRATCH/context/deploy/demo/Dockerfile" ] || { fail "$MAIN_REF carries no deploy/demo image machinery"; exit 1; }
rm -rf "$SCRATCH/context/$APP"
mkdir -p "$SCRATCH/context/$APP"
tar -C "$APP" --exclude=node_modules --exclude=dist --exclude=test-results --exclude=acceptance-screens -cf - . | tar -C "$SCRATCH/context/$APP" -xf -
# The receipt formats item (row 666) changes the CORE, so the union overlays
# this branch's core tree the same way the app directory is overlaid: the
# image's core build and the acceptance box then run this branch's receipt
# routes, not main's. MAIN_REF's behaviour is unchanged: it stays the base
# the overlays land on.
rm -rf "$SCRATCH/context/core"
mkdir -p "$SCRATCH/context/core"
tar -C core -cf - . | tar -C "$SCRATCH/context/core" -xf -
pass "the union context assembles ($MAIN_REF with this branch's app and core directories)"

echo "== building the image"
docker build -f "$SCRATCH/context/deploy/demo/Dockerfile" -t "$IMAGE" "$SCRATCH/context" >/dev/null || {
	fail "the image build failed"
	exit 1
}
pass "the image builds from main's tree and this branch's app"

echo "== the throwaway Postgres"
docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
# -v here too: a plain rm would strand the previous run's anonymous volume.
docker rm -f -v "$PG_NAME" >/dev/null 2>&1 || true
docker run -d --name "$PG_NAME" --network "$NET" -p "127.0.0.1:$PG_PORT:5432" \
	-e POSTGRES_PASSWORD=hardscape postgres:16-alpine >/dev/null
until docker exec "$PG_NAME" pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
pass "the throwaway Postgres answers"

PHRASE="$(random_hex 8) $(random_hex 8) $(random_hex 8)"
HASH="$(printf '%s\n' "$PHRASE" | docker run --rm -i --entrypoint caddy "$IMAGE" hash-password)"
case "$HASH" in
	'$2a'*|'$2b'*|'$2y'*) pass "the generated gate value is a bcrypt hash" ;;
	*) fail "the generated gate value does not look like bcrypt"; exit 1 ;;
esac

echo "== the box on a clean database"
freshdb w9_exit
docker rm -f "$BOX_NAME" >/dev/null 2>&1 || true
docker run -d --name "$BOX_NAME" --network "$NET" -p 127.0.0.1:5801:8080 \
	-e "DATABASE_URL=postgres://postgres:hardscape@$PG_NAME:5432/w9_exit?sslmode=disable" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" >/dev/null
if wait_healthy "$BASE"; then
	pass "the box answers /healthz on a clean database"
else
	fail "the box never answered /healthz"
	docker logs "$BOX_NAME" 2>&1 | tail -20
	exit 1
fi
code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/testers.html")"
[ "$code" = "401" ] && pass "the testers' page sits behind the phrase gate" \
	|| fail "the testers' page answered $code without the phrase, want 401"
page="$(curl -s -u "demo:$PHRASE" "$BASE/testers.html")"
case "$page" in
	*"testers' brief"*) pass "the app serves the testers' page" ;;
	*) fail "the testers' page did not serve" ;;
esac
KGN_LINE="$(docker exec "$PG_NAME" psql -U postgres -d w9_exit -tAc \
	"SELECT ol.id::text FROM order_lines ol JOIN orders o ON o.id = ol.order_id
	  WHERE o.order_no = 'DEMO-KGN-S01' LIMIT 1" | tr -d '[:space:]')"
[ -n "$KGN_LINE" ] || { fail "the harness could not read the Kingston sale's line id"; exit 1; }
pass "the harness holds the Kingston sale's line id for the three-rail leg"
flows="$(curl -s -u "demo:$PHRASE" "$BASE/api/v1/demo/flows")"
case "$flows" in
	*'"flow":"sales"'*'"flow":"sales-manager"'*) pass "the flows table answers its two rows" ;;
	*) fail "the flows table did not answer: $flows" ;;
esac

echo "== the acceptance run: the eleven screens as each role, the world, and the page's claims"
SHOT_DIR="$(pwd)/$APP/acceptance-screens"
rm -rf "$SHOT_DIR"
cd "$APP"
if CI=true CT_IMAGE_URL="$BASE" CT_GATE_PHRASE="$PHRASE" CT_SHOT_DIR="$SHOT_DIR" \
	CT_KGN_LINE="$KGN_LINE" \
	npx --no-install playwright test --config test/acceptance.config.ts --grep-invert restart; then
	pass "the acceptance run is green"
else
	fail "the acceptance run failed"
	FAILURES=$((FAILURES + 1))
fi
# The run's screenshots, held to the exact set: every screen of both passes
# named, none missing, none extra.
expected="00-world-flows
40-claims-testers-page
manager-01-way-in
manager-02-drawer
manager-03-sale
manager-04-contractor-pavers
manager-05-credit-refused
manager-06-receipt-card
manager-07-parked
manager-08-return-within
manager-09-own-refund
manager-10-kingston
manager-11-beyond
sales-01-way-in
sales-02-drawer
sales-03-sale
sales-04-below-floor-populated
sales-04-below-floor-rung
sales-04-contractor-pavers
sales-05-credit-refused
sales-06-receipt-account
sales-06-receipt-card
sales-06-receipt-cash
sales-07-parked
sales-08-return-within
sales-09-approved
sales-09-dialog
sales-09-refused
sales-10-kingston
sales-11-beyond"
actual="$(cd "$SHOT_DIR" && ls *.png 2>/dev/null | sed 's/\.png$//' | sort)"
missing=""
for name in $expected; do
	printf '%s\n' "$actual" | grep -qx "$name" || missing="$missing $name"
done
extra=""
for name in $actual; do
	printf '%s\n' "$expected" | grep -qx "$name" || extra="$extra $name"
done
count="$(printf '%s\n' "$actual" | grep -c . || true)"
[ "$count" = "30" ] && [ -z "$missing" ] && [ -z "$extra" ] \
	&& pass "the run produced exactly its 30 named screenshots" \
	|| fail "the screenshots drifted (count $count, missing:$missing, extra:$extra)"
cd - >/dev/null
echo "== the screenshots' own rule: no two byte-identical (the story's thirty)"
dupes="$(cd "$SHOT_DIR" && sha256sum ./*.png | sort | uniq -w64 -D | sed 's/^[0-9a-f]* *//')"
[ -z "$dupes" ] && pass "no two of the story's screenshots are byte-identical" \
	|| fail "byte-identical screenshots: $(printf '%s' "$dupes" | tr '\n' ' ')"

echo "== the restart leg: a restart signs everyone out"
PRIOR_TOKEN="$(curl -s -u "demo:$PHRASE" -H 'Content-Type: application/json' \
	-d '{"flow":"sales"}' "$BASE/api/v1/demo/session" | sed 's/.*"token":"\([^"]*\)".*/\1/')"
docker restart "$BOX_NAME" >/dev/null
if wait_healthy "$BASE"; then
	pass "the box came back after the restart"
else
	fail "the box never came back after the restart"
fi
cd "$APP"
if CI=true CT_IMAGE_URL="$BASE" CT_GATE_PHRASE="$PHRASE" CT_SHOT_DIR="$SHOT_DIR" \
	CT_PRIOR_TOKEN="$PRIOR_TOKEN" \
	npx --no-install playwright test --config test/acceptance.config.ts 90-restart; then
	pass "the restart leg is green (the pre restart bearer is dead, a fresh sign in works)"
else
	fail "the restart leg failed"
	FAILURES=$((FAILURES + 1))
fi
restart_shots="$(cd "$SHOT_DIR" && ls *.png 2>/dev/null | wc -l | tr -d ' ')"
[ "$restart_shots" = "31" ] \
	&& pass "the restart leg's own screenshot sits beside the story's thirty" \
	|| fail "the restart leg left $restart_shots screenshots, want 31"
dupes="$(cd "$SHOT_DIR" && sha256sum ./*.png | sort | uniq -w64 -D | sed 's/^[0-9a-f]* *//')"
[ -z "$dupes" ] && pass "no two of the run's 31 screenshots are byte-identical" \
	|| fail "byte-identical screenshots: $(printf '%s' "$dupes" | tr '\n' ' ')"
cd - >/dev/null

echo "== both lanes' existing suites (the stub harness)"
cd "$APP"
if CI=true npm test; then
	pass "the lanes' suites are green"
else
	fail "the lanes' suites failed"
	FAILURES=$((FAILURES + 1))
fi
if ../../node_modules/.bin/tsc --noEmit; then
	pass "the app typechecks"
else
	fail "the app typecheck failed"
	FAILURES=$((FAILURES + 1))
fi
cd - >/dev/null

echo "== the root gates"
cd "$(git rev-parse --show-toplevel)"
if bash scripts/check-shape.sh >/dev/null 2>&1; then
	pass "the shape check passes"
else
	fail "the shape check failed"
	FAILURES=$((FAILURES + 1))
fi
if bash scripts/check-scope.sh >/dev/null 2>&1; then
	pass "the scope check passes"
else
	fail "the scope check failed"
	FAILURES=$((FAILURES + 1))
fi
if (trap 'rm -rf package.json bun.lock node_modules' EXIT; bun add --exact yaml@2.9.1 >/dev/null 2>&1 && bun test >/dev/null 2>&1); then
	pass "bun test passes"
else
	fail "bun test failed"
	FAILURES=$((FAILURES + 1))
fi
if (trap 'rm -rf package.json bun.lock node_modules' EXIT; bun add --exact yaml@2.9.1 >/dev/null 2>&1 && bun scripts/generate-manifest.ts --check >/dev/null 2>&1); then
	pass "the manifest drift check passes"
else
	fail "the manifest drift check failed"
	FAILURES=$((FAILURES + 1))
fi
cd "$APP" >/dev/null

echo "== the two source scans and the sink scan"
# The money scan is the one rule test/scans.pw.ts holds (ruling 14: the named
# display helper and the seam file ports.ts are the boundary; tests and the
# closure prose are not scanned the way the source is). It is called here, not
# restated, so there is no second divergent grep.
if CI=true npx --no-install playwright test test/scans.pw.ts -g "the money scan" >/dev/null 2>&1; then
	pass "the money scan prints nothing (test/scans.pw.ts, the one rule)"
else
	fail "the money scan printed something"
fi
if grep -rniE 'card_?number|tokeniz|runner\.js' src >/dev/null 2>&1; then
	fail "the card scan printed something"
else
	pass "the card scan prints nothing"
fi
if node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/sinks.test.mjs >/dev/null 2>&1; then
	pass "the sink scan is green"
else
	fail "the sink scan failed"
	FAILURES=$((FAILURES + 1))
fi

echo "== done: $FAILURES failure(s)"
[ "$FAILURES" -eq 0 ]

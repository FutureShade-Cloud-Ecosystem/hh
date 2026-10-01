#!/bin/bash
# W9's exit test (plan r7 section 4, item W9): the story end to end and the
# testers' page, green on the local image built from the goal's integration
# branch. Run from a checkout of the branch:
#
#   web/apps/counter-till/test/acceptance/exit-test.sh
#
# The image is built from the union the lead's merge will carry: the goal
# branch's tree (WT_GOAL, branch till/goal) read through git archive, plus this
# branch's deploy/demo (the W5 image machinery the goal branch does not hold
# yet), plus this lane's declared Touches, the counter till app directory as
# this branch has it. The union assembles in the scratch directory the lane
# was granted (SCRATCH); nothing is ever written into WT_GOAL.
#
# Needs: docker, node, npm, bun, and network access to pull the base images.
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
#     screen per role in the untracked acceptance folder;
#   the restart leg: the pre restart bearer is refused and a fresh sign in
#     works (a restart signs everyone out);
#   both lanes' existing suites stay green (the stub harness, 38 tests);
#   the app typechecks;
#   the root gates pass: bun test, the manifest drift check, the shape check,
#     the scope check;
#   the two source scans and the sink scan print nothing.
set -eu

cd "$(git rev-parse --show-toplevel)"
APP=web/apps/counter-till
GOAL="${WT_GOAL:-/home/colton/Desktop/FBHQ/counter-till/wt-goal}"
SCRATCH="${W9_SCRATCH:-/home/colton/Desktop/FBHQ/counter-till/scratch/w9}"
IMAGE=hh-demo-till:w9-exit
PG_NAME=hh-till-l5d-pg
BOX_NAME=hh-till-l5d-box
NET=hh-till-l5d-net
PG_PORT=5603
BASE=http://127.0.0.1:5801

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

random_hex() { openssl rand -hex "$1"; }

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
		docker rm -f "$PG_NAME" >/dev/null 2>&1 || true
		docker network rm "$NET" >/dev/null 2>&1 || true
		docker rmi -f "$IMAGE" >/dev/null 2>&1 || true
		rm -rf "$SCRATCH/context"
	fi
}
trap cleanup EXIT

echo "== the union context from the goal's head and this lane's Touches"
[ -d "$GOAL" ] || { fail "the goal worktree is missing at $GOAL"; exit 1; }
rm -rf "$SCRATCH/context"
mkdir -p "$SCRATCH/context"
git -C "$GOAL" archive HEAD | tar -x -C "$SCRATCH/context"
# The image machinery rides the goal's head since W5 merged there; a goal
# checkout from before that merge carries none, and this branch's copy stands
# in for it.
if [ ! -f "$SCRATCH/context/deploy/demo/Dockerfile" ]; then
	rm -rf "$SCRATCH/context/deploy"
	cp -r deploy "$SCRATCH/context/deploy"
fi
rm -rf "$SCRATCH/context/$APP"
mkdir -p "$SCRATCH/context/$APP"
tar -C "$APP" --exclude=node_modules --exclude=dist --exclude=test-results -cf - . | tar -C "$SCRATCH/context/$APP" -xf -
pass "the union context assembles (goal head, the app from this lane)"

echo "== building the image"
docker build -f "$SCRATCH/context/deploy/demo/Dockerfile" -t "$IMAGE" "$SCRATCH/context" >/dev/null || {
	fail "the image build failed"
	exit 1
}
pass "the image builds from the goal's integration branch and this lane's work"

echo "== the throwaway Postgres"
docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
docker rm -f "$PG_NAME" >/dev/null 2>&1 || true
docker run -d --name "$PG_NAME" --network "$NET" -p "127.0.0.1:$PG_PORT:5432" \
	-e POSTGRES_PASSWORD=hardscape postgres:16-alpine >/dev/null
until docker exec "$PG_NAME" pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
pass "the throwaway Postgres answers"

PHRASE="walnut bramble ledger crevice $(random_hex 8)"
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
	npx --no-install playwright test --config test/acceptance.config.ts --grep-invert restart; then
	pass "the acceptance run is green"
else
	fail "the acceptance run failed"
	FAILURES=$((FAILURES + 1))
fi
screens="$(find "$SHOT_DIR" -name '*.png' 2>/dev/null | wc -l)"
[ "$screens" -ge 20 ] && pass "the run produced $screens screenshots (per screen per role)" \
	|| fail "the run produced only $screens screenshots"
cd - >/dev/null

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
if grep -rnE '_cents[[:space:]]*[-+*/]|[-+*/][[:space:]]*[A-Za-z_.]*_cents' src >/dev/null 2>&1; then
	fail "the money scan printed something"
else
	pass "the money scan prints nothing"
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

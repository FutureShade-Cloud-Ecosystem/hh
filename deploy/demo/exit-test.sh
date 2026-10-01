#!/bin/sh
# W5's exit test (plan r7 section 4, item W5): the demo image, end to end,
# against a throwaway local Postgres. Run from a checkout of the branch:
#
#   ./deploy/demo/exit-test.sh
#
# Needs: docker, curl, openssl (or /dev/urandom), and the network access to
# pull the base images the build uses. The throwaway Postgres is the
# container hh-till-l3d-pg on 127.0.0.1:5599, started on the lane's network
# when absent and left running when it already was.
#
# What is asserted, clause by clause:
#
#   the image builds;
#   on a clean database it answers /healthz ok (no phrase asked);
#   the app's first page asks for the phrase (refused without, served with);
#   /api/v1/demo/flows answers the closed table's two flows;
#   POST /api/v1/demo/session without the phrase is refused by the gate,
#     and with the phrase mints a token;
#   GET /api/v1/quotes with a valid demo bearer and no phrase answers 200
#     (the gate is the pages and the mint only);
#   /api/v1/payments/config answers simulated: true (G7's served side);
#   without the hash value the image exits before serve, and before
#     anything writes;
#   an interrupted build (migrations applied, the populate's row absent,
#     nothing but the demo's own org) resumes and reaches serve;
#   a database that is not the demo's is refused before migrate, the
#     recovery named, the migration record untouched;
#   the forwarded address discipline (W2's review): a client-written
#     X-Forwarded-For never survives the image's Caddy (proven at the
#     traffic level, one capture behind the proxy), and rotating forged
#     values buy the mint's limit no bucket;
#   the phrase's hash sits on no file in the container's writable layer
#     and in no served process's environment.
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

IMAGE=hh-demo-till:w5-exit
PG_NAME=hh-till-l3d-pg
NET=hh-till-l3d-net
PG_PORT=5599
BASE=http://127.0.0.1:5801

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

migrate_only() {
	# The image's own core, migrate role only: the state a boot reaches if
	# it stops between migrate and demo-populate.
	docker run --rm --network "$NET" \
		-e "DATABASE_URL=$1" \
		--entrypoint sh "$IMAGE" -c 'cd /srv/core && core migrate' >/dev/null 2>&1
}

user_tables() {
	docker exec "$PG_NAME" psql -U postgres -d "$1" -tAc \
		"SELECT count(*) FROM information_schema.tables
		  WHERE table_schema NOT IN ('pg_catalog', 'information_schema')" | tr -d ' '
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
PHRASE="walnut bramble ledger crevice $(random_hex 8)"
HASH="$(printf '%s\n' "$PHRASE" | docker run --rm -i --entrypoint caddy "$IMAGE" hash-password)"
case "$HASH" in
	'$2a$'*|'$2b$'*|'$2y$'*) pass "the generated gate value is a bcrypt hash" ;;
	*) fail "the generated gate value does not look like bcrypt"; exit 1 ;;
esac

echo "== the throwaway Postgres"
docker network inspect "$NET" >/dev/null 2>&1 || docker network create "$NET" >/dev/null
if ! docker ps --format '{{.Names}}' | grep -qx "$PG_NAME"; then
	docker rm -f "$PG_NAME" >/dev/null 2>&1 || true
	docker run -d --name "$PG_NAME" --network "$NET" \
		-p "127.0.0.1:$PG_PORT:5432" \
		-e POSTGRES_PASSWORD=hardscape postgres:16-alpine >/dev/null
fi
until docker exec "$PG_NAME" pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
pass "the throwaway Postgres answers"

for name in w5-exit-happy w5-exit-resume w5-exit-cap w5-exit-capcli; do
	docker rm -f "$name" >/dev/null 2>&1 || true
done
# Any other container of this lane's earlier runs carries the same w5-
# prefix and may hold the script's published ports; it goes too.
stale="$(docker ps -aq --filter 'name=w5-' || true)"
[ -z "$stale" ] || docker rm -f $stale >/dev/null 2>&1 || true

# --------------------------------------------------------------------------
# The happy box: a clean database, the whole boot, every served leg.
# --------------------------------------------------------------------------
echo "== the happy box on a clean database"
freshdb w5_exit_happy
docker run -d --name w5-exit-happy --network "$NET" -p 127.0.0.1:5801:8080 \
	-e "DATABASE_URL=postgres://postgres:hardscape@$PG_NAME:5432/w5_exit_happy?sslmode=disable" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" >/dev/null

if wait_healthy "$BASE"; then
	pass "on a clean database the image answers /healthz ok"
else
	fail "the image never answered /healthz on a clean database"
	docker logs w5-exit-happy 2>&1 | tail -20
fi

code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz")"
[ "$code" = "200" ] && pass "the health leg asks for no phrase" \
	|| fail "the health leg answered $code without a phrase, want 200"

code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/")"
[ "$code" = "401" ] && pass "a page without the phrase is refused" \
	|| fail "a page without the phrase answered $code, want 401"

page="$(curl -s -u "demo:$PHRASE" "$BASE/")"
case "$page" in
	*ct-app-root*) pass "the app's first page serves with the phrase" ;;
	*) fail "the app's first page did not serve (no app markup in the answer)" ;;
esac

flows="$(curl -s -u "demo:$PHRASE" "$BASE/api/v1/demo/flows")"
case "$flows" in
	*'"flow":"sales"'*'"flow":"sales-manager"'*)
		pass "/api/v1/demo/flows answers the closed table's two flows"
		;;
	*)
		fail "/api/v1/demo/flows did not answer the two flows: $flows"
		;;
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

pay="$(curl -s -H "Authorization: Bearer $TOKEN" -H 'X-Org: hh-demo' \
	"$BASE/api/v1/payments/config")"
case "$pay" in
	*'"simulated":true'*) pass "/api/v1/payments/config answers simulated: true (G7)" ;;
	*) fail "/api/v1/payments/config did not answer simulated: true: $pay" ;;
esac

# The hash's placement: nowhere in the writable layer, no served process
# carries it, Caddy's autosave lives in the shared-memory home.
if docker exec w5-exit-happy sh -c "grep -rl '\$2a\$' /srv /etc /root /tmp /run 2>/dev/null" | grep -q .; then
	fail "the phrase's hash sits on a file in the container's writable layer"
else
	pass "the phrase's hash sits on no file in the writable layer"
fi
if docker exec w5-exit-happy sh -c '[ -e /root/.config ] && echo present || echo absent' | grep -q absent; then
	pass "Caddy's autosave sits in the shared-memory home, not the writable layer"
else
	fail "Caddy's config home leaked into /root/.config"
fi
leaks="$(docker exec w5-exit-happy sh -c '
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
freshdb w5_exit_nohash
nohash_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=postgres://postgres:hardscape@$PG_NAME:5432/w5_exit_nohash?sslmode=disable" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
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
[ "$(user_tables w5_exit_nohash)" = "0" ] \
	&& pass "the refused boot wrote nothing to the database" \
	|| fail "the refused boot left user tables behind"

echo "== the interrupted build resumes"
freshdb w5_exit_build
migrate_only "postgres://postgres:hardscape@$PG_NAME:5432/w5_exit_build?sslmode=disable"
docker run -d --name w5-exit-resume --network "$NET" -p 127.0.0.1:5802:8080 \
	-e "DATABASE_URL=postgres://postgres:hardscape@$PG_NAME:5432/w5_exit_build?sslmode=disable" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" >/dev/null
probes=0
until docker exec w5-exit-resume wget -q -O /dev/null http://127.0.0.1:8080/healthz 2>/dev/null; do
	probes=$((probes + 1))
	[ "$probes" -lt 90 ] || break
	sleep 2
done
if docker exec w5-exit-resume wget -q -O /dev/null http://127.0.0.1:8080/healthz 2>/dev/null; then
	pass "the interrupted build resumed and reached serve"
else
	fail "the interrupted build never reached serve"
fi
resume_log="$(docker logs w5-exit-resume 2>&1 || true)"
case "$resume_log" in
	*"resuming the build"*) pass "the resume names itself in the boot's output" ;;
	*) fail "the resume path did not announce itself" ;;
esac

echo "== a database that is not the demo's"
freshdb w5_exit_foreign
migrate_only "postgres://postgres:hardscape@$PG_NAME:5432/w5_exit_foreign?sslmode=disable"
docker exec "$PG_NAME" psql -U postgres -q -d w5_exit_foreign \
	-c "INSERT INTO org (slug, name) VALUES ('another-dealer', 'Another Dealer')"
before="$(docker exec "$PG_NAME" psql -U postgres -d w5_exit_foreign -tAc \
	'SELECT count(*) FROM schema_migrations' | tr -d ' ')"
foreign_log="$(docker run --rm --network "$NET" \
	-e "DATABASE_URL=postgres://postgres:hardscape@$PG_NAME:5432/w5_exit_foreign?sslmode=disable" \
	-e "PAYMENT_VAULT_KEY=$(random_hex 32)" \
	-e "HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1" \
	-e "HH_ACCEPTANCE_SIGN_KEY=$(random_hex 32)" \
	-e "DEMO_GATE_BCRYPT_HASH=$HASH" \
	"$IMAGE" 2>&1)" && foreign_ok=0 || foreign_ok=1
[ "$foreign_ok" = "1" ] && pass "a non-demo database is refused" \
	|| fail "a non-demo database was not refused"
case "$foreign_log" in
	*"recreate the database resource"*) pass "the refusal names the recovery: recreate the database" ;;
	*) fail "the refusal does not name the recovery" ;;
esac
after="$(docker exec "$PG_NAME" psql -U postgres -d w5_exit_foreign -tAc \
	'SELECT count(*) FROM schema_migrations' | tr -d ' ')"
[ "$before" = "$after" ] && pass "the refusal came before migrate (the record is unchanged)" \
	|| fail "the refused boot changed the migration record ($before to $after)"

# --------------------------------------------------------------------------
# The forwarded address discipline (W2's review, routed here).
# --------------------------------------------------------------------------
echo "== the forwarded address discipline, at the proxy"
# One capture behind a Caddy built from the image's own template: the
# client sends a forged X-Forwarded-For, the capture shows what the
# upstream would have received. The readiness probe targets the page leg
# (not proxied), because the capture socket takes exactly one connection.
docker run -d --name w5-exit-cap --network "$NET" \
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
	if docker exec w5-exit-cap sh -c \
		'wget -S -q -O /dev/null http://127.0.0.1:8080/ 2>&1' | grep -q " 401 "; then
		ready=1
		break
	fi
	probes=$((probes + 1))
	sleep 1
done
[ "$ready" = "1" ] || fail "the capture proxy never came up"
docker run -d --name w5-exit-capcli --network "$NET" \
	--entrypoint curl \
	curlimages/curl:latest \
	-s --max-time 8 -o /dev/null \
	-H "X-Forwarded-For: 9.9.9.9" \
	"http://w5-exit-cap:8080/api/v1/payments/config" >/dev/null
# The client's address is read while it runs: a stopped container's
# address is released, and this docker renders it as the words invalid IP.
client_ip="$(docker inspect w5-exit-capcli --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')"
probes=0
while [ "$(docker inspect w5-exit-capcli --format '{{.State.Running}}')" = "true" ] && [ "$probes" -lt 20 ]; do
	probes=$((probes + 1))
	sleep 1
done
capture="$(docker exec w5-exit-cap cat /tmp/capture.txt 2>/dev/null || true)"
if [ -n "$client_ip" ] && [ -n "$capture" ]; then
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
	--entrypoint sh curlimages/curl:latest -c '
		codes=""
		for i in $(seq 1 12); do
			c="$(curl -s -o /dev/null -w "%{http_code}" -u "demo:$P" \
				-H "Content-Type: application/json" -d "{\"flow\":\"sales\"}" \
				http://w5-exit-happy:8080/api/v1/demo/session)"
			codes="$codes $c"
			case "$c" in 429) break ;; esac
		done
		forged=0
		for i in $(seq 1 12); do
			c="$(curl -s -o /dev/null -w "%{http_code}" -u "demo:$P" \
				-H "Content-Type: application/json" \
				-H "X-Forwarded-For: 10.0.$i.1" \
				-d "{\"flow\":\"sales\"}" \
				http://w5-exit-happy:8080/api/v1/demo/session)"
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

# --------------------------------------------------------------------------
# Cleanup: the containers and the throwaway databases go; the Postgres
# stays for the next run.
# --------------------------------------------------------------------------
for name in w5-exit-happy w5-exit-resume w5-exit-cap w5-exit-capcli; do
	docker rm -f "$name" >/dev/null 2>&1 || true
done
for db in w5_exit_happy w5_exit_nohash w5_exit_build w5_exit_foreign; do
	docker exec "$PG_NAME" psql -U postgres -q -c "DROP DATABASE IF EXISTS $db WITH (FORCE)" >/dev/null 2>&1 || true
done

echo "== done: $FAILURES failure(s)"
[ "$FAILURES" -eq 0 ]

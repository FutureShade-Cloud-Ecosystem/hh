#!/bin/sh
# The acceptance-real box (W2-4 (a)): the finance image (deploy/finance/
# Dockerfile, the default build) on a throwaway database, started with the
# run's throwaway session key file, so the Playwright specs in
# acceptance-real reach the real core's authz with no FB Console and no
# demo mint. Both the acceptance workflow and a developer run the same
# script:
#
#   bash test/acceptance-real/box.sh up     # start the box, write the state
#   bash test/acceptance-real/box.sh down   # remove every container
#   bash test/acceptance-real/box.sh names  # the names this tree's box uses
#
# or through Playwright, which runs `box.sh up` as its webServer and the
# config's globalTeardown runs `box.sh down`.
#
# The names carry the tree: the containers and the network are named after the
# app directory the box belongs to (the same FNV-1a slot the ports use), or
# after the name the run gives the tree with MDREAL_NAME. Two worktrees beside
# each other therefore run two boxes with two sets of names.
#
# What `up` does, in order:
#   1. generates the run's throwaway EC P-256 session key and the invented
#      acceptance tester (nothing names a real person, every value is
#      per run) and writes state.json for the specs;
#   2. starts a throwaway Postgres 16 and the finance image on it, with the
#      box's finance user list naming the acceptance tester, so the
#      entrypoint provisions the staff row and the global
#      financial_controller grant at boot (the token's subject);
#   3. waits for the box's /healthz.
#
# WORLD SEAM (fin2 W2-1b): until the world driver lands this box builds on
# today's seed (core finance-seed). When W2-1b merges, load the world dump
# in load_world below, before the box boots, and swap the pinned figures in
# harness.pw.ts for the world's.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
# The app directory this box belongs to: the Money Desk app itself, two levels
# up from this script (test/acceptance-real). This is the same string the
# Playwright config hashes (its own appDir, without the trailing slash), so
# the slot below is the slot the ports use.
APP_DIR="$(cd "$HERE/../.." && pwd)"

# Ports and names a run may override. The defaults hash the app directory
# (the FNV-1a slot the repo's other configs use), so this tree's box never
# collides with another worktree's box beside it; the Playwright config
# computes the same pair and passes them to this script.
SLOT="$("$(command -v node || echo /snap/bin/node)" -e "const p = process.argv[1]; let h = 2166136261; for (let i = 0; i < p.length; i += 1) { h ^= p.charCodeAt(i); h = Math.imul(h, 16777619); } console.log((h >>> 0) % 512)" "$APP_DIR")"
MDREAL_BOX_PORT="${MDREAL_BOX_PORT:-$((27000 + SLOT * 2))}"
CONSOLE_PORT="${MDREAL_CONSOLE_PORT:-$((27000 + SLOT * 2 + 1))}"
MDREAL_PG_PORT="${MDREAL_PG_PORT:-$((28000 + SLOT * 2))}"
MDREAL_IMAGE="${MDREAL_IMAGE:-s4-w2-4a-finance:latest}"
MDREAL_STATE="${MDREAL_STATE:-${TMPDIR:-/tmp}/money-desk-acceptance-real-$SLOT}"
export MDREAL_STATE

# THE NAMES CARRY THE TREE. A box's containers and its network are named
# after the tree they belong to, so two worktrees beside each other never
# reach for the same container: the slot is the same FNV-1a slot the ports
# use, and a run may name the tree outright with MDREAL_NAME (the Playwright
# config passes it). Without this, a second tree's `up` would remove the first
# tree's box, because both asked for the same fixed names.
NAME_PREFIX="${MDREAL_NAME:-s4-w2-4a-$SLOT}"
NAME_PG="$NAME_PREFIX-pg"
NAME_BOX="$NAME_PREFIX-box"
NAME_CONSOLE="$NAME_PREFIX-console"
NAME_NET="$NAME_PREFIX-net"

ORG="${MDREAL_ORG:-lakeshore-hardscape}"
DEALER_NAME="${MDREAL_DEALER_NAME:-Lakeshore Hardscape}"
ISSUER="${MDREAL_ISSUER:-hh-acceptance-real}"
STAFF_AUDIENCE="${MDREAL_STAFF_AUDIENCE:-hh-staff}"
TENANT_AUDIENCE="${MDREAL_TENANT_AUDIENCE:-hh-tenant}"

node_bin() {
	if [ -n "${MDREAL_NODE:-}" ]; then echo "$MDREAL_NODE"; return 0; fi
	if [ -x /snap/bin/node ]; then echo /snap/bin/node; return 0; fi
	command -v node
}

up() {
	mkdir -p -m 0700 "$MDREAL_STATE"
	# The throwaway key, the tester and the state the specs read. Nothing
	# here is a credential of a real system.
	"$(node_bin)" --experimental-strip-types "$HERE/session.ts" setup \
		--box-port "$MDREAL_BOX_PORT" --console-port "$CONSOLE_PORT" --org "$ORG"
	chmod 0400 "$MDREAL_STATE/keys/session.pem" 2>/dev/null || true

	PG_PASS="$(openssl rand -hex 24)"
	VAULT_KEY="$(openssl rand -hex 32)"
	SIGN_KEY="$(openssl rand -hex 32)"

	docker rm -f "$NAME_BOX" "$NAME_PG" "$NAME_CONSOLE" >/dev/null 2>&1 || true
	docker network rm "$NAME_NET" >/dev/null 2>&1 || true
	docker network create "$NAME_NET" >/dev/null
	docker run -d --name "$NAME_PG" --network "$NAME_NET" \
		-p "127.0.0.1:${MDREAL_PG_PORT}:5432" \
		-e POSTGRES_PASSWORD="$PG_PASS" \
		postgres:16-alpine >/dev/null
	probes=0
	until docker exec "$NAME_PG" pg_isready -U postgres >/dev/null 2>&1; do
		probes=$((probes + 1))
		if [ "$probes" -ge 60 ]; then
			echo "box: the throwaway database never came up" >&2
			exit 1
		fi
		sleep 1
	done

	# The console double: the four FB Console answers the box calls (team
	# membership per request, the exchange's account, session and JWT
	# reads), so the real exchange mints the staff token with no FB
	# Console. It serves the acceptance tester alone.
	docker run -d --name "$NAME_CONSOLE" --network "$NAME_NET" \
		-p "127.0.0.1:${CONSOLE_PORT}:8080" \
		-e MDREAL_STATE=/state \
		-e MDREAL_CONSOLE_PORT=8080 \
		-v "$MDREAL_STATE:/state:ro" \
		-v "$HERE:/double:ro" \
		node:22-alpine node /double/console-double.mjs >/dev/null
	probes=0
	until curl -sf "http://127.0.0.1:${CONSOLE_PORT}/account" >/dev/null 2>&1; do
		if [ "$(docker inspect -f '{{.State.Running}}' "$NAME_CONSOLE" 2>/dev/null)" != "true" ]; then
			echo "box: the console double exited at boot" >&2
			docker logs "$NAME_CONSOLE" 2>&1 | tail -10 >&2
			exit 1
		fi
		probes=$((probes + 1))
		if [ "$probes" -ge 60 ]; then
			echo "box: the console double never answered" >&2
			exit 1
		fi
		sleep 1
	done

	SUBJECT="$(sed -n 's/.*"subject": *"\([^"]*\)".*/\1/p' "$MDREAL_STATE/state.json")"

	# load_world: the W2-1b seam. Until fin2's world driver lands this box
	# boots on today's seed (core finance-seed, the entrypoint's own path).
	# When the world dump exists, load it into the fresh database HERE, then
	# boot with MDREAL_SKIP_SEED=1 so the entrypoint migrates without
	# seeding. The dump's sha256 is checked before anything is dropped,
	# exactly the finance-reset rule.
	: # no world dump yet (W2-1b)

	docker run -d --name "$NAME_BOX" --network "$NAME_NET" \
		-p "127.0.0.1:${MDREAL_BOX_PORT}:8080" \
		-e DATABASE_URL="postgres://postgres:${PG_PASS}@${NAME_PG}:5432/postgres?sslmode=disable" \
		-e APP_ENV=staging \
		-e HH_BOX_KIND=finance \
		-e CORE_SIGNIN_MODE=console \
		-e CORE_TOKEN_ISSUER="$ISSUER" \
		-e CORE_STAFF_AUDIENCE="$STAFF_AUDIENCE" \
		-e CORE_TENANT_AUDIENCE="$TENANT_AUDIENCE" \
		-e CORE_SESSION_SIGNING_KEY_FILE=/run/hh-secrets/mount/session.pem \
		-e CORE_APPWRITE_ENDPOINT=http://${NAME_CONSOLE}:8080 \
		-e CORE_APPWRITE_PROJECT=acceptance-console \
		-e CORE_APPWRITE_API_KEY=acceptance-console-key \
		-e HH_FINANCE_USERS="acceptance.controller@example.invalid|${SUBJECT}|Acceptance Controller|financial_controller" \
		-e PAYMENT_VAULT_KEY="$VAULT_KEY" \
		-e PORTAL_BASE_URL="https://portal.127.0.0.1.nip.io" \
		-e ERP_BASE_URL="https://erp.127.0.0.1.nip.io" \
		-e PORT=8080 \
		-e HH_ACCEPTANCE_SIGN_KEY_ID=acceptance-quote-sign \
		-e HH_ACCEPTANCE_SIGN_KEY="$SIGN_KEY" \
		-e HH_WEB_MODE=console \
		-e HH_WEB_ORG="$ORG" \
		-e HH_WEB_CONSOLE_ENDPOINT="http://127.0.0.1:${CONSOLE_PORT}" \
		-e HH_WEB_CONSOLE_PROJECT_ID=acceptance-console \
		-e HH_WEB_DEALER_NAME="$DEALER_NAME" \
		-v "$MDREAL_STATE/keys:/run/hh-secrets/mount:ro" \
		"$MDREAL_IMAGE" >/dev/null
	probes=0
	until curl -sf "http://127.0.0.1:${MDREAL_BOX_PORT}/healthz" >/dev/null 2>&1; do
		if [ "$(docker inspect -f '{{.State.Running}}' "$NAME_BOX" 2>/dev/null)" != "true" ]; then
			echo "box: the finance box exited before its health answered" >&2
			docker logs "$NAME_BOX" 2>&1 | tail -30 >&2
			exit 1
		fi
		probes=$((probes + 1))
		if [ "$probes" -ge 180 ]; then
			echo "box: the finance box never answered /healthz" >&2
			docker logs "$NAME_BOX" 2>&1 | tail -30 >&2
			exit 1
		fi
		sleep 2
	done
	echo "box: up at http://127.0.0.1:${MDREAL_BOX_PORT} (state in ${MDREAL_STATE})"
	names
}

down() {
	docker rm -f "$NAME_BOX" "$NAME_PG" "$NAME_CONSOLE" >/dev/null 2>&1 || true
	docker network rm "$NAME_NET" >/dev/null 2>&1 || true
}

# The webServer holder. Playwright owns its webServer process for the whole
# run, so that process has to stay alive: `up` returns the moment the health
# answers, and Playwright's own probe of the URL can land after the script has
# already gone ("Process from config.webServer exited early"). This waits for
# the box to go away, or for Playwright to stop the process.
hold() {
	while docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "$NAME_BOX"; do
		sleep 5
	done
}

# The names this tree's box uses. Two app directories print two sets, so one
# tree's box can never be another tree's box.
names() {
	echo "names: pg=${NAME_PG} box=${NAME_BOX} console=${NAME_CONSOLE} net=${NAME_NET} slot=${SLOT} state=${MDREAL_STATE} pg_port=${MDREAL_PG_PORT}"
}

case "${1:-}" in
	up) up ;;
	down) down ;;
	hold) hold ;;
	names) names ;;
	*) echo "usage: box.sh up|down|hold|names" >&2; exit 2 ;;
esac

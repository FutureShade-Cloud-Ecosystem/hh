#!/bin/sh
# The demo image's entrypoint (plan r7 section 4, item W5): one place where
# the boot order lives. The order is the plan's G0 made executable, with
# the phrase gate's value checked before anything else:
#
#   0. refuse to start without the phrase's hash (G6, the gate's value)
#   1. wait, bounded, for the database to accept connections
#   2. classify the database's shape (empty, building, built, foreign)
#   3. core demo-check (the G0 guard) before anything writes
#   4. the image's build marker on a fresh database, then core migrate,
#      core demoseed -apply, core demo-populate, and core demoseed -apply
#      again: the quote world's owners are the two demo role logins the
#      populate plants, so the first pass through the seed can carry no
#      quote, and the seed is idempotent
#   5. core serve on a loopback port, Caddy in front on the public port
#
# Both database variables are set here from the one variable the operator
# sets (DATABASE_URL): the serve role reads CORE_DATABASE_URL and every
# one-shot reads DATABASE_URL, and the plan's answer to the one-shots'
# split naming is to derive one from the other so they cannot drift.
#
# THE MID-BOOT QUESTION, AND THE ANSWER BUILT HERE. A boot that stops
# between migrate and demo-populate leaves a database demo-check refuses:
# the migration set is applied but the populate's demo_box row is absent,
# and the guard reads that as a world that was never built. The answer is
# the image's own build marker: on a fresh database the entrypoint creates
# demo_build_marker (one row recording the build started; outside the
# migration chain, the same discipline as the populate's demo_box) before
# migrate runs, so the image only ever resumes a build it started. A
# database holding a schema with no marker is not this image's build,
# whatever it holds, and is refused with the recovery named before
# anything writes: every database of the source product below the org
# migration has exactly that shape, and none of them is the demo's (the
# review's P1-1). The resume path itself runs the populate's own refusals
# as guarded probes before it continues (no org other than the demo's, no
# user outside the migrations' planted set, no payment configuration the
# populate did not write), so a database carrying the marker but holding a
# real user or a real configuration is refused before migrate or the seed
# write a row (the review's P1-2). Every write on every path stays forward
# only and idempotent.
set -eu

log() { echo "entrypoint: $*"; }
fail() { echo "entrypoint: $*" >&2; exit 1; }

# --------------------------------------------------------------------------
# The phrase gate's value (G6, row 484 of the decision record): the shared
# phrase lives in the vault and only its hash reaches the box, as the
# runtime value DEMO_GATE_BCRYPT_HASH. The entrypoint refuses to start
# without it, and refuses a value that does not look like a bcrypt hash:
# Caddy's basic authentication wants the hash, and the likeliest mistake
# is the phrase pasted where the hash belongs. The refusal names the
# mistake and repeats no value. The variable is unset here, before any
# child process starts, so the core and Caddy inherit no copy of it; the
# value reaches Caddy's configuration generation alone, through the shell
# variable captured below, which is never exported.
# --------------------------------------------------------------------------
HASH_VALUE="$(printenv DEMO_GATE_BCRYPT_HASH || true)"
unset DEMO_GATE_BCRYPT_HASH
if [ -z "$HASH_VALUE" ]; then
	fail "DEMO_GATE_BCRYPT_HASH is not set: the phrase gate asks for the shared phrase on every page and on the mint, and the image refuses to start without the phrase's hash"
fi
case "$HASH_VALUE" in
	'$2a$'*|'$2b$'*|'$2y$'*) ;;
	*)
		fail "DEMO_GATE_BCRYPT_HASH does not look like a bcrypt hash: the gate wants the hash of the phrase and not the phrase itself; make the hash from the phrase kept in the vault, and set only the hash"
		;;
esac

# --------------------------------------------------------------------------
# The environment the image requires. The image is the demo image, so the
# box says demo twice here, by name, explicitly (the G2 values): nothing
# the operator sets can turn this image into anything else. The values the
# core needs at boot are required up front, before any one-shot writes:
# the vault key seals the invented payment credentials (a restart with a
# different key could not unseal them, so it is an operator value and not
# something the entrypoint invents), and the acceptance signing pair signs
# the customer quote acceptances.
# --------------------------------------------------------------------------

export DEMO_MODE=true
export APP_ENV=demo

for name in DATABASE_URL PAYMENT_VAULT_KEY HH_ACCEPTANCE_SIGN_KEY_ID HH_ACCEPTANCE_SIGN_KEY; do
	if [ -z "$(printenv "$name")" ]; then
		fail "$name is not set: the demo core needs it at boot, and the entrypoint refuses to start without it"
	fi
done

export CORE_DATABASE_URL="$DATABASE_URL"
export CORE_STAFF_AUDIENCE="${CORE_STAFF_AUDIENCE:-hh-staff}"
export CORE_TENANT_AUDIENCE="${CORE_TENANT_AUDIENCE:-hh-tenant}"
# The core has no default sign in mode; this image defaults it to the demo
# mode (the in memory issuer). A copy that signs real sessions sets console
# in its own environment, with the key file and issuer names the core lists.
export CORE_SIGNIN_MODE="${CORE_SIGNIN_MODE:-demo}"

# The public port is the platform's PORT. The core listens on a loopback
# port only: it is never reachable except through Caddy, which fronts
# every page and every mint request.
GATE_PORT="${PORT:-8080}"
CORE_INTERNAL_PORT="${CORE_INTERNAL_PORT:-9090}"
export CORE_LISTEN_ADDR="127.0.0.1:${CORE_INTERNAL_PORT}"
UPSTREAM="127.0.0.1:${CORE_INTERNAL_PORT}"

# --------------------------------------------------------------------------
# Step 1: a bounded wait for the database to accept connections. The
# platform may start the database beside the image, not before it; exiting
# here lets the platform retry the whole boot. Each probe is one cheap
# query, and the bound is counted in probes, not announced as a wait.
# --------------------------------------------------------------------------
PGCONNECT_TIMEOUT=2
export PGCONNECT_TIMEOUT

probes=0
until psql "$DATABASE_URL" -tAqc 'SELECT 1' >/dev/null 2>&1; do
	probes=$((probes + 1))
	if [ "$probes" -ge 60 ]; then
		fail "the database never accepted a connection: the container exits so the platform can retry the boot"
	fi
	sleep 1
done
unset PGCONNECT_TIMEOUT

# --------------------------------------------------------------------------
# Step 2: classify the database's shape. Each question is its own probe,
# so no query ever names a table that may not exist (a count over an
# absent table would fail the whole classification, because PostgreSQL
# computes aggregates before it chooses a CASE branch). The states:
#
#   empty     no schema_migrations, no marker, no user table at all: a
#             fresh database the build fills
#   foreign   anything else without this image's build marker: a schema
#             this image did not start, refused with the recovery named
#             before anything writes
#   building  the marker present, the populate's demo_box row absent: the
#             interrupted build of the demo's own world that the resume
#             path serves, after the refusals below (see the header)
#   built     the populate's row present: a whole demo world
# --------------------------------------------------------------------------
has_table() {
	[ "$(psql "$DATABASE_URL" -tAqc \
		"SELECT count(*) FROM information_schema.tables
		  WHERE table_schema = current_schema() AND table_name = '$1'")" -gt 0 ]
}

user_table_count() {
	psql "$DATABASE_URL" -tAqc \
		"SELECT count(*) FROM information_schema.tables
		  WHERE table_schema NOT IN ('pg_catalog', 'information_schema')"
}

if ! has_table schema_migrations && ! has_table demo_build_marker && [ "$(user_table_count)" -eq 0 ]; then
	STATE=empty
elif ! has_table demo_build_marker; then
	STATE=foreign
elif has_table demo_box; then
	STATE=built
else
	STATE=building
fi

# --------------------------------------------------------------------------
# The resume path's refusals (the review's P1-2): a database carrying this
# image's marker but not the populate's row is resumed only when it is
# still the world this image builds. The probes mirror the populate's own
# refusals (the exemption sets are its planted set and its one payment
# configuration, copied from core/internal/demo/populate.go and
# populate_refuse.go, and each probe guards its own table so a build
# stopped before a table existed reads as empty), and they run before
# migrate or the seed write a row. Every refusal names the recovery.
# --------------------------------------------------------------------------
resume_refusals() {
	if has_table org; then
		foreign_orgs="$(psql "$DATABASE_URL" -tAqc \
			"SELECT count(*) FROM org WHERE lower(slug::text) <> 'hh-demo'")"
		[ "$foreign_orgs" -eq 0 ] || \
			fail "the build this image started holds $foreign_orgs org row(s) other than the demo's own, so this is not the demo's world: nothing here may write to it, and the recovery is to recreate the database resource"
	fi
	if has_table users; then
		foreign_users="$(psql "$DATABASE_URL" -tAqc "
			SELECT count(*) FROM users
			 WHERE lower(email::text) <> 'scrubbed-0009@example.invalid'
			   AND id::text NOT IN (
			     'd7000000-0000-4000-9000-000000000010',
			     'd7000000-0000-4000-9000-0000000000da',
			     '40000061-0000-4000-9000-000000000001',
			     '40000061-0000-4000-9000-000000000002',
			     '40000061-0000-4000-9000-000000000003',
			     '40000061-0000-4000-9000-000000000004',
			     'd0200000-0000-4000-9000-000000000001',
			     'd0200000-0000-4000-9000-000000000002',
			     'd0200000-0000-4000-9000-000000000003',
			     'de110000-0000-4000-9000-000000000001',
			     'de110000-0000-4000-9000-000000000002')")"
		[ "$foreign_users" -eq 0 ] || \
			fail "the build this image started holds $foreign_users user row(s) outside the migrations' planted set and the demo accounts, so this is not the demo's world: nothing here may write to it, and the recovery is to recreate the database resource"
	fi
	if has_table payment_config; then
		foreign_pay="$(psql "$DATABASE_URL" -tAqc \
			"SELECT count(*) FROM payment_config WHERE id <> 'de110000-0000-4000-9000-0000000000ca'::uuid")"
		[ "$foreign_pay" -eq 0 ] || \
			fail "the build this image started holds $foreign_pay payment configuration row(s) the demo populate did not write, so this is not the demo's world: nothing here may write to it, and the recovery is to recreate the database resource"
	fi
}

case "$STATE" in
	empty|built)
		# Step 3 on the ordinary paths: demo-check first, before anything
		# writes. It re-reads the box statement, holds both database
		# variables to one value, and passes only a fresh database or a
		# whole demo world.
		log "the database is $STATE: demo-check runs before anything writes"
		core demo-check
		;;
	building)
		resume_refusals
		log "resuming the build this image started (its marker row is present, the populate's row absent): the populate's own refusals ran first, every step idempotent and forward only, demo-check before serve"
		;;
	foreign)
		fail "the database holds a schema but not this image's build marker, so this image did not start the build and writes nothing to it: the recovery is to recreate the database resource, and the demo world builds from nothing on the fresh database"
		;;
	*)
		fail "the database's shape could not be classified (state: $STATE): the container exits so the platform can retry the boot"
		;;
esac

# --------------------------------------------------------------------------
# Step 4: the marker on a fresh database, then the build, in the plan's
# order, idempotent on every path. The marker is written before migrate
# runs, so only builds this image started are ever resumed (the review's
# P1-1); it lives outside the migration chain like the populate's demo_box,
# and nothing but this script ever writes it. migrate applies only what it
# has not, the seed writes only its missing rows in one transaction, the
# populate completes the world and writes its own marker row.
#
# The seed applies once more after the populate. The quote world is
# conditional on the two demo role logins (its owners must read as Inside
# Sales and Sales manager through the platform link and the staff role
# grant, which only the populate's accounts carry), and the populate plants
# those logins after the seed's first pass, so one boot seeds the quotes
# only if the boot applies the seed again once they exist. Both passes are
# idempotent: on a built database each writes nothing.
# --------------------------------------------------------------------------
if [ "$STATE" = empty ]; then
	psql "$DATABASE_URL" -qc "CREATE TABLE IF NOT EXISTS demo_build_marker (started timestamptz NOT NULL DEFAULT now())"
	psql "$DATABASE_URL" -qc "INSERT INTO demo_build_marker SELECT now() WHERE NOT EXISTS (SELECT 1 FROM demo_build_marker)"
fi

core migrate
core demoseed -apply
core demo-populate
core demoseed -apply

if [ "$STATE" = building ]; then
	# The resume path's fail-closed gate: now that the world is whole, the
	# G0 guard speaks, and a build that did not complete stops here instead
	# of reaching serve.
	core demo-check
fi

# --------------------------------------------------------------------------
# Step 5: serve behind Caddy. The core starts first (a refusal above is a
# refusal before any listener), the entrypoint waits for its health, then
# Caddy takes the public port. The monitor loop tears the pair down
# together: whichever side stops, the container exits and the platform
# retries the whole boot, where the classifier above makes the retry safe.
# --------------------------------------------------------------------------
core serve &
CORE_PID=$!

probes=0
until wget -q -O /dev/null "http://127.0.0.1:${CORE_INTERNAL_PORT}/healthz" 2>/dev/null; do
	if ! kill -0 "$CORE_PID" 2>/dev/null; then
		fail "the core exited before it answered its health: its refusal is in the output above"
	fi
	probes=$((probes + 1))
	if [ "$probes" -ge 60 ]; then
		kill "$CORE_PID" 2>/dev/null || true
		fail "the core never answered its health check: the container exits so the platform can retry the boot"
	fi
	sleep 1
done

# Caddy autosaves the adapted configuration for its resume feature. With
# the phrase gate in it, the autosave would hold the phrase's hash, so
# Caddy's configuration and data homes are pointed at the container's
# shared-memory tmpfs: no hash reaches the image's layers, any volume, or
# anything that outlives the container, and the gate's configuration is
# piped in on standard input, never written by this script.
export XDG_CONFIG_HOME=/dev/shm/caddy-config
export XDG_DATA_HOME=/dev/shm/caddy-data

GATE_CONFIG="$(sed -e "s|__GATE_PORT__|${GATE_PORT}|g" \
                    -e "s|__UPSTREAM__|${UPSTREAM}|g" \
                    -e "s|__GATE_HASH__|${HASH_VALUE}|g" \
                    /srv/templates/Caddyfile.template)"

printf '%s\n' "$GATE_CONFIG" | caddy run --config - --adapter caddyfile &
CADDY_PID=$!
# The pipeline forked above carries its own copy of both values; the
# parent's copies go now, so no shell variable holds the hash a moment
# longer than the gate's configuration needs it.
unset GATE_CONFIG HASH_VALUE

trap 'kill "$CORE_PID" "$CADDY_PID" 2>/dev/null; exit 0' TERM INT

while :; do
	if ! kill -0 "$CORE_PID" 2>/dev/null; then
		kill "$CADDY_PID" 2>/dev/null || true
		fail "the core stopped: the container exits so the platform retries the boot, where an interrupted build resumes"
	fi
	if ! kill -0 "$CADDY_PID" 2>/dev/null; then
		kill "$CORE_PID" 2>/dev/null || true
		fail "the proxy stopped: the container exits so the platform retries the boot"
	fi
	sleep 1
done

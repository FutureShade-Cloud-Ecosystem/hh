#!/bin/sh
# The finance image's entrypoint (the deploy spec FINANCE-BOX-SPEC-r1,
# sections 2, 3, 4 and 6): the one place the boot order lives.
#
#   0. the variable checks: one line per missing name, never a value, and
#      the box statement values (HH_BOX_KIND, APP_ENV, CORE_SIGNIN_MODE)
#      checked for the exact required words
#   1. the one database name held in both spellings: a differing
#      DATABASE_URL / CORE_DATABASE_URL pair refuses before anything runs,
#      and with only one set both spellings carry its target
#   2. the console copy's session signing key made private where the
#      platform mounted it too open (the demo entrypoint's discipline,
#      copied verbatim below)
#   3. the web runtime config (/srv/app/config.json) written from the
#      HH_WEB_* names, console mode
#   4. a bounded wait for the database to accept connections
#   5. the database's shape classified: EMPTY means migrate then
#      finance-seed; NON EMPTY means the box kind check (core
#      finance-box-check, which refuses before anything writes) then
#      migrate, forward only on every path
#   6. core serve, core worker and Caddy under the supervisor: the
#      entrypoint exits non zero as soon as any one of the three exits, so
#      the platform restarts the container. No signals are sent by this
#      script: the children are wrapped so each records its own exit, and
#      the supervisor reads the record. When the platform stops the
#      container, the trap exits zero and the stop takes the children with
#      it.
#
# ONE REPLICA ONLY (the spec's section 2): the worker runs the jobs runner
# as a single instance with no leader election, so the box never scales out.
set -eu

log() { echo "entrypoint: $*"; }
fail() { echo "entrypoint: $*" >&2; exit 1; }

# The image layout root: the web root and the templates directory live
# under it. In the image it is /srv; the override exists so the entrypoint
# can be exercised from a checkout without touching /srv.
FINANCE_ROOT="${FINANCE_ROOT:-/srv}"

# --------------------------------------------------------------------------
# Step 0: the variable checks. Every value the box and the core need at boot
# is a required name; a missing one refuses with one line naming it, and a
# box statement value that is not the required word refuses naming the word.
# Nothing below this block runs when any check fails: no child process, no
# database call, no file write.
#
# Step 0a, before the checks: the sign in allow list, derived from the box's
# finance user list. HH_FINANCE_USERS is the one source of truth for the
# people on the box, so the allow list is derived from it here instead of
# being set beside it. A list and an allow list that disagree refuse the
# boot, naming the two variables and never an address. With no list the
# allow list is left as it is, and the check below still asks for it.
# --------------------------------------------------------------------------
# shellcheck source=signin-allow-list.sh
. "$(dirname "$0")/signin-allow-list.sh"
derive_signin_allow_list || exit 1

for name in \
	DATABASE_URL APP_ENV HH_BOX_KIND CORE_SIGNIN_MODE \
	CORE_TOKEN_ISSUER CORE_STAFF_AUDIENCE CORE_TENANT_AUDIENCE \
	CORE_SESSION_SIGNING_KEY_FILE \
	CORE_APPWRITE_ENDPOINT CORE_APPWRITE_PROJECT CORE_APPWRITE_API_KEY \
	CORE_SIGNIN_EMAIL_ADDRESSES \
	PAYMENT_VAULT_KEY PORTAL_BASE_URL ERP_BASE_URL PORT \
	HH_ACCEPTANCE_SIGN_KEY_ID HH_ACCEPTANCE_SIGN_KEY \
	HH_WEB_MODE HH_WEB_ORG HH_WEB_CONSOLE_ENDPOINT HH_WEB_CONSOLE_PROJECT_ID \
	HH_WEB_DEALER_NAME; do
	if [ -z "$(printenv "$name")" ]; then
		fail "$name is not set: the finance box needs it at boot, and the entrypoint refuses to start without it"
	fi
done

[ "$HH_BOX_KIND" = finance ] || \
	fail "HH_BOX_KIND must be finance on this image: this image is the finance box and plants no other kind of world"
[ "$APP_ENV" = staging ] || \
	fail "APP_ENV must be staging on the finance box: the box runs the stub payment rails, and the rails refuse production"
[ "$CORE_SIGNIN_MODE" = console ] || \
	fail "CORE_SIGNIN_MODE must be console on the finance box: the default build accepts no other mode"

# Step 1: one database in both spellings. A differing pair is the operator
# pointing the box's two readers at two databases: refused here, one line
# naming the pair and never a value, before anything runs. One set name is
# enough: DATABASE_URL is required above, so with CORE_DATABASE_URL unset
# both spellings carry DATABASE_URL's target.
if [ -n "${CORE_DATABASE_URL:-}" ] && [ "$CORE_DATABASE_URL" != "$DATABASE_URL" ]; then
	fail "DATABASE_URL and CORE_DATABASE_URL name different targets: set both to the same database, or set DATABASE_URL alone and leave CORE_DATABASE_URL unset"
fi
if [ -z "${CORE_DATABASE_URL:-}" ]; then
	CORE_DATABASE_URL="$DATABASE_URL"
fi
export CORE_DATABASE_URL

# --------------------------------------------------------------------------
# Step 2: the console copy's session signing key (the demo entrypoint's
# discipline, copied verbatim): FB Deploy's file storage mounts files as
# 0755 root:root and its API refuses chmod, while the core refuses a key
# readable by group or other. A key whose mode is outside 0400 and 0600 is
# copied once into a private path and the variable is pointed at the copy
# before any core process starts. The key's bytes are never printed and
# never copied anywhere else, and a missing or unreadable key refuses the
# boot. The secrets directory comes only from the function's argument
# (never from the environment) and must live under /run/.
# --------------------------------------------------------------------------
# BEGIN ensure_signing_key
ensure_signing_key() {
	secrets_dir="${1:-/run/hh-secrets}"
	case "$secrets_dir" in
		/run/*) ;;
		*) fail "the signing key's private directory must live under /run/, and $secrets_dir does not: the entrypoint refuses to place the copy anywhere else" ;;
	esac
	key_file="${CORE_SESSION_SIGNING_KEY_FILE:-}"
	if [ -z "$key_file" ]; then
		fail "CORE_SESSION_SIGNING_KEY_FILE is not set: a console copy needs the session signing key's path, and the entrypoint refuses to start without it"
	fi
	if [ ! -f "$key_file" ] || [ ! -r "$key_file" ]; then
		fail "CORE_SESSION_SIGNING_KEY_FILE is not a readable file: the entrypoint refuses to start without the session signing key"
	fi
	key_mode="$(stat -c '%a' "$key_file")" || \
		fail "CORE_SESSION_SIGNING_KEY_FILE's mode could not be read: the entrypoint refuses to start without knowing it"
	# The owner-only modes (0400, 0600) carry no group, other or set
	# bits; any other mode is a mounted key the core would refuse at boot.
	if [ "$((0$key_mode & 0177))" -eq 0 ]; then
		return 0
	fi
	mkdir -p "$secrets_dir" || \
		fail "the private secrets directory $secrets_dir could not be created: the mounted signing key cannot be made private"
	chmod 0700 "$secrets_dir" 2>/dev/null || true
	rm -f "$secrets_dir/session.pem"
	( umask 077 && cp "$key_file" "$secrets_dir/session.pem" ) || \
		fail "the mounted signing key could not be copied into $secrets_dir: the entrypoint refuses to start on the too-open mount"
	chmod 0400 "$secrets_dir/session.pem"
	CORE_SESSION_SIGNING_KEY_FILE="$secrets_dir/session.pem"
	export CORE_SESSION_SIGNING_KEY_FILE
	log "the mounted signing key was readable by group or other, so the core reads a private copy at $CORE_SESSION_SIGNING_KEY_FILE (the mounted file is untouched)"
}
# END ensure_signing_key

ensure_signing_key

# --------------------------------------------------------------------------
# Step 3: the web runtime config. The image bakes no sign in mode; the
# entrypoint writes /srv/app/config.json from the HH_WEB_* names, validated
# by write-web-config.sh with the same rules as the browser's parser. A
# refusal names the variable and never repeats its value.
# --------------------------------------------------------------------------
# shellcheck source=write-web-config.sh
. "$(dirname "$0")/write-web-config.sh"
write_web_config "$FINANCE_ROOT/app/config.json" || exit 1

# --------------------------------------------------------------------------
# The ports. The public port is the platform's PORT; the core listens on a
# loopback port only and is never reachable except through Caddy. Only this
# script reads CORE_INTERNAL_PORT (the core is told its address by
# CORE_LISTEN_ADDR), so it is un-exported before any child starts.
# --------------------------------------------------------------------------
GATE_PORT="$PORT"
CORE_INTERNAL_PORT="${CORE_INTERNAL_PORT:-9090}"
export CORE_LISTEN_ADDR="127.0.0.1:${CORE_INTERNAL_PORT}"
UPSTREAM="127.0.0.1:${CORE_INTERNAL_PORT}"
internal_port="$CORE_INTERNAL_PORT"
unset CORE_INTERNAL_PORT
CORE_INTERNAL_PORT="$internal_port"
unset internal_port

# --------------------------------------------------------------------------
# Step 4: a bounded wait for the database to accept connections. The
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
# Step 5: classify the database's shape and act on it. EMPTY (no schema
# bookkeeping at all) is a fresh database: migrate, then finance-seed, which
# applies the seed, records box_kind=finance through the settings module
# and leaves the seed's users with no usable password. NON EMPTY is a
# returning database: the box kind check runs BEFORE migrate and refuses a
# database the box did not seed, so a box pointed at the testers' copy, a
# staging world or a demo world writes nothing. migrate then runs forward
# only, applying what the new image added since the last boot.
# --------------------------------------------------------------------------
has_table() {
	[ "$(psql "$DATABASE_URL" -tAqc \
		"SELECT count(*) FROM information_schema.tables
		  WHERE table_schema = current_schema() AND table_name = '$1'")" -gt 0 ]
}

if ! has_table schema_migrations; then
	log "the database is empty: migrate, then finance-seed"
	core migrate
	core finance-seed
else
	log "the database is not empty: the box kind check runs before migrate"
	core finance-box-check
	core migrate
fi

# --------------------------------------------------------------------------
# Step 5a: the box's finance users, applied from the list the environment
# carries. Every entry is provisioned and every finance person the list left
# out is revoked, in one transaction, so the box is the list. The apply is
# convergent, so a boot with a changed list brings the box to the list and a
# boot with the same list changes nothing. With no list in the environment
# there is nobody to apply, and the box's people are the ones the operator
# linked with core finance-user.
# --------------------------------------------------------------------------
if [ -n "${HH_FINANCE_USERS:-}" ]; then
	log "the box's finance user list is applied"
	core finance-user --apply-from-env
fi

# --------------------------------------------------------------------------
# Step 6: serve, worker and Caddy under the supervisor. Each child runs in
# a wrapper that records its own exit (the label and the status) in the
# exits file; the supervisor reads the file. As soon as any one of the
# three has exited, the entrypoint exits non zero, so the platform restarts
# the container: a box serving with a dead worker would stop posting
# journals quietly. No signals are sent by this script: the wrappers write
# the record themselves, the health loop reads it, and the platform's stop
# is answered by the trap alone. Everything lives on the shared-memory
# tmpfs and leaves nothing in the image's layers.
# --------------------------------------------------------------------------
EXIT_FILE="/dev/shm/finance-box-exits.$$"
: > "$EXIT_FILE"
CADDY_FILE=""
trap 'rm -f "$EXIT_FILE" "$CADDY_FILE" 2>/dev/null || true' EXIT

run_child() {
	child_label="$1"
	shift
	(
		set +e
		"$@"
		child_rc=$?
		printf '%s:%s\n' "$child_label" "$child_rc" >> "$EXIT_FILE"
		exit "$child_rc"
	) &
}

# The core starts first: a refusal above is a refusal before any listener.
run_child serve core serve

probes=0
until wget -q -O /dev/null "http://127.0.0.1:${CORE_INTERNAL_PORT}/healthz" 2>/dev/null; do
	if grep -q '^serve:' "$EXIT_FILE" 2>/dev/null; then
		fail "core serve exited before it answered its health: its refusal is in the output above"
	fi
	probes=$((probes + 1))
	if [ "$probes" -ge 60 ]; then
		fail "the core never answered its health check: the container exits so the platform can retry the boot"
	fi
	sleep 1
done

run_child worker core worker

# Caddy runs from the adapted template on the shared-memory tmpfs; its
# autosave homes are pointed there too, so nothing generated reaches the
# image's layers, a volume, or anything that outlives the container.
export XDG_CONFIG_HOME=/dev/shm/caddy-config
export XDG_DATA_HOME=/dev/shm/caddy-data

CADDY_FILE="/dev/shm/finance-box-Caddyfile.$$"
sed -e "s|__PORT__|${GATE_PORT}|g" \
    -e "s|__UPSTREAM__|${UPSTREAM}|g" \
    "$FINANCE_ROOT/templates/Caddyfile.template" > "$CADDY_FILE"

run_child proxy caddy run --config "$CADDY_FILE" --adapter caddyfile

# The platform's stop is a clean stop, not a failure: exit zero. A child's
# own exit is the failure path below.
trap 'exit 0' TERM INT

while :; do
	if [ -s "$EXIT_FILE" ]; then
		exited="$(head -n 1 "$EXIT_FILE")"
		fail "a child exited (${exited%%:*}): the container exits non zero so the platform can restart it"
	fi
	sleep 1
done

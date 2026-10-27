#!/bin/sh
# The finance entrypoint's supervisor, without Docker: stand-in children on
# the PATH record their invocations and exit by themselves (never a
# signal), and the entrypoint must exit non zero as soon as any one of the
# three (serve, worker, Caddy) has exited. Two database shapes are walked:
# the empty path (core migrate, then core finance-seed) and the non empty
# one (core finance-box-check, then core migrate, no seed). Run from a
# checkout:
#
#   sh deploy/finance/entrypoint-supervisor-test.sh
#
# Every stand-in child exits on its own, so nothing is left running when
# the script ends: no detached processes, no signals.
set -eu
cd "$(git rev-parse --show-toplevel)"

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

mkdir -p "$SCRATCH/bin" "$SCRATCH/finance-root/app" "$SCRATCH/finance-root/templates"
cp deploy/finance/Caddyfile.template "$SCRATCH/finance-root/templates/Caddyfile.template"
printf '%s\n' '-----BEGIN EC PRIVATE KEY-----' 'throwaway-test-key-material' '-----END EC PRIVATE KEY-----' > "$SCRATCH/session.pem"
chmod 0600 "$SCRATCH/session.pem"

# The stand-ins. Each logs its invocation and exits; the worker's exit 3 is
# the failure the supervisor must catch. psql answers the connection probe
# and the shape question (schema_migrations present or not, per the
# scenario file). wget answers the health probe; nothing in this test binds
# a port.
cat > "$SCRATCH/bin/psql" <<'EOF'
#!/bin/sh
case "$3" in
	*'SELECT 1'*) echo 1 ;;
	*"table_name = 'schema_migrations'"*) cat /dev/shm/finance-supervisor-test-has-migrations 2>/dev/null || echo 0 ;;
	*) echo 0 ;;
esac
exit 0
EOF
cat > "$SCRATCH/bin/core" <<'EOF'
#!/bin/sh
echo "core $*" >> /dev/shm/finance-supervisor-test-calls
case "$1" in
	worker) exit 3 ;;
	*) exit 0 ;;
esac
EOF
cat > "$SCRATCH/bin/caddy" <<'EOF'
#!/bin/sh
echo "caddy $*" >> /dev/shm/finance-supervisor-test-calls
exit 0
EOF
cat > "$SCRATCH/bin/wget" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$SCRATCH/bin/psql" "$SCRATCH/bin/core" "$SCRATCH/bin/caddy" "$SCRATCH/bin/wget"

run_supervisor() {
	has_migrations="$1"
	CALLS=/dev/shm/finance-supervisor-test-calls
	HAS=/dev/shm/finance-supervisor-test-has-migrations
	: > "$CALLS"
	echo "$has_migrations" > "$HAS"
	rc=0
	env \
		PATH="$SCRATCH/bin:$PATH" \
		DATABASE_URL='postgres://unused:unused@127.0.0.1:1/unused' \
		APP_ENV=staging \
		HH_BOX_KIND=finance \
		CORE_SIGNIN_MODE=console \
		CORE_TOKEN_ISSUER=finbox-test-issuer \
		CORE_STAFF_AUDIENCE=finbox-test-staff \
		CORE_TENANT_AUDIENCE=finbox-test-tenant \
		CORE_SESSION_SIGNING_KEY_FILE="$SCRATCH/session.pem" \
		CORE_APPWRITE_ENDPOINT=https://console.example.test \
		CORE_APPWRITE_PROJECT=finbox-test-project \
		CORE_APPWRITE_API_KEY=finbox-test-key-placeholder \
		CORE_SIGNIN_EMAIL_ADDRESSES=tester@example.test \
		PAYMENT_VAULT_KEY=0000000000000000000000000000000000000000000000000000000000000000 \
		PORTAL_BASE_URL=https://finance-box.example.test \
		ERP_BASE_URL=https://finance-box.example.test \
		PORT=57726 \
		HH_ACCEPTANCE_SIGN_KEY_ID=finbox-test-accept-key \
		HH_ACCEPTANCE_SIGN_KEY=0000000000000000000000000000000000000000000000000000000000000000 \
		HH_WEB_MODE=console \
		HH_WEB_ORG=finbox \
		HH_WEB_CONSOLE_ENDPOINT=https://console.example.test \
		HH_WEB_CONSOLE_PROJECT_ID=finbox-test-project \
		HH_WEB_DEALER_NAME='Invented Test Dealer' \
		FINANCE_ROOT="$SCRATCH/finance-root" \
		sh deploy/finance/entrypoint.sh 2>&1 || rc=$?
	cat "$CALLS" > "$SCRATCH/last-calls"
	return "$rc"
}

# Scenario A: the empty database. migrate then finance-seed run, then the
# children start, and the worker's own exit takes the container down.
if out="$(run_supervisor 0)"; then
	fail "on the empty path the entrypoint exited zero though a child died"
else
	pass "on the empty path a child's own exit takes the entrypoint non zero"
fi
case "$out" in
	*"a child exited"*) pass "the supervisor names the child exit" ;;
	*) fail "the supervisor does not name the child exit: $out" ;;
esac
calls="$(cat "$SCRATCH/last-calls")"
case "$calls" in
	*"core migrate"*"core finance-seed"*) pass "the empty path migrates then seeds" ;;
	*) fail "the empty path order is wrong: $calls" ;;
esac
case "$calls" in
	*"finance-box-check"*) fail "the empty path must not run the box kind check: $calls" ;;
	*) pass "the empty path runs no box kind check" ;;
esac
case "$calls" in
	*"core serve"*"core worker"*"caddy run"*) pass "all three children started" ;;
	*) fail "not all three children started: $calls" ;;
esac
[ -f "$SCRATCH/finance-root/app/config.json" ] && \
	pass "the web config was written" || \
	fail "the web config was not written"

# Scenario B: the non empty database. The box kind check runs before
# migrate; the seed never runs; the worker's exit still takes the container
# down.
if out="$(run_supervisor 1)"; then
	fail "on the non empty path the entrypoint exited zero though a child died"
else
	pass "on the non empty path a child's own exit takes the entrypoint non zero"
fi
calls="$(cat "$SCRATCH/last-calls")"
case "$calls" in
	*"core finance-box-check"*"core migrate"*) pass "the non empty path checks the box kind before migrating" ;;
	*) fail "the non empty path order is wrong: $calls" ;;
esac
case "$calls" in
	*"finance-seed"*) fail "the non empty path must not seed: $calls" ;;
	*) pass "the non empty path runs no seed" ;;
esac
rm -f /dev/shm/finance-supervisor-test-calls /dev/shm/finance-supervisor-test-has-migrations

if [ "$FAILURES" -ne 0 ]; then
	echo "$FAILURES failure(s)"
	exit 1
fi
echo "all passed"

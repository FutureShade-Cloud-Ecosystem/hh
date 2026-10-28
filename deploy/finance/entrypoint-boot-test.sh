#!/bin/sh
# The finance entrypoint's boot legs, without Docker and without a database:
# the ports that are written into the Caddy template, the database URL that
# must never sit on a command line, and the database's shape as the core
# classifies it. Run from a checkout:
#
#   sh deploy/finance/entrypoint-boot-test.sh
#
# The core and psql are stand-ins on the PATH: the stand-in core records the
# role it was asked to run and answers the state the leg wants, and the
# stand-in psql records its arguments and the service file it was pointed at.
# So the legs prove what the entrypoint DOES, with no image, no container and
# no database, and the records are read after the run.
set -eu
cd "$(git rev-parse --show-toplevel)"

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

# -- the parser ----------------------------------------------------------------

# The URL parser on its own: the legs read the service file it writes and
# check that the URL never appears in it whole.
# shellcheck source=deploy/finance/database-url.sh
. deploy/finance/database-url.sh

write_service() {
	# write_service URL -> writes the service file, prints its path
	pg_path="$SCRATCH/service.$$"
	if ! pg_service_write "$pg_path" "$1"; then
		rm -f "$pg_path"
		return 1
	fi
	printf '%s\n' "$pg_path"
}

# A plain URL: the fields are read and written as they stand.
if path="$(write_service 'postgres://boxuser:boxpass@127.0.0.1:57726/finance_box')"; then
	service="$(cat "$path")"
	case "$service" in
		*"host=127.0.0.1"*) pass "the service file carries the host" ;;
		*) fail "the service file does not carry the host: $service" ;;
	esac
	case "$service" in
		*"port=57726"*) pass "the service file carries the port" ;;
		*) fail "the service file does not carry the port: $service" ;;
	esac
	case "$service" in
		*"dbname=finance_box"*) pass "the service file carries the database" ;;
		*) fail "the service file does not carry the database: $service" ;;
	esac
	case "$service" in
		*"user=boxuser"*) pass "the service file carries the user" ;;
		*) fail "the service file does not carry the user: $service" ;;
	esac
	case "$service" in
		*"password=boxpass"*) pass "the service file carries the password" ;;
		*) fail "the service file does not carry the password: $service" ;;
	esac
	mode="$(stat -c '%a' "$path")"
	if [ "$((0$mode & 0177))" -eq 0 ]; then
		pass "the service file is owner only (mode $mode)"
	else
		fail "the service file is readable by group or other (mode $mode)"
	fi
	rm -f "$path"
else
	fail "a plain URL was refused"
fi

# A URL with parameters: every parameter is a libpq connection parameter and
# is written under its own name.
if path="$(write_service 'postgres://boxuser:boxpass@db.example.test:5432/finance_box?sslmode=disable&connect_timeout=9')"; then
	service="$(cat "$path")"
	case "$service" in
		*"sslmode=disable"*) pass "the service file carries sslmode" ;;
		*) fail "the service file does not carry sslmode: $service" ;;
	esac
	case "$service" in
		*"connect_timeout=9"*) pass "the service file carries connect_timeout" ;;
		*) fail "the service file does not carry connect_timeout: $service" ;;
	esac
	rm -f "$path"
else
	fail "a URL with parameters was refused"
fi

# A parameter name that carries a raw line break: refused, like a value with
# one would be. The name is written into the service file, and the file is
# the only place the password lives, so an extra line there is not a shape
# the box writes.
newline_url="$(printf 'postgres://boxuser:boxpass@127.0.0.1:57726/finance_box?ssl\nmode=disable')"
if pg_service_write "$SCRATCH/newline-key-service" "$newline_url" 2>/dev/null; then
	fail "a parameter name with a raw line break was accepted"
else
	pass "a parameter name with a raw line break is refused"
fi
case "${PG_URL_ERROR:-}" in
	*DATABASE_URL*) pass "the raw line break refusal names DATABASE_URL" ;;
	*) fail "the raw line break refusal does not name DATABASE_URL: ${PG_URL_ERROR:-}" ;;
esac

# A percent encoded line break in a parameter name: the name is decoded
# before the check, so the encoded newline is refused the same way.
if pg_service_write "$SCRATCH/enc-newline-key-service" 'postgres://boxuser:boxpass@127.0.0.1:57726/finance_box?ssl%0Amode=disable' 2>/dev/null; then
	fail "a percent encoded line break in a parameter name was accepted"
else
	pass "a percent encoded line break in a parameter name is refused"
fi

# A percent encoded service parameter: libpq decodes parameter names when it
# reads the URI, so the encoded spelling IS the service parameter, and it is
# refused the same way the plain one is.
if pg_service_write "$SCRATCH/enc-svc-service" 'postgres://boxuser:boxpass@127.0.0.1:57726/finance_box?%73ervice=other' 2>/dev/null; then
	fail "a percent encoded service parameter was accepted"
else
	pass "a percent encoded service parameter is refused"
fi

# A percent encoded parameter name without a line break: decoded, because
# libpq reads the URI's names decoded, so the service file carries the name
# the URI meant.
if path="$(write_service 'postgres://boxuser:boxpass@127.0.0.1:57726/finance_box?sslmo%64e=disable')"; then
	service="$(cat "$path")"
	case "$service" in
		*"sslmode=disable"*) pass "a percent encoded parameter name is decoded" ;;
		*) fail "a percent encoded parameter name was not decoded: $service" ;;
	esac
	rm -f "$path"
else
	fail "a percent encoded parameter name was refused"
fi

# A percent encoded password: the escape is decoded, because libpq sends the
# password as it stands.
if path="$(write_service 'postgres://boxuser:p%40ss%20word@127.0.0.1:57726/finance_box')"; then
	service="$(cat "$path")"
	case "$service" in
		*"password=p@ss word"*) pass "the percent escapes in the password are decoded" ;;
		*) fail "the percent escapes in the password are not decoded: $service" ;;
	esac
	rm -f "$path"
else
	fail "a percent encoded URL was refused"
fi

# A URL with no port: the port is left to libpq's default.
if path="$(write_service 'postgres://boxuser:boxpass@db.example.test/finance_box')"; then
	service="$(cat "$path")"
	case "$service" in
		*"port="*) fail "the service file invents a port: $service" ;;
		*) pass "the service file leaves the port to libpq" ;;
	esac
	rm -f "$path"
else
	fail "a URL with no port was refused"
fi

# A URL with no userinfo: the user and the password are left out.
if path="$(write_service 'postgres://db.example.test:5432/finance_box')"; then
	service="$(cat "$path")"
	case "$service" in
		*"user="*|*"password="*) fail "the service file invents a user: $service" ;;
		*) pass "the service file leaves the user to libpq" ;;
	esac
	rm -f "$path"
else
	fail "a URL with no userinfo was refused"
fi

# A bracketed IPv6 host: the port follows the bracket.
if path="$(write_service 'postgres://boxuser:boxpass@[2001:db8::1]:57726/finance_box')"; then
	service="$(cat "$path")"
	case "$service" in
		*"host=2001:db8::1"*) pass "the service file carries the IPv6 host" ;;
		*) fail "the service file does not carry the IPv6 host: $service" ;;
	esac
	case "$service" in
		*"port=57726"*) pass "the service file carries the port after the bracket" ;;
		*) fail "the service file does not carry the port after the bracket: $service" ;;
	esac
	rm -f "$path"
else
	fail "a URL with an IPv6 host was refused"
fi

# A URL that is not a postgres URL: refused, naming the variable and never
# a value.
if pg_service_write "$SCRATCH/bad-service" 'mysql://boxuser:boxpass@127.0.0.1:3306/finance_box' 2>/dev/null; then
	fail "a URL that is not a postgres URL was accepted"
else
	pass "a URL that is not a postgres URL is refused"
fi
case "${PG_URL_ERROR:-}" in
	*DATABASE_URL*) pass "the refusal names DATABASE_URL" ;;
	*) fail "the refusal does not name DATABASE_URL: ${PG_URL_ERROR:-}" ;;
esac
case "${PG_URL_ERROR:-}" in
	*"mysql"*) fail "the refusal prints a value: ${PG_URL_ERROR:-}" ;;
	*) pass "the refusal prints no value" ;;
esac

# A URL with no database: refused, naming the variable.
if pg_service_write "$SCRATCH/nodb-service" 'postgres://boxuser:boxpass@127.0.0.1:57726' 2>/dev/null; then
	fail "a URL with no database was accepted"
else
	pass "a URL with no database is refused"
fi

# -- the ports -----------------------------------------------------------------

# The port block is the entrypoint's own text, taken from between its
# markers, so the leg runs the same lines the entrypoint runs.
PORT_BLOCK="$(sed -n '/^# BEGIN internal port$/,/^# END internal port$/p' deploy/finance/entrypoint.sh | sed '1d;$d')"

run_port_block() {
	# run_port_block PORT CORE_INTERNAL_PORT: runs the block and prints what
	# it says. The second argument may be empty, which means the variable is
	# unset.
	(
		fail() { echo "entrypoint: $*" >&2; exit 1; }
		export PORT="$1"
		if [ -n "$2" ]; then
			export CORE_INTERNAL_PORT="$2"
		else
			unset CORE_INTERNAL_PORT
		fi
		eval "$PORT_BLOCK"
		echo "listen=$CORE_LISTEN_ADDR"
	) 2>&1
}

# Digits only: the port is written into the Caddy template.
if out="$(run_port_block 57726 '')"; then
	pass "a digits PORT is accepted"
else
	fail "a digits PORT was refused: $out"
fi
case "$out" in
	*"127.0.0.1:9090"*) pass "CORE_INTERNAL_PORT defaults to 9090" ;;
	*) fail "CORE_INTERNAL_PORT did not default to 9090: $out" ;;
esac
if out="$(run_port_block 57726 9191)"; then
	pass "a digits CORE_INTERNAL_PORT is accepted"
else
	fail "a digits CORE_INTERNAL_PORT was refused: $out"
fi
case "$out" in
	*"127.0.0.1:9191"*) pass "CORE_INTERNAL_PORT is used as it stands" ;;
	*) fail "CORE_INTERNAL_PORT was not used as it stands: $out" ;;
esac

for bad in '80x' '57726; rm -rf /' '-1' '57 726'; do
	if out="$(run_port_block "$bad" '')"; then
		fail "PORT=$bad was accepted"
	else
		pass "PORT=$bad is refused"
	fi
	case "$out" in
		*PORT*) pass "the refusal names PORT" ;;
		*) fail "the refusal does not name PORT: $out" ;;
	esac
done
if out="$(run_port_block 57726 '90x')"; then
	fail "a CORE_INTERNAL_PORT that is not digits was accepted"
else
	pass "a CORE_INTERNAL_PORT that is not digits is refused"
fi
case "$out" in
	*CORE_INTERNAL_PORT*) pass "the refusal names CORE_INTERNAL_PORT" ;;
	*) fail "the refusal does not name CORE_INTERNAL_PORT: $out" ;;
esac

# -- the boot flow ------------------------------------------------------------

BIN="$SCRATCH/bin"
mkdir -p "$BIN"

# The stand-in core: it records the role it was asked to run and answers the
# state the leg wants.
cat > "$BIN/core" <<'STUB'
#!/bin/sh
printf 'core %s\n' "$*" >> "$STUB_RECORD"
case "$1" in
	finance-box-state)
		printf '%s\n' "$STUB_STATE"
		;;
	finance-box-check)
		[ "$STUB_CHECK" = pass ] || exit 1
		;;
esac
exit 0
STUB
chmod +x "$BIN/core"

# The stand-in psql: it records its arguments and the service file it was
# pointed at, and answers the shape query the way a migrated database would.
cat > "$BIN/psql" <<'STUB'
#!/bin/sh
printf 'psql %s\n' "$*" >> "$STUB_RECORD"
printf 'service_file=%s\n' "${PGSERVICEFILE:-}" >> "$STUB_RECORD"
if [ -n "${PGSERVICEFILE:-}" ]; then
	cat "$PGSERVICEFILE" >> "$STUB_RECORD"
fi
case "$*" in
	*information_schema.tables*) printf '1\n' ;;
esac
exit 0
STUB
chmod +x "$BIN/psql"

# The web root the entrypoint writes its runtime config into.
mkdir -p "$SCRATCH/finance-root/app" "$SCRATCH/finance-root/templates"
printf '%s\n' '-----BEGIN EC PRIVATE KEY-----' 'throwaway-test-key-material' '-----END EC PRIVATE KEY-----' > "$SCRATCH/session.pem"
chmod 0600 "$SCRATCH/session.pem"
cp deploy/finance/Caddyfile.template "$SCRATCH/finance-root/templates/Caddyfile.template"

run_box() {
	# run_box STATE CHECK: runs the entrypoint with the stand-ins and prints
	# what it said.
	rm -f "$SCRATCH/record"
	env \
		PATH="$BIN:$PATH" \
		STUB_RECORD="$SCRATCH/record" \
		STUB_STATE="$1" \
		STUB_CHECK="$2" \
		DATABASE_URL='postgres://boxuser:boxpass@127.0.0.1:57726/finance_box?sslmode=disable' \
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
		sh deploy/finance/entrypoint.sh 2>&1 || true
}

# -- the URL never on a command line -------------------------------------------

out="$(run_box empty pass)"
record="$(cat "$SCRATCH/record" 2>/dev/null || true)"

case "$record" in
	*"postgres://"*) fail "the database URL reached a command line: $record" ;;
	*) pass "the database URL never reaches a command line" ;;
esac
case "$record" in
	*"boxpass"*)
		# the password may appear in the service file (that is where it
		# lives), but never in a command line
		if printf '%s\n' "$record" | grep '^psql .*boxpass' >/dev/null 2>&1; then
			fail "the database password reached a command line: $record"
		else
			pass "the database password never reaches a command line"
		fi
		;;
	*) pass "the database password never reaches a command line" ;;
esac
case "$record" in
	*"service_file=/dev/shm/finance-box-pgservice."*) pass "psql reads a service file from the shared memory tmpfs" ;;
	*) fail "psql was not pointed at a service file: $record" ;;
esac
service_path="$(printf '%s\n' "$record" | sed -n 's/^service_file=//p' | head -n 1)"
if [ -n "$service_path" ] && [ ! -e "$service_path" ]; then
	pass "the service file is removed when the probe is done"
else
	fail "the service file is left behind: ${service_path:-none}"
fi

# -- the shape drives the boot -------------------------------------------------

out="$(run_box empty pass)"
record="$(cat "$SCRATCH/record" 2>/dev/null || true)"
case "$record" in
	*"core finance-box-state"*) pass "the entrypoint asks the core for the database's shape" ;;
	*) fail "the entrypoint does not ask the core for the database's shape: $record" ;;
esac
case "$record" in
	*"core migrate"*) pass "an empty database is migrated" ;;
	*) fail "an empty database was not migrated: $record" ;;
esac
case "$record" in
	*"core finance-seed"*) pass "an empty database is seeded" ;;
	*) fail "an empty database was not seeded: $record" ;;
esac

# The bootstrap ownership marker: an empty database is marked the box's own
# BEFORE the first migrate, so the box's own interrupted first boot can be
# told from a database the box never planted. The mark is one psql call that
# sits between the shape question and the migrate, through the service file
# like the probe, and never on a command line.
case "$record" in
	*"CREATE SCHEMA IF NOT EXISTS finance_box_bootstrap"*)
		pass "an empty database is marked the box's own before the first migrate" ;;
	*) fail "an empty database was not marked with the bootstrap ownership mark: $record" ;;
esac
mark_line="$(printf '%s\n' "$record" | grep -n 'CREATE SCHEMA IF NOT EXISTS' | head -n 1 | cut -d: -f1)"
migrate_line="$(printf '%s\n' "$record" | grep -n 'core migrate' | head -n 1 | cut -d: -f1)"
if [ -n "$mark_line" ] && [ -n "$migrate_line" ] && [ "$mark_line" -lt "$migrate_line" ]; then
	pass "the bootstrap ownership mark is written before the migrate"
else
	fail "the bootstrap ownership mark is not written before the migrate (mark line ${mark_line:-none}, migrate line ${migrate_line:-none})"
fi

out="$(run_box unseeded pass)"
record="$(cat "$SCRATCH/record" 2>/dev/null || true)"
case "$record" in
	*"core finance-box-state"*) pass "the entrypoint asks the core for the database's shape" ;;
	*) fail "the entrypoint does not ask the core for the database's shape: $record" ;;
esac
case "$record" in
	*"core migrate"*) pass "an interrupted first boot is migrated" ;;
	*) fail "an interrupted first boot was not migrated: $record" ;;
esac
case "$record" in
	*"core finance-seed"*) pass "an interrupted first boot is seeded, so the boot heals" ;;
	*) fail "an interrupted first boot was not seeded, so the boot does not heal: $record" ;;
esac
case "$record" in
	*"core finance-box-check"*) fail "an interrupted first boot was refused by the box kind check: $record" ;;
	*) pass "an interrupted first boot does not run the box kind check" ;;
esac

out="$(run_box finance pass)"
record="$(cat "$SCRATCH/record" 2>/dev/null || true)"
case "$record" in
	*"core finance-box-check"*) pass "the finance box's own world runs the box kind check" ;;
	*) fail "the finance box's own world did not run the box kind check: $record" ;;
esac
case "$record" in
	*"core finance-seed"*) fail "the finance box's own world was seeded again: $record" ;;
	*) pass "the finance box's own world is not seeded again" ;;
esac

out="$(run_box foreign fail)"
record="$(cat "$SCRATCH/record" 2>/dev/null || true)"
case "$record" in
	*"core finance-box-check"*) pass "a foreign database runs the box kind check" ;;
	*) fail "a foreign database did not run the box kind check: $record" ;;
esac
case "$record" in
	*"core migrate"*) fail "a foreign database was migrated: $record" ;;
	*) pass "a foreign database is not migrated" ;;
esac
case "$record" in
	*"core finance-seed"*) fail "a foreign database was seeded: $record" ;;
	*) pass "a foreign database is not seeded" ;;
esac

if [ "$FAILURES" -ne 0 ]; then
	echo "$FAILURES failure(s)"
	exit 1
fi
echo "all passed"

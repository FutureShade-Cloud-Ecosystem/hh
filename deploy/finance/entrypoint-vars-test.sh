#!/bin/sh
# The finance entrypoint's variable checks, without Docker: every required
# name is a hard requirement, and the box statement values must be the exact
# required words. Run from a checkout:
#
#   sh deploy/finance/entrypoint-vars-test.sh
#
# Every leg refuses before any child process, any database call and any file
# write, so the script touches nothing but its own temp dirs. The values are
# made up; the vault key is the zero string in the pinned length, the
# endpoint is a non existent host, and the signing key file is never read
# (the checks refuse before that step).
set -eu
cd "$(git rev-parse --show-toplevel)"

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

# A throwaway signing key the value-check legs never reach; shape only.
mkdir -p "$SCRATCH/finance-root/app" "$SCRATCH/finance-root/templates"
printf '%s\n' '-----BEGIN EC PRIVATE KEY-----' 'throwaway-test-key-material' '-----END EC PRIVATE KEY-----' > "$SCRATCH/session.pem"
chmod 0600 "$SCRATCH/session.pem"
cp deploy/finance/Caddyfile.template "$SCRATCH/finance-root/templates/Caddyfile.template"

REQUIRED_NAMES='DATABASE_URL APP_ENV HH_BOX_KIND CORE_SIGNIN_MODE CORE_TOKEN_ISSUER CORE_STAFF_AUDIENCE CORE_TENANT_AUDIENCE CORE_SESSION_SIGNING_KEY_FILE CORE_APPWRITE_ENDPOINT CORE_APPWRITE_PROJECT CORE_APPWRITE_API_KEY CORE_SIGNIN_EMAIL_ADDRESSES PAYMENT_VAULT_KEY PORTAL_BASE_URL ERP_BASE_URL PORT HH_ACCEPTANCE_SIGN_KEY_ID HH_ACCEPTANCE_SIGN_KEY HH_WEB_MODE HH_WEB_ORG HH_WEB_CONSOLE_ENDPOINT HH_WEB_CONSOLE_PROJECT_ID HH_WEB_DEALER_NAME'

# run_box ENV_ASSIGNMENTS... -- sh deploy/finance/entrypoint.sh, with every
# required name set to a made up but shaped value unless overridden or
# dropped.
run_box() {
	drops=''
	sets=''
	while [ "$#" -gt 0 ]; do
		case "$1" in
			--) shift; break ;;
			-u) drops="$drops -u $2"; shift 2 ;;
			*) sets="$sets $1"; shift ;;
		esac
	done
	env \
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
		$sets \
		env $drops sh deploy/finance/entrypoint.sh 2>&1
}

# The missing-name legs: one required name dropped per run.
for name in $REQUIRED_NAMES; do
	if out="$(run_box -u "$name" --)"; then
		fail "without $name the entrypoint started"
	else
		pass "without $name the entrypoint exits non zero"
	fi
	case "$out" in
		*"$name"*"is not set"*) pass "the refusal names $name" ;;
		*) fail "the refusal does not name $name: $out" ;;
	esac
	case "$out" in
		*"migrate"*|*"listening"*|*"finance-seed"*) fail "the entrypoint went past the checks without $name: $out" ;;
		*) pass "the entrypoint stopped at the check for $name" ;;
	esac
done

# The box statement legs: a wrong word refuses naming the variable, never
# the value.
for leg in 'HH_BOX_KIND demo' 'APP_ENV production' 'CORE_SIGNIN_MODE demo'; do
	set -- $leg
	if out="$(run_box "$1=$2" --)"; then
		fail "with $1=$2 the entrypoint started"
	else
		pass "with $1=$2 the entrypoint exits non zero"
	fi
	case "$out" in
		*"$1"*) pass "the refusal names $1" ;;
		*) fail "the refusal does not name $1: $out" ;;
	esac
	case "$out" in
		*"migrate"*|*"listening"*|*"finance-seed"*) fail "the entrypoint went past the checks with $1=$2: $out" ;;
		*) pass "the entrypoint stopped at the check for $1" ;;
	esac
done

# The database name pair legs. The signing key file is pointed at a path
# that does not exist in every leg, so a run that gets past the pair check
# stops at the key step naming it: the pair verdict is readable from which
# name the refusal carries.
PAIR_KEY='CORE_SESSION_SIGNING_KEY_FILE=/nonexistent/session.pem'

# Both set and different: refused, naming the pair, never a value.
if out="$(run_box 'CORE_DATABASE_URL=postgres://other:other@127.0.0.1:2/other' "$PAIR_KEY" --)"; then
	fail "with the database names set to different targets the entrypoint started"
else
	pass "with the database names set to different targets the entrypoint exits non zero"
fi
case "$out" in
	*"DATABASE_URL"*"and"*"CORE_DATABASE_URL"*) pass "the refusal names the pair" ;;
	*) fail "the refusal does not name the pair: $out" ;;
esac
case "$out" in
	*"postgres://"*) fail "the refusal prints a value: $out" ;;
	*) pass "the refusal prints no value" ;;
esac
case "$out" in
	*"migrate"*|*"listening"*|*"finance-seed"*) fail "the entrypoint went past the checks with a differing pair: $out" ;;
	*) pass "the entrypoint stopped at the pair check" ;;
esac

# Only DATABASE_URL set: accepted. The run reaches the signing key step
# (its refusal names that file, not the pair).
if out="$(run_box "$PAIR_KEY" --)"; then
	fail "the acceptance leg ran to the end (the key refusal was expected): $out"
fi
case "$out" in
	*"different targets"*) fail "a differing pair was blamed where only DATABASE_URL was set: $out" ;;
	*) pass "only DATABASE_URL set is accepted at the pair check" ;;
esac
case "$out" in
	*"CORE_SESSION_SIGNING_KEY_FILE"*) pass "the run got past the pair check to the key step" ;;
	*) fail "the run did not reach the key step: $out" ;;
esac

# Both set and equal: accepted too.
if out="$(run_box 'CORE_DATABASE_URL=postgres://unused:unused@127.0.0.1:1/unused' "$PAIR_KEY" --)"; then
	fail "the equal pair leg ran to the end (the key refusal was expected): $out"
fi
case "$out" in
	*"different targets"*) fail "an equal pair was refused: $out" ;;
	*) pass "both database names set to the same target are accepted" ;;
esac

if [ "$FAILURES" -ne 0 ]; then
	echo "$FAILURES failure(s)"
	exit 1
fi
echo "all passed"

#!/bin/sh
# The finance entrypoint's finance user list: HH_FINANCE_USERS is the one
# source of truth for the people on the box, and the sign in allow list is
# derived from it rather than set beside it. Two sources that disagree refuse
# the boot, naming the variable and never the value. Run from a checkout:
#
#   sh deploy/finance/entrypoint-finance-users-test.sh
#
# The legs never reach a database: every refusal they prove happens in the
# entrypoint's variable checks, before any child process, any database call
# and any file write. The values are made up.
set -eu
cd "$(git rev-parse --show-toplevel)"

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

# The file the entrypoint sources for the derivation.
ALLOW_LIST_FILE="deploy/finance/signin-allow-list.sh"

CONTROLLER="fin-controller-a@example.invalid"
PAYABLES="fin-payables-b@example.invalid"
OTHER="fin-other-c@example.invalid"

# derive runs the entrypoint's derivation with the given environment and
# prints what it derived, or the refusal it made. The word unset drops the
# name from the environment instead of setting it empty. The list is read in
# a subshell, so a display name with a space travels whole.
derive() {
	(
		if [ "$1" = unset ]; then
			unset HH_FINANCE_USERS
		else
			HH_FINANCE_USERS="$1"
			export HH_FINANCE_USERS
		fi
		if [ "$2" = unset ]; then
			unset CORE_SIGNIN_EMAIL_ADDRESSES
		else
			CORE_SIGNIN_EMAIL_ADDRESSES="$2"
			export CORE_SIGNIN_EMAIL_ADDRESSES
		fi
		if [ ! -f "$ALLOW_LIST_FILE" ]; then
			echo "NO_DERIVATION"
			exit 0
		fi
		# shellcheck source=signin-allow-list.sh
		. "./$ALLOW_LIST_FILE"
		derive_signin_allow_list || exit 1
		printf 'derived=[%s]\n' "$CORE_SIGNIN_EMAIL_ADDRESSES"
	) 2>&1 || true
}

# The derived allow list leg: the list alone gives the allow list, with the
# addresses in the order they are written and folded to lower case.
out="$(derive "$CONTROLLER|fin-console-user-a|Financial Controller|financial_controller;$PAYABLES|fin-console-user-b|Payables and Receivables|payables_receivables" unset)"
case "$out" in
	"derived=[$CONTROLLER,$PAYABLES]") pass "the allow list is derived from the finance user list" ;;
	*) fail "the allow list is not derived from the finance user list: $out" ;;
esac

# The agreeing pair: both names set to the same list is not a disagreement.
out="$(derive "$CONTROLLER|fin-console-user-a|Financial Controller|financial_controller" "$CONTROLLER")"
case "$out" in
	"derived=[$CONTROLLER]") pass "an agreeing allow list is left alone" ;;
	*) fail "an agreeing allow list was refused or changed: $out" ;;
esac

# The disagreeing pair: the refusal names the variables and carries no value.
out="$(derive "$CONTROLLER|fin-console-user-a|Financial Controller|financial_controller" "$OTHER")"
case "$out" in
	*CORE_SIGNIN_EMAIL_ADDRESSES*) pass "the disagreement refusal names CORE_SIGNIN_EMAIL_ADDRESSES" ;;
	*) fail "the disagreement refusal does not name CORE_SIGNIN_EMAIL_ADDRESSES: $out" ;;
esac
case "$out" in
	*"$CONTROLLER"*|*"$OTHER"*) fail "the disagreement refusal carries a value: $out" ;;
	*) pass "the disagreement refusal carries no address" ;;
esac

# An entry of the wrong shape refuses, naming the variable and no value.
out="$(derive "$CONTROLLER|fin-console-user-a|Financial Controller" unset)"
case "$out" in
	*HH_FINANCE_USERS*) pass "the malformed entry refusal names HH_FINANCE_USERS" ;;
	*) fail "the malformed entry refusal does not name HH_FINANCE_USERS: $out" ;;
esac
case "$out" in
	*"$CONTROLLER"*) fail "the malformed entry refusal carries a value: $out" ;;
	*) pass "the malformed entry refusal carries no address" ;;
esac

# No list at all leaves the allow list alone: the entrypoint's required name
# check still asks for it.
out="$(derive unset "$CONTROLLER")"
case "$out" in
	"derived=[$CONTROLLER]") pass "no finance user list leaves the allow list alone" ;;
	*) fail "no finance user list changed the allow list: $out" ;;
esac

# The entrypoint legs: the same two rules, seen from the entrypoint itself.
mkdir -p "$SCRATCH/finance-root/app" "$SCRATCH/finance-root/templates"
printf '%s\n' '-----BEGIN EC PRIVATE KEY-----' 'throwaway-test-key-material' '-----END EC PRIVATE KEY-----' > "$SCRATCH/session.pem"
chmod 0600 "$SCRATCH/session.pem"
cp deploy/finance/Caddyfile.template "$SCRATCH/finance-root/templates/Caddyfile.template"

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
		env $drops sh deploy/finance/entrypoint.sh 2>&1 || true
}

# The list alone: the allow list is derived, so the entrypoint does not ask
# for it. APP_ENV is wrong on purpose, so the refusal that comes proves the
# entrypoint got past the required name checks without reaching the database.
out="$(run_box -u CORE_SIGNIN_EMAIL_ADDRESSES "HH_FINANCE_USERS=$CONTROLLER|fin-console-user-a|Controller|financial_controller" "APP_ENV=production" --)"
case "$out" in
	*"CORE_SIGNIN_EMAIL_ADDRESSES is not set"*) fail "the entrypoint still asks for the allow list: $out" ;;
	*) pass "the entrypoint does not ask for the allow list when the list derives it" ;;
esac
case "$out" in
	*"APP_ENV"*) pass "the entrypoint got past the required name checks with the list alone" ;;
	*) fail "the entrypoint stopped before the value checks: $out" ;;
esac

# The disagreeing pair: the entrypoint refuses naming the variable and never
# the value, before any database call.
out="$(run_box "HH_FINANCE_USERS=$CONTROLLER|fin-console-user-a|Controller|financial_controller" "CORE_SIGNIN_EMAIL_ADDRESSES=$OTHER" --)"
case "$out" in
	*"CORE_SIGNIN_EMAIL_ADDRESSES"*) pass "the entrypoint refusal names CORE_SIGNIN_EMAIL_ADDRESSES" ;;
	*) fail "the entrypoint refusal does not name CORE_SIGNIN_EMAIL_ADDRESSES: $out" ;;
esac
case "$out" in
	*"$CONTROLLER"*|*"$OTHER"*) fail "the entrypoint refusal carries a value: $out" ;;
	*) pass "the entrypoint refusal carries no address" ;;
esac
case "$out" in
	*"migrate"*|*"listening"*|*"finance-seed"*) fail "the entrypoint went past the checks: $out" ;;
	*) pass "the entrypoint stopped at the check" ;;
esac

if [ "$FAILURES" -ne 0 ]; then
	echo "$FAILURES failure(s)"
	exit 1
fi
echo "all passed"

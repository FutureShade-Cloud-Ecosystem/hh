#!/bin/sh
# The entrypoint's sign in mode requirement, without Docker: CORE_SIGNIN_MODE
# is a required name with no default. Run from a checkout:
#
#   ./deploy/demo/entrypoint-mode-test.sh
#
# Every other required name is set (with made up values, the gate value only
# bcrypt shaped), so the only thing the entrypoint can refuse is the mode.
# The refusal happens before any write, any child process and any database
# call, so the script touches nothing. The mode is passed empty rather than
# left out, because the entrypoint treats an empty value as unset and the
# caller's own environment is not ours to clear.
set -eu
cd "$(git rev-parse --show-toplevel)"

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

run_entrypoint() {
	DEMO_GATE_BCRYPT_HASH='$2a$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz01234' \
		DATABASE_URL='postgres://unused:unused@127.0.0.1:1/unused' \
		PAYMENT_VAULT_KEY=0000000000000000000000000000000000000000000000000000000000000000 \
		HH_ACCEPTANCE_SIGN_KEY_ID=mode-test \
		HH_ACCEPTANCE_SIGN_KEY=0000000000000000000000000000000000000000000000000000000000000000 \
		CORE_SIGNIN_MODE="$1" \
		sh deploy/demo/entrypoint.sh 2>&1
}

if out="$(run_entrypoint "")"; then
	fail "the entrypoint started without a sign in mode"
else
	pass "without the sign in mode the entrypoint exits non-zero"
fi
case "$out" in
	*CORE_SIGNIN_MODE*"is not set"*) pass "the refusal names CORE_SIGNIN_MODE" ;;
	*) fail "the refusal does not name CORE_SIGNIN_MODE: $out" ;;
esac
case "$out" in
	*"running"*|*"listening"*|*migrate*) fail "the entrypoint went past the check: $out" ;;
	*) pass "the entrypoint stopped at the check" ;;
esac

if [ "$FAILURES" -ne 0 ]; then
	echo "$FAILURES failure(s)"
	exit 1
fi
echo "all passed"

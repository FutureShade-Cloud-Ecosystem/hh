#!/bin/sh
# CORE_INTERNAL_PORT is read by the entrypoint and by nothing in the core, so
# the core must not inherit it: the demo environment guard refuses a CORE_
# name on no list, and this operator variable is documented in the README.
# The test runs the entrypoint's marked port block alone, without Docker, and
# asserts a child process sees no CORE_INTERNAL_PORT while the shell still
# holds the value for the health wait and the proxy. Run from a checkout:
#
#   ./deploy/demo/entrypoint-port-test.sh
set -eu
cd "$(git rev-parse --show-toplevel)"

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

BLOCK="$(sed -n '/^# BEGIN internal port$/,/^# END internal port$/p' deploy/demo/entrypoint.sh)"
[ -n "$BLOCK" ] || { echo "FAIL: the marked port block is missing from the entrypoint"; exit 1; }

for set_value in "" 9191; do
	if [ -n "$set_value" ]; then
		child="$(CORE_INTERNAL_PORT="$set_value" sh -c "$BLOCK"'
			env | grep -c "^CORE_INTERNAL_PORT=" || true' )"
		held="$(CORE_INTERNAL_PORT="$set_value" sh -c "$BLOCK"'
			echo "$CORE_INTERNAL_PORT $UPSTREAM"')"
		want="$set_value 127.0.0.1:$set_value"
	else
		child="$(env -u CORE_INTERNAL_PORT sh -c "$BLOCK"'
			env | grep -c "^CORE_INTERNAL_PORT=" || true')"
		held="$(env -u CORE_INTERNAL_PORT sh -c "$BLOCK"'
			echo "$CORE_INTERNAL_PORT $UPSTREAM"')"
		want="9090 127.0.0.1:9090"
	fi
	if [ "$child" = "0" ]; then
		pass "a child process inherits no CORE_INTERNAL_PORT (set to '${set_value:-unset}')"
	else
		fail "a child process inherits CORE_INTERNAL_PORT (set to '${set_value:-unset}')"
	fi
	if [ "$held" = "$want" ]; then
		pass "the shell keeps the port and the upstream ('$held')"
	else
		fail "the shell lost the port or the upstream: '$held', want '$want'"
	fi
done

if [ "$FAILURES" -ne 0 ]; then
	echo "$FAILURES failure(s)"
	exit 1
fi
echo "all passed"

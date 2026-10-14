#!/bin/sh
# The demo box names its dealer: the entrypoint defaults DEALER_NAME to the demo
# dealer when the operator leaves it unset, and the demo environment allow list
# carries the name. Static, touches nothing. Run from a checkout:
#
#   ./deploy/demo/entrypoint-dealer-test.sh
set -eu
cd "$(git rev-parse --show-toplevel)"

FAILURES=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }

if grep -q '^export DEALER_NAME="${DEALER_NAME:-Gable Landscape Supply}"$' deploy/demo/entrypoint.sh; then
	pass "the entrypoint defaults DEALER_NAME to the demo dealer"
else
	fail "the entrypoint does not default DEALER_NAME to Gable Landscape Supply"
fi
if grep -q '"DEALER_NAME"' core/cmd/core/demoenv_inventory.go; then
	pass "the demo env allow list names DEALER_NAME"
else
	fail "the demo env allow list lacks DEALER_NAME"
fi

if [ "$FAILURES" -ne 0 ]; then
	echo "$FAILURES failure(s)"
	exit 1
fi
echo "all passed"

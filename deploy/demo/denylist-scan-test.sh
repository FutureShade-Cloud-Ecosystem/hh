#!/bin/sh
# The denylist scan's own harness: on a clean bundle directory the scan
# passes with zero matches, and on a planted first-dealer word it fails
# and counts exactly one match, proving the scan bites. Database legs
# are exercised by the image exit test, which drives live containers.

set -eu

cd "$(git rev-parse --show-toplevel)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/app"

step() { echo "== $*"; }

step "a clean bundle directory scans clean"
out="$(./deploy/demo/denylist-scan.sh --bundle-dir "$tmp/app")" \
	|| { echo "FAIL: the clean directory did not scan clean"; echo "$out"; exit 1; }
echo "$out" | grep -q '^bundle_matches=0$' \
	|| { echo "FAIL: the clean directory did not report bundle_matches=0"; echo "$out"; exit 1; }
echo "$out" | grep -Eq '^denylist_sha256=[0-9a-f]{64}$' \
	|| { echo "FAIL: no denylist_sha256 was printed"; echo "$out"; exit 1; }
echo "PASS: $out" | tr '\n' ' '; echo

step "a planted first-dealer word bites"
printf 'Such as This month, Trenton\n' > "$tmp/app/planted.js"
if out2="$(./deploy/demo/denylist-scan.sh --bundle-dir "$tmp/app" 2>&1)"; then
	echo "FAIL: the scan passed a planted word"
	echo "$out2"
	exit 1
fi
printf '%s\n' "$out2" | grep -q '^bundle_matches=1$' \
	|| { echo "FAIL: the planted word did not count exactly one match"; echo "$out2"; exit 1; }
printf '%s\n' "$out2" | grep -q 'planted.js' \
	|| { echo "FAIL: the offending file was not named"; echo "$out2"; exit 1; }
echo "PASS: the scan bites on a planted word and names the file"

step "denylist-scan-test: pass"

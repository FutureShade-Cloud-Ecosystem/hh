#!/bin/sh
# The Caddyfile leg for the session JWK Set: GET /.well-known/jwks.json must
# reach the core through the demo image's Caddy, with no phrase and with the
# forwarded address overwritten, and must not fall to the front door's
# catch all (which asks for the phrase and serves a page). The leg runs the
# image's own template under the same Caddy release the Dockerfile copies,
# with a second Caddy inside the same container standing in for the core, so
# it needs no image build and no database.
#
# Usage: deploy/demo/caddy-jwks-test.sh
# Needs docker and the caddy image of the Dockerfile's release. One
# container, named below, removed on every exit path.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
CADDY_IMAGE="${CADDY_IMAGE:-caddy:2.10.2}"
NAME="${CADDY_TEST_NAME:-s4-auth-exchange-caddy-jwks}"
fails=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; fails=$((fails + 1)); }

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# A bcrypt hash of a throwaway phrase made here; it protects nothing.
HASH="$(printf 'throwaway\n' | docker run --rm -i "$CADDY_IMAGE" caddy hash-password)"

docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" --network none -v "$HERE":/t:ro -e HASH="$HASH" "$CADDY_IMAGE" sh -c '
	export XDG_CONFIG_HOME=/dev/shm/ct XDG_DATA_HOME=/dev/shm/ctd
	sed -e "s|__GATE_PORT__|8080|g" \
	    -e "s|__UPSTREAM__|127.0.0.1:9999|g" \
	    -e "s|__GATE_HASH__|$HASH|g" /t/Caddyfile.template > /tmp/cf
	# The stand in core: echoes what the proxy sent so the leg can read it.
	cat > /tmp/up <<STAND
{
	admin off
	auto_https off
}
:9999 {
	respond /.well-known/jwks.json "{\"keys\":[],\"seen_xff\":\"{header.X-Forwarded-For}\"}" 200
	respond "core fell through" 404
}
STAND
	caddy start --config /tmp/up --adapter caddyfile >/dev/null 2>&1 || exit 1
	exec caddy run --config /tmp/cf --adapter caddyfile
' >/dev/null

probes=0
ready=0
while [ "$probes" -lt 30 ]; do
	if docker exec "$NAME" sh -c 'wget -S -q -O /dev/null http://127.0.0.1:8080/ 2>&1' | grep -q " 401 "; then
		ready=1
		break
	fi
	probes=$((probes + 1))
	sleep 1
done
[ "$ready" = "1" ] || { echo "FAIL: Caddy never came up" >&2; docker logs "$NAME" 2>&1 | tail -5 >&2; exit 1; }

body="$(docker exec "$NAME" sh -c 'wget -q -O - --header="X-Forwarded-For: 9.9.9.9" http://127.0.0.1:8080/.well-known/jwks.json 2>/dev/null' || true)"
case "$body" in
	*'"keys":[]'*) pass "the JWK Set path reaches the core through Caddy with no phrase" ;;
	*) fail "the JWK Set path did not reach the core: $body" ;;
esac
case "$body" in
	*'"seen_xff":"9.9.9.9"'*) fail "a client written X-Forwarded-For survived the proxy" ;;
	*'"seen_xff":"127.0.0.1"'*) pass "the proxy replaced X-Forwarded-For with the address it saw" ;;
	*) fail "the forwarded address is not the proxy's own observation: $body" ;;
esac

# The catch all is unchanged: the front door still asks for the phrase.
if docker exec "$NAME" sh -c 'wget -S -q -O /dev/null http://127.0.0.1:8080/some/page 2>&1' | grep -q " 401 "; then
	pass "the catch all still asks for the phrase"
else
	fail "the catch all no longer asks for the phrase"
fi

[ "$fails" = "0" ] || exit 1
echo "caddy jwks test: all legs passed"

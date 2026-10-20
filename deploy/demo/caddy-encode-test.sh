#!/bin/sh
# The Caddyfile leg for response compression (UX-52a): requests to an app page
# and a JS bundle asset with Accept-Encoding: gzip (or zstd) must answer with
# Content-Encoding: gzip (or zstd) and fewer bytes than the plain file, while
# requests without Accept-Encoding answer plain. API routes answer without
# Content-Encoding even when Accept-Encoding is sent (SSE streams must not be
# buffered and token bearing responses must not be compressed). The phrase gate
# still answers 401 without the phrase, the JWK Set still answers with no
# phrase, and the forwarded address overwrite is preserved.
#
# Usage: deploy/demo/caddy-encode-test.sh
# Needs docker and the caddy image of the Dockerfile's release. One container,
# named below, removed on every exit path.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
CADDY_IMAGE="${CADDY_IMAGE:-caddy:2.10.2}"
NAME="${CADDY_TEST_NAME:-s4-ux-52a-caddy-encode}"
TMP_DIR="/tmp/${NAME}-fixture"
fails=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; fails=$((fails + 1)); }

cleanup() {
	docker rm -f "$NAME" >/dev/null 2>&1 || true
	rm -rf "$TMP_DIR"
}
trap cleanup EXIT

rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR/srv/app/till/assets"

# Populate /srv/app/till from a built demo image when available on the host so
# the script measures the real built till bundle without building the image;
# fall back to the checkout's counter-till sources when no built image is
# present on the machine.
ASSET_SOURCE="source-fallback"
if [ "${DEMO_IMAGE:-}" != "none" ]; then
	for candidate in "${DEMO_IMAGE:-}" "registry.digitalocean.com/futurebuild/hh-demo-till:b73d385cf01a" "hh-demo-till:goal-key"; do
		[ -n "$candidate" ] || continue
		if docker image inspect "$candidate" >/dev/null 2>&1; then
			if docker run --rm --entrypoint tar "$candidate" -C /srv/app -cf - index.html till 2>/dev/null | tar -C "$TMP_DIR/srv/app" -xf - 2>/dev/null; then
				if ls "$TMP_DIR/srv/app/till/assets/"*.js >/dev/null 2>&1; then
					ASSET_SOURCE="image:$candidate"
					break
				fi
			fi
		fi
	done
fi

if [ "$ASSET_SOURCE" = "source-fallback" ]; then
	cp "$ROOT/web/apps/counter-till/index.html" "$TMP_DIR/srv/app/till/index.html"
	cp "$ROOT/web/apps/front-door/index.html" "$TMP_DIR/srv/app/index.html"
	find "$ROOT/web/apps/counter-till/src" "$ROOT/web/packages" -name '*.ts' -type f -print0 \
		| xargs -0 cat > "$TMP_DIR/srv/app/till/assets/app-root-fallback.js"
fi

LARGEST_JS_PATH="$(ls -S "$TMP_DIR/srv/app/till/assets/"*.js | head -n 1)"
LARGEST_JS_NAME="$(basename "$LARGEST_JS_PATH")"
DISK_JS_BYTES="$(wc -c < "$LARGEST_JS_PATH" | tr -d ' ')"

WASM_NAME=""
if ls "$TMP_DIR/srv/app/till/assets/"*.wasm >/dev/null 2>&1; then
	WASM_PATH="$(ls -S "$TMP_DIR/srv/app/till/assets/"*.wasm | head -n 1)"
	WASM_NAME="$(basename "$WASM_PATH")"
fi

# A bcrypt hash of a throwaway phrase made here; it protects nothing.
HASH="$(printf 'throwaway\n' | docker run --rm -i "$CADDY_IMAGE" caddy hash-password)"
AUTH="$(printf 'demo:throwaway' | base64 | tr -d '\n')"

docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" --network none \
	-v "$HERE":/t:ro \
	-v "$TMP_DIR/srv/app":/srv/app:ro \
	-e HASH="$HASH" "$CADDY_IMAGE" sh -c '
	export XDG_CONFIG_HOME=/dev/shm/ct XDG_DATA_HOME=/dev/shm/ctd
	sed -e "s|__GATE_PORT__|8080|g" \
	    -e "s|__UPSTREAM__|127.0.0.1:9999|g" \
	    -e "s|__GATE_HASH__|$HASH|g" /t/Caddyfile.template > /tmp/cf
	API_PAD="$(head -c 1024 /dev/zero | tr "\0" "x")"
	cat > /tmp/up <<STAND
{
	admin off
	auto_https off
}
:9999 {
	header /api/* Content-Type "application/json; charset=utf-8"
	respond /.well-known/jwks.json "{\"keys\":[],\"seen_xff\":\"{header.X-Forwarded-For}\"}" 200
	respond /api/v1/demo/mint "{\"token\":\"$API_PAD\",\"seen_xff\":\"{header.X-Forwarded-For}\"}" 200
	respond /api/v1/catalog/items "{\"items\":[\"$API_PAD\"],\"seen_xff\":\"{header.X-Forwarded-For}\"}" 200
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

# 1. App page (/till/ and /) with Accept-Encoding: gzip answers Content-Encoding: gzip.
page_gzip_hdr="$(docker exec "$NAME" sh -c "wget -S -q -O /dev/null --header='Authorization: Basic $AUTH' --header='Accept-Encoding: gzip' http://127.0.0.1:8080/till/ 2>&1" || true)"
if printf '%s\n' "$page_gzip_hdr" | grep -qi 'Content-Encoding:[[:space:]]*gzip'; then
	pass "app page (/till/) with Accept-Encoding: gzip answers Content-Encoding: gzip"
else
	fail "app page (/till/) with Accept-Encoding: gzip did not answer Content-Encoding: gzip"
fi

root_gzip_hdr="$(docker exec "$NAME" sh -c "wget -S -q -O /dev/null --header='Authorization: Basic $AUTH' --header='Accept-Encoding: gzip' http://127.0.0.1:8080/ 2>&1" || true)"
if printf '%s\n' "$root_gzip_hdr" | grep -qi 'Content-Encoding:[[:space:]]*gzip'; then
	pass "front door catch-all (/) with Accept-Encoding: gzip answers Content-Encoding: gzip"
else
	fail "front door catch-all (/) with Accept-Encoding: gzip did not answer Content-Encoding: gzip"
fi

# 2. App page (/till/) without Accept-Encoding answers plain (no Content-Encoding).
page_plain_hdr="$(docker exec "$NAME" sh -c "wget -S -q -O /dev/null --header='Authorization: Basic $AUTH' http://127.0.0.1:8080/till/ 2>&1" || true)"
if printf '%s\n' "$page_plain_hdr" | grep -qi 'Content-Encoding:'; then
	fail "app page (/till/) without Accept-Encoding answered with a Content-Encoding header"
else
	pass "app page (/till/) without Accept-Encoding answers plain"
fi

# 3. Till largest JS asset with Accept-Encoding: gzip and zstd answers compressed, and without answers plain.
js_url="http://127.0.0.1:8080/till/assets/$LARGEST_JS_NAME"
js_gzip_hdr="$(docker exec "$NAME" sh -c "wget -S -q -O /dev/null --header='Authorization: Basic $AUTH' --header='Accept-Encoding: gzip' '$js_url' 2>&1" || true)"
if printf '%s\n' "$js_gzip_hdr" | grep -qi 'Content-Encoding:[[:space:]]*gzip'; then
	pass "till largest JS asset ($LARGEST_JS_NAME) with Accept-Encoding: gzip answers Content-Encoding: gzip"
else
	fail "till largest JS asset ($LARGEST_JS_NAME) with Accept-Encoding: gzip did not answer Content-Encoding: gzip"
fi

js_zstd_hdr="$(docker exec "$NAME" sh -c "wget -S -q -O /dev/null --header='Authorization: Basic $AUTH' --header='Accept-Encoding: zstd, gzip' '$js_url' 2>&1" || true)"
if printf '%s\n' "$js_zstd_hdr" | grep -qi 'Content-Encoding:[[:space:]]*zstd'; then
	pass "till largest JS asset ($LARGEST_JS_NAME) with Accept-Encoding: zstd, gzip prefers Content-Encoding: zstd"
else
	fail "till largest JS asset ($LARGEST_JS_NAME) with Accept-Encoding: zstd, gzip did not answer Content-Encoding: zstd"
fi

js_plain_hdr="$(docker exec "$NAME" sh -c "wget -S -q -O /dev/null --header='Authorization: Basic $AUTH' '$js_url' 2>&1" || true)"
if printf '%s\n' "$js_plain_hdr" | grep -qi 'Content-Encoding:'; then
	fail "till largest JS asset ($LARGEST_JS_NAME) without Accept-Encoding answered with a Content-Encoding header"
else
	pass "till largest JS asset ($LARGEST_JS_NAME) without Accept-Encoding answers plain"
fi

js_plain_bytes="$(docker exec "$NAME" sh -c "wget -q -O - --header='Authorization: Basic $AUTH' '$js_url' | wc -c" | tr -d ' ')"
js_gzip_bytes="$(docker exec "$NAME" sh -c "wget -q -O - --header='Authorization: Basic $AUTH' --header='Accept-Encoding: gzip' '$js_url' | wc -c" | tr -d ' ')"
js_zstd_bytes="$(docker exec "$NAME" sh -c "wget -q -O - --header='Authorization: Basic $AUTH' --header='Accept-Encoding: zstd, gzip' '$js_url' | wc -c" | tr -d ' ')"

echo "MEASURE: source=$ASSET_SOURCE asset=$LARGEST_JS_NAME plain_bytes=$js_plain_bytes gzip_bytes=$js_gzip_bytes zstd_bytes=$js_zstd_bytes"

if [ "$js_plain_bytes" = "$DISK_JS_BYTES" ] && [ "$js_gzip_bytes" -lt "$js_plain_bytes" ] && [ "$js_zstd_bytes" -lt "$js_plain_bytes" ]; then
	pass "till largest JS asset shrinks under compression (plain $js_plain_bytes B -> gzip $js_gzip_bytes B, zstd $js_zstd_bytes B)"
else
	fail "till largest JS asset did not shrink under compression (disk $DISK_JS_BYTES B, plain $js_plain_bytes B, gzip $js_gzip_bytes B, zstd $js_zstd_bytes B)"
fi

if [ -n "$WASM_NAME" ]; then
	wasm_url="http://127.0.0.1:8080/till/assets/$WASM_NAME"
	wasm_plain_bytes="$(docker exec "$NAME" sh -c "wget -q -O - --header='Authorization: Basic $AUTH' '$wasm_url' | wc -c" | tr -d ' ')"
	wasm_gzip_bytes="$(docker exec "$NAME" sh -c "wget -q -O - --header='Authorization: Basic $AUTH' --header='Accept-Encoding: gzip' '$wasm_url' | wc -c" | tr -d ' ')"
	wasm_zstd_bytes="$(docker exec "$NAME" sh -c "wget -q -O - --header='Authorization: Basic $AUTH' --header='Accept-Encoding: zstd, gzip' '$wasm_url' | wc -c" | tr -d ' ')"
	echo "MEASURE: source=$ASSET_SOURCE asset=$WASM_NAME plain_bytes=$wasm_plain_bytes gzip_bytes=$wasm_gzip_bytes zstd_bytes=$wasm_zstd_bytes"
fi

# 4. API handles (/api/* and /api/v1/demo/*) answer with NO Content-Encoding even when Accept-Encoding is sent, and keep X-Forwarded-For overwrite.
api_gzip_hdr="$(docker exec "$NAME" sh -c 'wget -S -q -O /dev/null --header="Accept-Encoding: gzip" --header="X-Forwarded-For: 9.9.9.9" http://127.0.0.1:8080/api/v1/catalog/items 2>&1' || true)"
if printf '%s\n' "$api_gzip_hdr" | grep -qi 'Content-Encoding:'; then
	fail "API route (/api/v1/catalog/items) with Accept-Encoding: gzip answered with a Content-Encoding header"
else
	pass "API route (/api/v1/catalog/items) with Accept-Encoding: gzip answers with no Content-Encoding"
fi

api_body="$(docker exec "$NAME" sh -c 'wget -q -O - --header="Accept-Encoding: gzip" --header="X-Forwarded-For: 9.9.9.9" http://127.0.0.1:8080/api/v1/catalog/items 2>/dev/null' || true)"
case "$api_body" in
	*'"seen_xff":"127.0.0.1"'*) pass "API route preserves the X-Forwarded-For overwrite" ;;
	*) fail "API route did not preserve X-Forwarded-For overwrite: $api_body" ;;
esac

mint_gzip_hdr="$(docker exec "$NAME" sh -c "wget -S -q -O /dev/null --header='Authorization: Basic $AUTH' --header='Accept-Encoding: gzip' --header='X-Forwarded-For: 9.9.9.9' http://127.0.0.1:8080/api/v1/demo/mint 2>&1" || true)"
if printf '%s\n' "$mint_gzip_hdr" | grep -qi 'Content-Encoding:'; then
	fail "demo mint route (/api/v1/demo/mint) with phrase and Accept-Encoding: gzip answered with a Content-Encoding header"
else
	pass "demo mint route (/api/v1/demo/mint) with phrase and Accept-Encoding: gzip answers with no Content-Encoding"
fi

# 5. The phrase gate still answers 401 without the phrase, and JWK Set still answers with no phrase.
if docker exec "$NAME" sh -c 'wget -S -q -O /dev/null http://127.0.0.1:8080/till/ 2>&1' | grep -q " 401 "; then
	pass "the phrase gate still answers 401 on /till/ without the phrase"
else
	fail "the phrase gate did not answer 401 on /till/ without the phrase"
fi

if docker exec "$NAME" sh -c 'wget -S -q -O /dev/null http://127.0.0.1:8080/api/v1/demo/mint 2>&1' | grep -q " 401 "; then
	pass "the phrase gate still answers 401 on /api/v1/demo/mint without the phrase"
else
	fail "the phrase gate did not answer 401 on /api/v1/demo/mint without the phrase"
fi

if docker exec "$NAME" sh -c 'wget -S -q -O /dev/null http://127.0.0.1:8080/some/page 2>&1' | grep -q " 401 "; then
	pass "the catch all still answers 401 without the phrase"
else
	fail "the catch all did not answer 401 without the phrase"
fi

jwks_body="$(docker exec "$NAME" sh -c 'wget -q -O - --header="X-Forwarded-For: 9.9.9.9" http://127.0.0.1:8080/.well-known/jwks.json 2>/dev/null' || true)"
case "$jwks_body" in
	*'"keys":[]'*) pass "the JWK Set still answers with no phrase" ;;
	*) fail "the JWK Set did not answer: $jwks_body" ;;
esac
case "$jwks_body" in
	*'"seen_xff":"127.0.0.1"'*) pass "the JWK Set still replaces X-Forwarded-For with the proxy's own observation" ;;
	*) fail "the JWK Set forwarded address is wrong: $jwks_body" ;;
esac

# 6. Every file handle in Caddyfile.template carries encode zstd gzip (17 handles; API handles do not).
encode_count="$(grep -c '^[[:space:]]*encode zstd gzip$' "$HERE/Caddyfile.template" || true)"
if [ "$encode_count" = "17" ]; then
	pass "all 17 file handles in Caddyfile.template carry encode zstd gzip (API handles excluded)"
else
	fail "Caddyfile.template carries $encode_count encode zstd gzip directives, want 17"
fi

[ "$fails" = "0" ] || exit 1
echo "caddy encode test: all legs passed"

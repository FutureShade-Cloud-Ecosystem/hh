#!/bin/sh
# image-files-test.sh: every file the finance entrypoint sources from its own
# directory must reach that directory in the image. The entrypoint is copied
# to /usr/local/bin/entrypoint and sources its helpers with
# . "$(dirname "$0")/<name>", so each <name> needs a COPY to
# /usr/local/bin/<name> in the runtime stage. The other entrypoint tests run
# from the source tree, where the helpers sit beside the script, so they
# cannot see a helper the image leaves out; this one reads the Dockerfile.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
entrypoint="$here/entrypoint.sh"
dockerfile="$here/Dockerfile"
failures=0

grep -q 'COPY deploy/finance/entrypoint.sh /usr/local/bin/entrypoint$' "$dockerfile" || {
	echo "FAIL: the Dockerfile does not copy the entrypoint to /usr/local/bin/entrypoint"
	failures=$((failures + 1))
}

helpers="$(sed -n 's|^[[:space:]]*\. "\$(dirname "\$0")/\([A-Za-z0-9._-]*\)".*|\1|p' "$entrypoint")"
[ -n "$helpers" ] || {
	echo "FAIL: found no helper the entrypoint sources; the pattern this test reads has drifted"
	failures=$((failures + 1))
}
for name in $helpers; do
	if [ ! -f "$here/$name" ]; then
		echo "FAIL: the entrypoint sources $name, which is not in deploy/finance/"
		failures=$((failures + 1))
	elif grep -q "^COPY deploy/finance/$name /usr/local/bin/$name\$" "$dockerfile"; then
		echo "PASS: $name is copied beside the entrypoint"
	else
		echo "FAIL: the entrypoint sources $name, and the Dockerfile never copies it to /usr/local/bin/$name"
		failures=$((failures + 1))
	fi
done

# The bootstrap ownership mark is written by the entrypoint and read by the
# core's classifier, and the word must be the same word in both: a drifted
# word would mark databases the classifier never answers for. Each side is
# read from its own source, and the two must match.
mark_shell="$(sed -n 's/.*CREATE SCHEMA IF NOT EXISTS \([a-z_]*\).*/\1/p' "$entrypoint" | head -n 1)"
boxkind="$here/../../core/cmd/core/boxkind.go"
mark_go="$(sed -n 's/^	financeBoxBootstrapSchema = "\([a-z_]*\)"$/\1/p' "$boxkind" | head -n 1)"
if [ -n "$mark_shell" ] && [ "$mark_shell" = "$mark_go" ]; then
	echo "PASS: the bootstrap ownership mark is the same word in the entrypoint and the classifier"
else
	echo "FAIL: the bootstrap ownership mark drifted (entrypoint: ${mark_shell:-none}, classifier: ${mark_go:-none})"
	failures=$((failures + 1))
fi

# The world layer is the finance image plus the one world dump the box resets
# to: Dockerfile.world copies a single *.sql.gz into /srv/world/, where the
# reset reads it with no store and no token. The repository holds no world
# data; the principal builds this layer from a scratch context that carries
# exactly one dump file, never committed. The layer names the image it rides
# on, and the COPY lands inside /srv/world/ with the .sql.gz suffix the
# reset scans for.
world_dockerfile="$here/Dockerfile.world"
if [ ! -f "$world_dockerfile" ]; then
	echo "FAIL: Dockerfile.world is missing; the world layer has no definition"
	failures=$((failures + 1))
else
	from_line="$(grep -c '^FROM ' "$world_dockerfile")"
	copy_line="$(sed -n 's|^\(COPY .*\) /srv/world/\([A-Za-z0-9._-]*\.sql\.gz\)$|\1|p' "$world_dockerfile")"
	if [ "$from_line" -eq 1 ] && [ -n "$copy_line" ]; then
		echo "PASS: the world layer rides on one image and lands one dump in /srv/world/"
	else
		echo "FAIL: Dockerfile.world must have exactly one FROM and one COPY of a .sql.gz into /srv/world/"
		failures=$((failures + 1))
	fi
fi

if [ "$failures" -ne 0 ]; then
	echo "$failures failure(s)"
	exit 1
fi
echo "all passed"

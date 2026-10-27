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

if [ "$failures" -ne 0 ]; then
	echo "$failures failure(s)"
	exit 1
fi
echo "all passed"

#!/bin/sh
# Build script for the multi-app demo image. Detects whether quote-desk is
# present and passes the appropriate build flag to the Dockerfile.
#
# Usage:
#   ./deploy/demo/build-image.sh            # auto-detect, builds from worktree root
#   ./deploy/demo/build-image.sh --force-quote-desk   # build with quote-desk even if absent
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# The worktree root is two levels above the deploy/demo/ directory.
WT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
IMAGE_TAG="${HH_DEMO_IMAGE_TAG:-hh-lane0-demo:local}"

# Check if quote-desk source is present.
HAS_QUOTE_DESK=0
if [ -d "$WT_ROOT/web/apps/quote-desk/src" ]; then
	HAS_QUOTE_DESK=1
fi

# Parse flags.
FORCE_QD=0
for arg in "$@"; do
	case "$arg" in
		--force-quote-desk) FORCE_QD=1 ;;
	esac
done

if [ "$FORCE_QD" = "1" ]; then
	HAS_QUOTE_DESK=1
fi

if [ "$HAS_QUOTE_DESK" = "1" ]; then
	echo "building with quote-desk"
	QD_ARG="--build-arg BUILD_QUOTE_DESK=1"
else
	echo "building without quote-desk (web/apps/quote-desk not on main yet)"
	QD_ARG=""
fi

# Build from the worktree root as the explicit context so every COPY
# instruction resolves correctly regardless of where this script is called from.
# shellcheck disable=SC2086
exec docker build \
	--file "$WT_ROOT/deploy/demo/Dockerfile" \
	--tag "$IMAGE_TAG" \
	$QD_ARG \
	"$WT_ROOT"

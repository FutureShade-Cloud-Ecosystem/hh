#!/usr/bin/env bash
# prepare.sh <dir>: build, under <dir> (made if absent, never inside the repo), everything the
# evening guard needs to move the Go wall clock:
#
#   <dir>/toolchain   a copy of the ACTIVE toolchain's GOROOT. A runner's active toolchain can
#                     live inside the module cache (GOTOOLCHAIN auto downloads it there when the
#                     installed go is not the one the tree names), and go refuses overlays that
#                     replace files beneath GOMODCACHE, so the overlay must point at a GOROOT
#                     outside every cache. The copy is the same toolchain go itself resolved for
#                     this tree; only its location changes.
#   <dir>/go          a wrapper that runs the copy's go with GOTOOLCHAIN=local, so no guard
#                     command can switch toolchains mid step. The copy is exactly the resolution
#                     of this tree's go line, so the pin holds on any runner and through a
#                     toolchain bump.
#   <dir>/overlay.json  a go build overlay that makes time.Now() add a fixed shift to the wall clock
#
# Why an overlay: a Go binary reads the clock through the vDSO and never through libc, so libfaketime
# (LD_PRELOAD) cannot move it. The overlay patches the standard library's time.Now only: monotonic
# readings, timers and deadlines are untouched, so nothing waits longer or shorter. The patch fails
# loudly if the toolchain's time.go no longer has the line it anchors on.
#
# The shift itself (HH_CLOCK_SHIFT_SECONDS, read by the overlay at each binary's start) comes from
# shift.sh: "evening" for a run with no database, the database URL for a run whose Postgres is
# pinned by libfaketime, so both halves of that run hold one clock.
#
# Usage in a step: <dir>/go test ... with GOFLAGS="-overlay=<dir>/overlay.json" and
# HH_CLOCK_SHIFT_SECONDS set. Module dependencies still come from the real module cache; only the
# toolchain is the copy.
set -euo pipefail
dir="${1:?usage: prepare.sh <dir>}"
mkdir -p "$dir"
goroot="$(go env GOROOT)"
src="$goroot/src/time/time.go"
anchor='sec, nsec, mono := runtimeNow()'
grep -qF "$anchor" "$src" || { echo "prepare.sh: the toolchain's time.Now no longer reads '$anchor'; update this script" >&2; exit 1; }
rm -rf "$dir/toolchain"
cp -a "$goroot" "$dir/toolchain"
copy="$dir/toolchain"
sed "s|$anchor|$anchor\n\tsec += hhClockShift|" "$copy/src/time/time.go" > "$dir/time.go"
cat > "$dir/hh_clockshift.go" <<'G'
package time

import "syscall"

// hhClockShift is whole seconds added to the wall clock (never the monotonic clock) of every
// time.Now, read once from HH_CLOCK_SHIFT_SECONDS; zero when unset or unreadable.
var hhClockShift = func() int64 {
	v, ok := syscall.Getenv("HH_CLOCK_SHIFT_SECONDS")
	if !ok {
		return 0
	}
	var n int64
	neg := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		if i == 0 && (c == '-' || c == '+') {
			neg = c == '-'
			continue
		}
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n
}()
G
cat > "$dir/go" <<W
#!/bin/sh
# The evening guard's go: the toolchain snapshot under this directory, pinned with GOTOOLCHAIN=local
# so the guard's commands cannot switch away from it mid step.
GOTOOLCHAIN=local "$copy/bin/go" "\$@"
W
chmod +x "$dir/go"
cat > "$dir/overlay.json" <<J
{"Replace": {"$copy/src/time/time.go": "$dir/time.go", "$copy/src/time/hh_clockshift.go": "$dir/hh_clockshift.go"}}
J

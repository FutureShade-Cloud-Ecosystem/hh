#!/usr/bin/env bash
# deploy/finance/world/run.sh
# Runs the 12-month synthetic finance world (days 0..312) day by day as fresh
# processes under the evening-guard clock overlay and a libfaketime Postgres,
# verifies all ties at the end, and dumps the database with its sha256.

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SEED="${FINWORLD_SEED:-1}"
ANCHOR="${FINWORLD_ANCHOR:-2026-09}"
DEALER_NAME="${HH_WEB_DEALER_NAME:-Quarryfen Hardscape Supply}"
GOCACHE_DIR="${FINWORLD_GOCACHE:-/home/colton/Desktop/FBHQ/hh-wt/s4-fin2-w21b-driver-gocache}"
EG_DIR="${FINWORLD_EG_DIR:-/home/colton/Desktop/FBHQ/hh-wt/s4-fin2-w21b-driver-eg}"
FAKETIME_DIR="${FINWORLD_FAKETIME_DIR:-${EG_DIR}/faketime}"
FAKETIME_FILE="${FAKETIME_DIR}/clock"
CONTAINER_NAME="${FINWORLD_PG_CONTAINER:-s4-fin2-w21b-pg}"
PG_PORT="${FINWORLD_PG_PORT:-57728}"
OUT_DIR="${FINWORLD_OUT_DIR:-/home/colton/Desktop/FBHQ/hh-s4/fin2/gates/w21b}"

# 1. Check free space on /home (stop under 30 GB).
AVAIL_KB="$(df -Pk /home | awk 'NR==2 {print $4}')"
if [ -z "$AVAIL_KB" ] || [ "$AVAIL_KB" -lt 31457280 ]; then
	echo "run.sh: refusing to start: /home has less than 30 GB free (${AVAIL_KB} KB)" >&2
	exit 1
fi

mkdir -p "$GOCACHE_DIR" "$FAKETIME_DIR" "$OUT_DIR"

# 2. Prepare evening-guard Go toolchain if not already prepared.
if [ ! -x "${EG_DIR}/toolchain/bin/go" ]; then
	echo "run.sh: preparing evening-guard toolchain at ${EG_DIR}..."
	bash "${REPO_ROOT}/scripts/evening-guard/prepare.sh" "$EG_DIR" || exit 1
fi
sed -i 's/sec += hhClockShift$/sec += hhClockShift()/' "${EG_DIR}/time.go" || exit 1
cat > "${EG_DIR}/hh_clockshift.go" <<'G'
package time

import "syscall"

// hhClockShift returns whole seconds added to the wall clock (never the monotonic clock) of every
// time.Now, read from HH_CLOCK_SHIFT_SECONDS; zero when unset or unreadable.
func hhClockShift() int64 {
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
}
G
cp -f "${EG_DIR}/time.go" "${EG_DIR}/toolchain/src/time/time.go" || exit 1
cp -f "${EG_DIR}/hh_clockshift.go" "${EG_DIR}/toolchain/src/time/hh_clockshift.go" || exit 1

# 3. Build core with -tags finworld using the evening-guard toolchain and overlay.
BIN_PATH="${EG_DIR}/core-finworld"
echo "run.sh: building core (-tags finworld) -> ${BIN_PATH}..."
(
	cd "${REPO_ROOT}/core" &&
	GOROOT="${EG_DIR}/toolchain" \
	GOCACHE="$GOCACHE_DIR" \
	PATH="${EG_DIR}/toolchain/bin:${PATH}" \
	"${EG_DIR}/toolchain/bin/go" build -overlay="${EG_DIR}/overlay.json" -tags finworld -o "$BIN_PATH" ./cmd/core/
) || exit 1

# 4. Write initial faketime offset (+0) atomically and ensure faketime Postgres is running.
printf '+0\n' > "${FAKETIME_FILE}.tmp" || exit 1
mv -f "${FAKETIME_FILE}.tmp" "$FAKETIME_FILE" || exit 1

if [ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER_NAME" 2>/dev/null || true)" != "true" ]; then
	docker rm -f -v "$CONTAINER_NAME" >/dev/null 2>&1 || true
	echo "run.sh: starting faketime Postgres container ${CONTAINER_NAME} on 127.0.0.1:${PG_PORT}..."
	docker run -d \
		--name "$CONTAINER_NAME" \
		-p "127.0.0.1:${PG_PORT}:5432" \
		-e POSTGRES_PASSWORD=postgres \
		-e POSTGRES_DB=finworld \
		-v "${FAKETIME_DIR}:/etc/faketime" \
		postgres:16-alpine \
		sh -c 'apk add --no-cache libfaketime >/dev/null && LD_PRELOAD=/usr/lib/faketime/libfaketime.so.1 FAKETIME_TIMESTAMP_FILE=/etc/faketime/clock FAKETIME_NO_CACHE=1 exec docker-entrypoint.sh postgres' || exit 1
fi

echo "run.sh: waiting for Postgres on 127.0.0.1:${PG_PORT}..."
READY=0
for _ in $(seq 1 60); do
	if docker exec "$CONTAINER_NAME" pg_isready -U postgres -d finworld >/dev/null 2>&1; then
		READY=1
		break
	fi
	sleep 1
done
if [ "$READY" -ne 1 ]; then
	echo "run.sh: Postgres did not become ready in time" >&2
	exit 1
fi

# Reset finworld database to a clean slate before day 0.
docker exec "$CONTAINER_NAME" psql -U postgres -d postgres -v ON_ERROR_STOP=1 -q \
	-c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'finworld' AND pid <> pg_backend_pid();" \
	-c "DROP DATABASE IF EXISTS finworld;" \
	-c "CREATE DATABASE finworld;" >/dev/null || exit 1

DATABASE_URL="postgres://postgres:postgres@127.0.0.1:${PG_PORT}/finworld?sslmode=disable"

set_day_clock() {
	local day_date="$1"
	local target_epoch
	local now_epoch
	local shift_sec
	target_epoch="$(TZ="America/Toronto" date -d "${day_date} 12:00:00" +%s)"
	now_epoch="$(date +%s)"
	shift_sec=$(( target_epoch - now_epoch ))
	if [ "$shift_sec" -ge 0 ]; then
		printf '+%d\n' "$shift_sec" > "${FAKETIME_FILE}.tmp" || return 1
	else
		printf -- '%d\n' "$shift_sec" > "${FAKETIME_FILE}.tmp" || return 1
	fi
	mv -f "${FAKETIME_FILE}.tmp" "$FAKETIME_FILE" || return 1
	echo "$shift_sec"
}

run_scoped() {
	local shift_sec="$1"
	shift
	systemd-run --user --scope --quiet --expand-environment=no -p MemoryMax=24G \
		bash -c 'cd "$1" && DATABASE_URL="$2" HH_CLOCK_SHIFT_SECONDS="$3" HH_WEB_DEALER_NAME="$4" "$5" "${@:6}"' \
		_ "${REPO_ROOT}/core" "$DATABASE_URL" "$shift_sec" "$DEALER_NAME" "$BIN_PATH" "$@"
}

# 5. Read the trading day schedule from core finworld days.
DAYS_LIST="$("$BIN_PATH" finworld days --seed "$SEED" --dealer "$DEALER_NAME")" || exit 1
TOTAL_START_MS="$(date +%s%3N)"
LAST_DAY_IDX=0
LAST_SHIFT_SEC=0

while read -r DAY_IDX DAY_DATE DAY_YM; do
	[ -z "$DAY_IDX" ] && continue
	REAL_DAY_BEFORE="$(TZ="America/Toronto" date +%Y-%m-%d)"
	DAY_START_MS="$(date +%s%3N)"

	SHIFT_SEC="$(set_day_clock "$DAY_DATE")" || exit 1

	if [ "$DAY_IDX" -eq 0 ]; then
		echo "run.sh: migrating and seeding under day 0 (${DAY_DATE}, shift=${SHIFT_SEC}s)..."
		run_scoped "$SHIFT_SEC" migrate || exit 1
		run_scoped "$SHIFT_SEC" seed || exit 1
	fi

	OUT_LINE="$(run_scoped "$SHIFT_SEC" finworld day --day "$DAY_IDX" --seed "$SEED" --dealer "$DEALER_NAME" --anchor "$ANCHOR")" || {
		echo "run.sh: failed on day ${DAY_IDX} (${DAY_DATE})" >&2
		exit 1
	}

	DAY_END_MS="$(date +%s%3N)"
	REAL_DAY_AFTER="$(TZ="America/Toronto" date +%Y-%m-%d)"
	ELAPSED_MS=$(( DAY_END_MS - DAY_START_MS ))

	echo "${OUT_LINE} [${DAY_YM} elapsed=${ELAPSED_MS}ms]"

	if [ "$REAL_DAY_AFTER" != "$REAL_DAY_BEFORE" ]; then
		echo "run.sh: evening guard tripped on day ${DAY_IDX} (${DAY_DATE}): real house date crossed midnight (${REAL_DAY_BEFORE} -> ${REAL_DAY_AFTER})" >&2
		exit 1
	fi

	LAST_DAY_IDX="$DAY_IDX"
	LAST_SHIFT_SEC="$SHIFT_SEC"
done <<< "$DAYS_LIST"

TOTAL_END_MS="$(date +%s%3N)"
TOTAL_ELAPSED_MS=$(( TOTAL_END_MS - TOTAL_START_MS ))
echo "run.sh: all days 0..${LAST_DAY_IDX} completed in ${TOTAL_ELAPSED_MS}ms"

# 6. Run tie checks through the final day. Only dump if every tie passes.
echo "run.sh: running core finworld check --through-day ${LAST_DAY_IDX}..."
run_scoped "$LAST_SHIFT_SEC" finworld check --through-day "$LAST_DAY_IDX" --seed "$SEED" --dealer "$DEALER_NAME" --anchor "$ANCHOR" || {
	echo "run.sh: tie check failed; skipping pg_dump" >&2
	exit 1
}

echo "run.sh: running core finance-check..."
systemd-run --user --scope --quiet --expand-environment=no -p MemoryMax=24G \
	bash -c 'cd "$1" && DATABASE_URL="$2" HH_CLOCK_SHIFT_SECONDS="$3" HH_WEB_DEALER_NAME="$4" HH_BOX_KIND=finance APP_ENV=staging CORE_SIGNIN_MODE=console "$5" finance-check' \
	_ "${REPO_ROOT}/core" "$DATABASE_URL" "$LAST_SHIFT_SEC" "$DEALER_NAME" "$BIN_PATH" || {
	echo "run.sh: finance-check failed; skipping pg_dump" >&2
	exit 1
}

# 7. Dump the database and compute its sha256.
#
# The dump is taken WITHOUT the owner statements: the world's objects belong
# to the build cluster's roles, and a dump that carries them can only be
# restored into a cluster that has those roles. The box restores the dump
# under its own role, and the reset maps the assist role names onto the box
# database's own suffix before it restores.
#
# The owner statements the dump leaves out are appended to it, in the dump's
# own words: the world's assist owned objects keep their owners on the box,
# the way the world was built. Without them the world's own migrations that
# alter those objects would refuse on a restored world.
DUMP_FILE="${OUT_DIR}/finworld_seed${SEED}_${ANCHOR}.sql"
SHA_FILE="${DUMP_FILE}.sha256"
echo "run.sh: all ties passed; writing pg_dump to ${DUMP_FILE}..."
docker exec "$CONTAINER_NAME" pg_dump -U postgres -d finworld --no-owner > "$DUMP_FILE" || exit 1
docker exec -i "$CONTAINER_NAME" psql -U postgres -d finworld -tAq < "${REPO_ROOT}/deploy/finance/world/ownership.sql" >> "$DUMP_FILE" || exit 1
sha256sum "$DUMP_FILE" | tee "$SHA_FILE"

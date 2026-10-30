//go:build finworld && integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	finworldDefaultContainerName = "s4-fin2-fwlegs-pg"
	finworldDefaultPGPort        = "57723"
)

// finworldDynamicClockShiftSrc mirrors deploy/finance/world/run.sh: hhClockShift is a function
// evaluated on every time.Now call rather than a package-init var, so mid-process updates to
// HH_CLOCK_SHIFT_SECONDS (such as finworldExecutePeriodClose and finworldExecuteYearClose advancing
// into the next month/year before calling the close route) move the Go clock immediately.
const finworldDynamicClockShiftSrc = `package time

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
`

type finworldTestRig struct {
	workDir string
	dsn     string
	ftFile  string
	overlay string
	binPath string
}

var (
	finworldRigsMu sync.Mutex
	finworldRigs   = map[testing.TB]*finworldTestRig{}
)

// finworldRigFor ensures the calling test has a pinned libfaketime Postgres container and an
// evening-guard Go toolchain overlay, matching deploy/finance/world/run.sh. When Docker is absent
// or unreachable, the test skips in words and never passes silently.
func finworldRigFor(t *testing.T) *finworldTestRig {
	t.Helper()
	finworldRigsMu.Lock()
	if rig, ok := finworldRigs[t]; ok {
		finworldRigsMu.Unlock()
		return rig
	}
	finworldRigsMu.Unlock()

	envDSN := strings.TrimSpace(os.Getenv("FINWORLD_PG_DSN"))
	envFT := strings.TrimSpace(os.Getenv("FINWORLD_FAKETIME_FILE"))
	envOverlay := strings.TrimSpace(os.Getenv("FINWORLD_OVERLAY_JSON"))
	if envDSN != "" && envFT != "" && envOverlay != "" {
		rig := &finworldTestRig{
			dsn:     envDSN,
			ftFile:  envFT,
			overlay: envOverlay,
		}
		finworldRigsMu.Lock()
		finworldRigs[t] = rig
		finworldRigsMu.Unlock()
		t.Cleanup(func() {
			finworldRigsMu.Lock()
			delete(finworldRigs, t)
			finworldRigsMu.Unlock()
		})
		return rig
	}

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("finworld driver leg skipped: docker binary is absent from PATH (%v)", err)
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("finworld driver leg skipped: docker daemon is not reachable (%v: %s)", err, strings.TrimSpace(string(out)))
	}

	workDir := t.TempDir()
	egDir := filepath.Join(workDir, "eg")
	ftDir := filepath.Join(egDir, "faketime")
	ftFile := filepath.Join(ftDir, "clock")
	if err := os.MkdirAll(ftDir, 0o755); err != nil {
		t.Fatalf("create faketime dir: %v", err)
	}
	if err := os.WriteFile(ftFile, []byte("+0\n"), 0o644); err != nil {
		t.Fatalf("init faketime clock file: %v", err)
	}

	prepareScript := filepath.Clean("../../../scripts/evening-guard/prepare.sh")
	if _, err := os.Stat(prepareScript); err != nil {
		prepareScript = filepath.Clean("scripts/evening-guard/prepare.sh")
	}
	if out, err := exec.Command("bash", prepareScript, egDir).CombinedOutput(); err != nil {
		t.Fatalf("prepare evening-guard toolchain (%s): %v\n%s", prepareScript, err, string(out))
	}
	if out, err := exec.Command("chmod", "-R", "u+w", filepath.Join(egDir, "toolchain")).CombinedOutput(); err != nil {
		t.Fatalf("chmod evening-guard toolchain writable: %v\n%s", err, string(out))
	}

	timeGoPath := filepath.Join(egDir, "time.go")
	timeGoRaw, err := os.ReadFile(timeGoPath)
	if err != nil {
		t.Fatalf("read prepared time.go: %v", err)
	}
	patchedTimeGo := strings.Replace(string(timeGoRaw), "sec += hhClockShift\n", "sec += hhClockShift()\n", 1)
	if patchedTimeGo == string(timeGoRaw) {
		t.Fatalf("prepared time.go did not contain expected 'sec += hhClockShift' anchor")
	}
	if err := os.WriteFile(timeGoPath, []byte(patchedTimeGo), 0o644); err != nil {
		t.Fatalf("write patched time.go: %v", err)
	}
	clockShiftPath := filepath.Join(egDir, "hh_clockshift.go")
	if err := os.WriteFile(clockShiftPath, []byte(finworldDynamicClockShiftSrc), 0o644); err != nil {
		t.Fatalf("write dynamic hh_clockshift.go: %v", err)
	}
	toolchainTimeDir := filepath.Join(egDir, "toolchain", "src", "time")
	if err := os.WriteFile(filepath.Join(toolchainTimeDir, "time.go"), []byte(patchedTimeGo), 0o644); err != nil {
		t.Fatalf("copy time.go into toolchain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolchainTimeDir, "hh_clockshift.go"), []byte(finworldDynamicClockShiftSrc), 0o644); err != nil {
		t.Fatalf("copy hh_clockshift.go into toolchain: %v", err)
	}

	containerName := strings.TrimSpace(os.Getenv("FINWORLD_PG_CONTAINER"))
	if containerName == "" {
		containerName = finworldDefaultContainerName
	}
	pgPort := strings.TrimSpace(os.Getenv("FINWORLD_PG_PORT"))
	if pgPort == "" {
		pgPort = finworldDefaultPGPort
	}

	_ = exec.Command("docker", "rm", "-f", "-v", containerName).Run()
	t.Cleanup(func() {
		finworldRigsMu.Lock()
		delete(finworldRigs, t)
		finworldRigsMu.Unlock()
		_ = exec.Command("docker", "rm", "-f", "-v", containerName).Run()
	})

	runArgs := []string{
		"run", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%s:5432", pgPort),
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=finworld",
		"-v", ftDir + ":/etc/faketime",
		"postgres:16-alpine",
		"sh", "-c",
		"apk add --no-cache libfaketime >/dev/null && LD_PRELOAD=/usr/lib/faketime/libfaketime.so.1 FAKETIME_TIMESTAMP_FILE=/etc/faketime/clock FAKETIME_NO_CACHE=1 exec docker-entrypoint.sh postgres -c fsync=off -c synchronous_commit=off -c full_page_writes=off",
	}
	if out, err := exec.Command("docker", runArgs...).CombinedOutput(); err != nil {
		t.Fatalf("start libfaketime postgres container %s on 127.0.0.1:%s: %v\n%s", containerName, pgPort, err, string(out))
	}

	dsn := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%s/finworld?sslmode=disable", pgPort)
	ready := false
	for i := 0; i < 60; i++ {
		if err := exec.Command("docker", "exec", containerName, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "finworld").Run(); err == nil {
			if probePool, err := db.Connect(context.Background(), dsn); err == nil {
				var one int
				qerr := probePool.QueryRow(context.Background(), "SELECT 1").Scan(&one)
				probePool.Close()
				if qerr == nil && one == 1 {
					ready = true
					break
				}
			}
		}
		time.Sleep(time.Second)
	}
	if !ready {
		t.Fatalf("libfaketime postgres container %s on 127.0.0.1:%s did not become ready within 60s", containerName, pgPort)
	}

	rig := &finworldTestRig{
		workDir: workDir,
		dsn:     dsn,
		ftFile:  ftFile,
		overlay: filepath.Join(egDir, "overlay.json"),
	}
	finworldRigsMu.Lock()
	finworldRigs[t] = rig
	finworldRigsMu.Unlock()
	return rig
}

func finworldTestDSN(t *testing.T) string {
	t.Helper()
	return finworldRigFor(t).dsn
}

func finworldTestFaketimeFile(t *testing.T) string {
	t.Helper()
	return finworldRigFor(t).ftFile
}

func finworldTestOverlay(t *testing.T) string {
	t.Helper()
	return finworldRigFor(t).overlay
}

func finworldSetDayClock(t *testing.T, ftFile string, target time.Time) int64 {
	t.Helper()
	shift := target.Unix() - time.Now().Unix()
	line := fmt.Sprintf("%+d\n", shift)
	tmpFile := ftFile + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(line), 0o644); err != nil {
		t.Fatalf("write faketime tmp file %s: %v", tmpFile, err)
	}
	if err := os.Rename(tmpFile, ftFile); err != nil {
		t.Fatalf("rename faketime file %s: %v", ftFile, err)
	}
	return shift
}

func finworldBuildBinary(t *testing.T) string {
	t.Helper()
	rig := finworldRigFor(t)
	if rig.binPath != "" {
		return rig.binPath
	}
	outDir := rig.workDir
	if outDir == "" {
		outDir = t.TempDir()
	}
	binPath := filepath.Join(outDir, "core-finworld")
	overlay := rig.overlay
	goroot := filepath.Join(filepath.Dir(overlay), "toolchain")
	goBin := filepath.Join(goroot, "bin", "go")
	if _, err := os.Stat(goBin); err != nil {
		goBin = "go"
	}
	cmd := exec.Command(goBin, "build", "-overlay", overlay, "-tags", "finworld", "-o", binPath, ".")
	cmd.Env = append(os.Environ(), "GOROOT="+goroot, "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build core-finworld binary: %v\n%s", err, string(out))
	}
	rig.binPath = binPath
	return binPath
}

func finworldRunSubprocess(t *testing.T, binPath, dsn string, shiftSec int64, extraEnv []string, args ...string) string {
	t.Helper()
	out, err := finworldRunSubprocessAllowErr(binPath, dsn, shiftSec, extraEnv, args...)
	if err != nil {
		t.Fatalf("core %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func finworldRunSubprocessAllowErr(binPath, dsn string, shiftSec int64, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command(binPath, args...)
	if _, err := os.Stat("db/migrations"); err != nil {
		if _, err2 := os.Stat("../../db/migrations"); err2 == nil {
			cmd.Dir = filepath.Clean("../..")
		}
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"DATABASE_URL=" + dsn,
		"ONESHOT_DATABASE_URL=" + dsn,
		"ALLOW_SEED=true",
		"APP_ENV=dev",
		"HH_CLOCK_SHIFT_SECONDS=" + strconv.FormatInt(shiftSec, 10),
	}
	env = append(env, extraEnv...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func finworldResetSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS ai, ai_pro, public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO public;`); err != nil {
		t.Fatalf("reset public schema: %v", err)
	}
}

func TestFinWorldDriver_StepA(t *testing.T) {
	ctx := context.Background()
	dsn := finworldTestDSN(t)
	ftFile := finworldTestFaketimeFile(t)
	loc := houseday.DefaultZone().Location()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect finworld db: %v", err)
	}
	defer pool.Close()

	finworldResetSchema(t, ctx, pool)
	binPath := finworldBuildBinary(t)

	// Day 0: 2025-10-01 12:00 America/Toronto.
	day0Target := time.Date(2025, time.October, 1, 12, 0, 0, 0, loc)
	shift0 := finworldSetDayClock(t, ftFile, day0Target)

	finworldRunSubprocess(t, binPath, dsn, shift0, nil, "migrate")
	finworldRunSubprocess(t, binPath, dsn, shift0, nil, "seed")
	finworldRunSubprocess(t, binPath, dsn, shift0, nil, "finworld", "day", "--day", "0")

	// Guard checks: Day 0 a second time must refuse; box env must refuse.
	if _, err := finworldRunSubprocessAllowErr(binPath, dsn, shift0, nil, "finworld", "day", "--day", "0"); err == nil {
		t.Fatalf("expected second day 0 run on already-marked database to refuse")
	}
	if _, err := finworldRunSubprocessAllowErr(binPath, dsn, shift0, []string{"APP_ENV=staging", "HH_BOX_KIND=finance"}, "finworld", "day", "--day", "1"); err == nil {
		t.Fatalf("expected finworld to refuse when box environment variables are set")
	}

	// Day 1: 2025-10-02 12:00 America/Toronto.
	day1Target := time.Date(2025, time.October, 2, 12, 0, 0, 0, loc)
	shift1 := finworldSetDayClock(t, ftFile, day1Target)
	finworldRunSubprocess(t, binPath, dsn, shift1, nil, "finworld", "day", "--day", "1")

	// Assert documents created on Day 0 (2025-10-01) and Day 1 (2025-10-02) carry the simulated dates in America/Toronto.
	for _, dayDate := range []string{"2025-10-01", "2025-10-02"} {
		var invCount, badInvDates int
		if err := pool.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE issue_date::text <> $1 OR (finalized_at AT TIME ZONE 'America/Toronto')::date::text <> $1)
			  FROM invoices
			 WHERE (created_at AT TIME ZONE 'America/Toronto')::date = $1::date`, dayDate).Scan(&invCount, &badInvDates); err != nil {
			t.Fatalf("query %s invoices: %v", dayDate, err)
		}
		if invCount == 0 || badInvDates != 0 {
			t.Fatalf("%s invoices: count=%d bad_dates=%d (want >0 and 0)", dayDate, invCount, badInvDates)
		}

		var payCount, badPayDates int
		if err := pool.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE (received_at AT TIME ZONE 'America/Toronto')::date::text <> $1)
			  FROM payments
			 WHERE (created_at AT TIME ZONE 'America/Toronto')::date = $1::date`, dayDate).Scan(&payCount, &badPayDates); err != nil {
			t.Fatalf("query %s payments: %v", dayDate, err)
		}
		if payCount == 0 || badPayDates != 0 {
			t.Fatalf("%s payments: count=%d bad_dates=%d (want >0 and 0)", dayDate, payCount, badPayDates)
		}

		var jCount, badJDates int
		if err := pool.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE fp.year <> 2025 OR fp.month <> 10 OR (j.posted_at AT TIME ZONE 'America/Toronto')::date::text <> $1)
			  FROM gl_journals j
			  JOIN fiscal_periods fp ON fp.id = j.period_id
			 WHERE j.status = 'POSTED'
			   AND (j.posted_at AT TIME ZONE 'America/Toronto')::date = $1::date`, dayDate).Scan(&jCount, &badJDates); err != nil {
			t.Fatalf("query %s journals: %v", dayDate, err)
		}
		if jCount == 0 || badJDates != 0 {
			t.Fatalf("%s journals: count=%d bad_dates=%d (want >0 and 0)", dayDate, jCount, badJDates)
		}
	}

	// Assert all ties at the end of Day 1.
	checkOut := finworldRunSubprocess(t, binPath, dsn, shift1, nil, "finworld", "check", "--through-day", "1")
	t.Logf("Step A tie check output:\n%s", checkOut)
}

func TestFinWorldDriver_StepB(t *testing.T) {
	ctx := context.Background()
	dsn := finworldTestDSN(t)
	ftFile := finworldTestFaketimeFile(t)
	loc := houseday.DefaultZone().Location()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect finworld db: %v", err)
	}
	defer pool.Close()

	finworldResetSchema(t, ctx, pool)
	binPath := finworldBuildBinary(t)

	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}
	m0 := plan.Months[0]
	if len(m0.TradingDays) == 0 {
		t.Fatalf("month 0 has no trading days")
	}

	var lastShift int64
	var lastDayIdx int
	for _, td := range m0.TradingDays {
		dayDate, err := time.ParseInLocation("2006-01-02", td.Date, loc)
		if err != nil {
			t.Fatalf("parse trading day %s: %v", td.Date, err)
		}
		target := time.Date(dayDate.Year(), dayDate.Month(), dayDate.Day(), 12, 0, 0, 0, loc)
		shift := finworldSetDayClock(t, ftFile, target)
		if td.TradingDayIndex == 0 {
			finworldRunSubprocess(t, binPath, dsn, shift, nil, "migrate")
			finworldRunSubprocess(t, binPath, dsn, shift, nil, "seed")
		}
		finworldRunSubprocess(t, binPath, dsn, shift, nil, "finworld", "day", "--day", strconv.Itoa(td.TradingDayIndex))
		lastShift = shift
		lastDayIdx = td.TradingDayIndex
	}

	// Verify 2025-10 fiscal period is CLOSED.
	var periodStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE year = 2025 AND month = 10`).Scan(&periodStatus); err != nil {
		t.Fatalf("query 2025-10 fiscal_period: %v", err)
	}
	if periodStatus != "CLOSED" {
		t.Fatalf("2025-10 fiscal_period status = %q, want CLOSED", periodStatus)
	}

	// Verify 2025-10 bank reconciliation is COMPLETED with zero variance.
	var recStatus string
	var stmtBal, glBal, variance int64
	if err := pool.QueryRow(ctx, `
		SELECT status, statement_balance_cents, gl_balance_cents, variance_cents
		  FROM bank_reconciliations
		 WHERE year = 2025 AND month = 10`).Scan(&recStatus, &stmtBal, &glBal, &variance); err != nil {
		t.Fatalf("query 2025-10 bank_reconciliation: %v", err)
	}
	if recStatus != "COMPLETED" || variance != 0 || stmtBal == glBal {
		t.Fatalf("2025-10 bank_reconciliation: status=%s stmt=%d gl=%d var=%d",
			recStatus, stmtBal, glBal, variance)
	}

	checkOut := finworldRunSubprocess(t, binPath, dsn, lastShift, nil, "finworld", "check", "--through-day", strconv.Itoa(lastDayIdx))
	t.Logf("Step B tie check output (through day %d):\n%s", lastDayIdx, checkOut)
}

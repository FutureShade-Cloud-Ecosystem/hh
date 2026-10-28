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
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

func finworldTestDSN(t *testing.T) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("FINWORLD_PG_DSN")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); v != "" {
		return v
	}
	return "postgres://postgres:postgres@127.0.0.1:57728/finworld?sslmode=disable"
}

func finworldTestFaketimeFile(t *testing.T) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("FINWORLD_FAKETIME_FILE")); v != "" {
		return v
	}
	return "/home/colton/Desktop/FBHQ/hh-wt/s4-fin2-w21b-driver-eg/faketime/clock"
}

func finworldTestOverlay(t *testing.T) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("FINWORLD_OVERLAY_JSON")); v != "" {
		return v
	}
	return "/home/colton/Desktop/FBHQ/hh-wt/s4-fin2-w21b-driver-eg/overlay.json"
}

func finworldSetDayClock(t *testing.T, ftFile string, target time.Time) int64 {
	t.Helper()
	shift := target.Unix() - time.Now().Unix()
	line := fmt.Sprintf("%+d\n", shift)
	if err := os.WriteFile(ftFile, []byte(line), 0o644); err != nil {
		t.Fatalf("write faketime file %s: %v", ftFile, err)
	}
	return shift
}

func finworldBuildBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "core-finworld")
	overlay := finworldTestOverlay(t)
	goroot := filepath.Join(filepath.Dir(overlay), "toolchain")
	goBin := filepath.Join(goroot, "bin", "go")
	if _, err := os.Stat(goBin); err != nil {
		goBin = "go"
	}
	gocache := os.Getenv("GOCACHE")
	if gocache == "" {
		gocache = "/home/colton/Desktop/FBHQ/hh-wt/s4-fin2-w21b-driver-gocache"
	}
	cmd := exec.Command(goBin, "build", "-overlay", overlay, "-tags", "finworld", "-o", binPath, ".")
	cmd.Env = append(os.Environ(), "GOCACHE="+gocache, "GOROOT="+goroot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build core-finworld binary: %v\n%s", err, string(out))
	}
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

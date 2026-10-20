package parity

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const repoRoot = "../../../.."

func patterns(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("visibility_files.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// Every path the rule names still exists, so a renamed visibility file cannot silently leave the rule.
func TestVisibilityFilesAllExist(t *testing.T) {
	pats := patterns(t)
	if len(pats) < 8 {
		t.Fatalf("the visibility list has only %d entries", len(pats))
	}
	for _, p := range pats {
		if _, err := os.Stat(filepath.Join(repoRoot, p)); err != nil {
			t.Errorf("visibility path %s: %v", p, err)
		}
	}
}

// The script's matcher picks exactly the visibility files out of a change, and the integration job runs it.
func TestPathRuleMatchesVisibilityFilesOnlyAndIsWiredIntoCI(t *testing.T) {
	script := filepath.Join(repoRoot, "scripts", "ci", "assist-parity-path-rule.sh")
	cmd := exec.Command("bash", script, "--match")
	cmd.Stdin = strings.NewReader(strings.Join([]string{
		"core/internal/order/summary.go",
		"core/internal/assist/readtx/run.go",
		"core/internal/assist/parity/redteam_integration_test.go",
		"core/db/migrations/0419_assist_orders_views_and_query_log.sql",
		"core/internal/billing/invoice.go",
		"web/apps/counter-till/src/main.ts",
		"core/internal/order/summary_test.go",
	}, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("bash is not available: %v", err)
	}
	got := strings.Fields(string(out))
	want := []string{
		"core/internal/order/summary.go",
		"core/internal/assist/readtx/run.go",
		"core/internal/assist/parity/redteam_integration_test.go",
		"core/db/migrations/0419_assist_orders_views_and_query_log.sql",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("matched %v, want %v", got, want)
	}
	ci, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ci), "scripts/ci/assist-parity-path-rule.sh") {
		t.Error("the integration job does not run the assist parity path rule")
	}
}

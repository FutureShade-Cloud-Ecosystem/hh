package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

// headerlessStore is a statementStore whose letterhead lookup fails and that keeps the one statement
// it is asked to save.
type headerlessStore struct {
	statementStore
	saved Statement
}

func (headerlessStore) CreditPolicy(context.Context, string) (CreditPolicy, error) {
	return CreditPolicy{AgingBucketDays: 30}, nil
}
func (headerlessStore) AgingRowsIn(context.Context, time.Time, int, string, BranchReach) ([]AgingRow, error) {
	return []AgingRow{{CustomerID: "c1", CustomerName: "Customer one", TotalCents: 1000}}, nil
}
func (headerlessStore) StatementLines(context.Context, string, time.Time, BranchReach) ([]StatementLine, error) {
	return nil, nil
}
func (headerlessStore) BranchHeader(context.Context, string) (StatementHeader, error) {
	return StatementHeader{}, errors.New("no letterhead")
}
func (headerlessStore) CreateRun(context.Context, string, string, time.Time, string) (string, error) {
	return "run1", nil
}
func (s *headerlessStore) SaveStatement(_ context.Context, _ string, st Statement, _ *StatementNotice, _ StatementOutbox) (string, error) {
	s.saved = st
	return "st1", nil
}
func (headerlessStore) FinishRun(context.Context, string, string, int, int, string) error { return nil }

func renderWithoutLetterhead(t *testing.T) string {
	t.Helper()
	store := &headerlessStore{}
	svc := NewStatementService(store)
	if _, _, err := svc.GenerateRun(context.Background(), RunModeMass, "", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), "actor"); err != nil {
		t.Fatalf("GenerateRun: %v", err)
	}
	return store.saved.HTML
}

// With no letterhead the statement names the dealer's own configured name, not a name in code.
func TestStatementFallbackHeader_ReadsTheConfiguredDealerName(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	html := renderWithoutLetterhead(t)
	if !strings.Contains(html, "Gable Landscape Supply") {
		t.Errorf("statement does not carry the configured dealer name: %q", html)
	}
	if strings.Contains(strings.ToLower(html), "dibbits") {
		t.Error("statement carries a dealer's name from code")
	}
}

// With no dealer name set the plain header is a neutral role word.
func TestStatementFallbackHeader_NeutralWhenNoDealerName(t *testing.T) {
	dealer.Set("")
	html := renderWithoutLetterhead(t)
	if strings.Contains(strings.ToLower(html), "dibbits") {
		t.Errorf("statement carries a dealer's name from code: %q", html)
	}
	if !strings.Contains(html, "Your dealer") {
		t.Errorf("statement does not carry the neutral header: %q", html)
	}
}

func (headerlessStore) GetRun(context.Context, string) (StatementRun, []Statement, error) {
	return StatementRun{ID: "run1"}, nil, nil
}

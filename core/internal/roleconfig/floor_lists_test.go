package roleconfig

// floor_lists_test.go, the branch scoped quote floor approvers list (plan
// item E, CP0-1 answer B): the Sales manager of the quote's branch decides a
// quote floor request beside the General Manager and the Financial
// Controller. The list is the configuration's data, never a code constant.
import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultHoldsQuoteFloorBranchApprovers(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	got := cfg.QuoteFloorBranchApprovers()
	if len(got) != 1 || got[0] != "sales_manager" {
		t.Fatalf("the decided default's quote floor branch approvers must be the Sales manager alone, got %v", got)
	}
}

func TestLoadRefusesConfigWithoutQuoteFloorBranchApprovers(t *testing.T) {
	// A document missing the list refuses: a dropped or null list would
	// silently strip the branch's Sales manager of the decide authority.
	doc := DefaultDocument(t)
	doc.Lists.QuoteFloorBranchApprovers = nil
	if _, err := Validate(doc); err == nil {
		t.Fatal("a configuration without the quote floor branch approvers list must refuse")
	}
}

func TestLoadRefusesUnknownRoleOnQuoteFloorBranchApprovers(t *testing.T) {
	doc := DefaultDocument(t)
	doc.Lists.QuoteFloorBranchApprovers = []string{"not_a_role"}
	_, err := Validate(doc)
	if err == nil || !strings.Contains(err.Error(), "quote_floor_branch_approvers") {
		t.Fatalf("an unknown role on the list must refuse naming the list, got %v", err)
	}
}

func TestLoadRefusesHROnQuoteFloorBranchApprovers(t *testing.T) {
	doc := DefaultDocument(t)
	doc.Lists.QuoteFloorBranchApprovers = []string{"hr"}
	_, err := Validate(doc)
	if err == nil || !strings.Contains(err.Error(), "hr") {
		t.Fatalf("the walls refuse HR on every approver list of money, got %v", err)
	}
}

func TestLoadParsesTheListFromTheDocument(t *testing.T) {
	var doc Document
	if err := json.Unmarshal([]byte(`{"quote_floor_branch_approvers": ["branch_manager"]}`), &doc.Lists); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Lists.QuoteFloorBranchApprovers) != 1 || doc.Lists.QuoteFloorBranchApprovers[0] != "branch_manager" {
		t.Fatalf("the document's list must parse onto the Lists field, got %v", doc.Lists.QuoteFloorBranchApprovers)
	}
}

// DefaultDocument builds the decided default as an in memory document the
// refusal tests mutate.
func DefaultDocument(t *testing.T) *Document {
	t.Helper()
	var doc Document
	if err := json.Unmarshal(defaultData, &doc); err != nil {
		t.Fatalf("unmarshal the default: %v", err)
	}
	return &doc
}

package assist

// answer_contract_test.go: the Go side always fills every field the contract marks required (PANEL-API
// 8.2; the panel's own types in web/packages/shell-chrome/src/tanyai-view.ts require them). The required
// lists are read from the contract document itself, so a field made required there that the Go side can
// leave out fails here.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func requiredOf(t *testing.T, schema string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Required []string `yaml:"required"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Components.Schemas[schema]
	if !ok {
		t.Fatalf("schema %s is not in the contract", schema)
	}
	return s.Required
}

func keysOf(t *testing.T, v any) map[string]bool {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k, raw := range m {
		if string(raw) != "null" && string(raw) != `""` {
			out[k] = true
		}
	}
	return out
}

func mustHave(t *testing.T, schema string, v any) {
	t.Helper()
	have := keysOf(t, v)
	for _, k := range requiredOf(t, schema) {
		if !have[k] {
			t.Errorf("%s: required field %q is missing or empty in %+v", schema, k, v)
		}
	}
}

func TestAnswerFieldsAreAlwaysFilledForWhatTheContractRequires(t *testing.T) {
	// Branch scoped asker whose branch the reader cannot name, and a global one; an open ended period.
	q, _, _ := newQuerier(&scriptTx{plan: cheapPlan, names: orderCols, data: [][]any{{"SO-1", "100", "11111111-1111-1111-1111-111111111111", "SO-1"}}})
	scoped, err := q.Query(context.Background(), dqManager, `{"view":"ai.orders","where":[{"col":"created_at","op":">=","value":"start_of_month"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Scope.Branches) != 1 {
		t.Fatalf("scope = %+v", scoped.Scope)
	}
	mustHave(t, "AssistScope", scoped.Scope)
	mustHave(t, "AssistScopeBranch", scoped.Scope.Branches[0])
	if scoped.Scope.Period == nil {
		t.Fatal("no period for a dated question")
	}
	mustHave(t, "AssistScopePeriod", scoped.Scope.Period)
	if scoped.Scope.Period.Label == "" || scoped.Scope.Branches[0].Label == "" {
		t.Errorf("empty labels: %+v", scoped.Scope)
	}
	// A named branch keeps its name.
	named := &ReadQuerier{Reader: q.Reader, Now: q.Now, Branches: fixedBranches{{BranchID: dqManager.BranchIDs[0], Name: "Trenton"}}}
	res, _ := named.Query(context.Background(), dqManager, `{"view":"ai.orders"}`)
	if res.Scope.Branches[0].Label != "Trenton" {
		t.Errorf("label = %q", res.Scope.Branches[0].Label)
	}
	// Citations and notices.
	for _, ref := range []Ref{{Kind: RefOrder, ID: "11111111-1111-1111-1111-111111111111", Label: "Order SO-1"}, {Kind: RefOrder, ID: "22222222-2222-2222-2222-222222222222"}} {
		run := &askRun{}
		r := Result{Command: DataQueryCode, Items: []Item{{Title: "x", Ref: &ref}}}
		run.absorb(DataQueryCode, &r)
		mustHave(t, "AssistCitation", run.cites[0])
	}
	for _, n := range []Notice{{Code: NoticeTooBroad}, {Code: NoticeRefused, Class: noticeClassFor(ClassCost)}} {
		mustHave(t, "AssistNotice", n)
	}
}

type fixedBranches []NamedBranch

func (f fixedBranches) Branches(context.Context, Caller) ([]NamedBranch, error) { return f, nil }

// The refusal classes are the panel's vocabulary (PANEL-API 8.2), so the glossary words every one.
func TestNoticeClassesAreThePanelsVocabulary(t *testing.T) {
	panel := []string{"cost", "margin", "payment_trail", "pii", "capability", "disabled"}
	for in, want := range map[string]string{
		ClassCost: "cost", ClassMargin: "margin", ClassPayment: "payment_trail", ClassPIIContact: "pii", ClassPIILoc: "pii",
	} {
		got := noticeClassFor(in)
		if got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
		found := false
		for _, p := range panel {
			found = found || p == got
		}
		if !found {
			t.Errorf("%s is not one of the panel's classes %v", got, panel)
		}
	}
	// And the contract's enum is exactly that list.
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "fragments", "catalog-support.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sorted := append([]string(nil), panel...)
	sort.Strings(sorted)
	if !strings.Contains(string(raw), "enum: ["+strings.Join(panel, ", ")+"]") {
		t.Errorf("the contract's notice class enum is not %v", panel)
	}
}

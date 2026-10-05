package billing

// load_receivables_test.go covers the import writer's refusals that
// need no database: the money guard (never a float, exact cents only),
// the record's dates and document number, and the references the
// invoice shape requires. Every refusal path returns before any
// statement runs, which is what lets these run against a nil
// transaction: a statement attempted on one would panic and fail the
// test. Every fixture is invented.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// ldImportItem builds the load.Write one imported open item carries.
func ldImportItem(amount any, issue, due, docNo string, refs ...load.RefMatch) load.Write {
	rec := map[string]any{
		"source_doc_no":     docNo,
		"issue_date":        issue,
		"due_date":          due,
		"open_amount_cents": amount,
	}
	return load.Write{
		OrgID:  "7b220000-0000-4000-8000-000000000011",
		Record: rec,
		Refs:   refs,
	}
}

// ldImportRefs is the customer and branch pair the invoice shape needs.
func ldImportRefs(customer, branch string) []load.RefMatch {
	return []load.RefMatch{
		{Kind: load.RefKindCustomer, LegacyID: customer, HHID: "7b220000-0000-4000-8000-0000000000c1"},
		{Kind: load.RefKindBranch, LegacyID: branch, HHID: "7b220000-0000-4000-8000-0000000000b1"},
	}
}

func TestReceivablesLoadWriterRefusesMoneyThatIsNotExactCents(t *testing.T) {
	w := NewReceivablesLoadWriter()
	for _, tc := range []struct {
		name   string
		amount any
	}{
		{"a float", 1250.5},
		{"a whole float", float64(1000)},
		{"a fraction as json number", json.Number("100.25")},
		{"an exponent", json.Number("1e3")},
		{"text", "1000"},
		{"zero", 0},
		{"zero as json number", json.Number("0")},
		{"absent", nil},
	} {
		_, err := w.Apply(context.Background(), nil, ldImportItem(tc.amount, "2021-06-01", "2021-07-01", "AR-1", ldImportRefs("CUST-1", "BR-1")...))
		var ref *load.Refusal
		if err == nil || !errors.As(err, &ref) {
			t.Fatalf("%s answered %v, want a refusal", tc.name, err)
		}
		if ref.Code != load.ReasonRecordShape || ref.Field != "open_amount_cents" {
			t.Fatalf("%s refused as %+v, want %s on open_amount_cents", tc.name, ref, load.ReasonRecordShape)
		}
	}
}

func TestReceivablesLoadWriterAcceptsExactCents(t *testing.T) {
	// The happy path needs the database; the acceptance here is that
	// money and shape pass the guards and the writer reaches for the
	// transaction, which a nil one answers with a panic caught as the
	// test's failure. Run one accepted record and expect exactly that:
	// the nil transaction is the proof the guards passed.
	w := NewReceivablesLoadWriter()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("an accepted record never reached the transaction: a guard refused it")
		}
	}()
	_, _ = w.Apply(context.Background(), nil, ldImportItem(125000, "2021-06-01", "2021-07-01", "AR-1", ldImportRefs("CUST-1", "BR-1")...))
}

func TestReceivablesLoadWriterRefusesBadShapes(t *testing.T) {
	w := NewReceivablesLoadWriter()
	for _, tc := range []struct {
		name  string
		write load.Write
		field string
	}{
		{"no document number", ldImportItem(1000, "2021-06-01", "2021-07-01", "", ldImportRefs("CUST-1", "BR-1")...), "source_doc_no"},
		{"no issue date", ldImportItem(1000, "", "2021-07-01", "AR-1", ldImportRefs("CUST-1", "BR-1")...), "issue_date"},
		{"an unparseable issue date", ldImportItem(1000, "June", "2021-07-01", "AR-1", ldImportRefs("CUST-1", "BR-1")...), "issue_date"},
		{"no due date", ldImportItem(1000, "2021-06-01", "", "AR-1", ldImportRefs("CUST-1", "BR-1")...), "due_date"},
		{"an impossible due date", ldImportItem(1000, "2021-06-01", "2021-02-30", "AR-1", ldImportRefs("CUST-1", "BR-1")...), "due_date"},
		{"no references", ldImportItem(1000, "2021-06-01", "2021-07-01", "AR-1"), "requires"},
		{"a customer alone", ldImportItem(1000, "2021-06-01", "2021-07-01", "AR-1", ldImportRefs("CUST-1", "BR-1")[0]), "requires"},
		{"a branch alone", ldImportItem(1000, "2021-06-01", "2021-07-01", "AR-1", ldImportRefs("CUST-1", "BR-1")[1]), "requires"},
		{"a reference with no resolved row", ldImportItem(1000, "2021-06-01", "2021-07-01", "AR-1",
			load.RefMatch{Kind: load.RefKindCustomer, LegacyID: "CUST-1"}, load.RefMatch{Kind: load.RefKindBranch, LegacyID: "BR-1", HHID: "7b220000-0000-4000-8000-0000000000b1"}), "requires"},
	} {
		_, err := w.Apply(context.Background(), nil, tc.write)
		var ref *load.Refusal
		if err == nil || !errors.As(err, &ref) {
			t.Fatalf("%s answered %v, want a refusal", tc.name, err)
		}
		if ref.Code != load.ReasonRecordShape || ref.Field != tc.field {
			t.Fatalf("%s refused as %+v, want %s on %s", tc.name, ref, load.ReasonRecordShape, tc.field)
		}
	}
}

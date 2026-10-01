package roleconfig

import (
	"encoding/json"
	"testing"
)

// The embedded seed names the planning record it was derived from, by
// file name and sha256, in its source block and in the package's
// constants; the two never drift apart.
func TestDefaultNamesItsSource(t *testing.T) {
	var doc Document
	if err := json.Unmarshal(DefaultData(), &doc); err != nil {
		t.Fatalf("the embedded default does not decode: %v", err)
	}
	if doc.Source.PlanningData != DefaultPlanningData || doc.Source.PlanningDoc != DefaultPlanningDoc {
		t.Errorf("the default's source block names %q and %q, wanted %q and %q",
			doc.Source.PlanningData, doc.Source.PlanningDoc, DefaultPlanningData, DefaultPlanningDoc)
	}
	if doc.Source.PlanningDataSHA256 != DefaultPlanningDataSHA256 {
		t.Errorf("the default's data sha256 is %q, the constant holds %q",
			doc.Source.PlanningDataSHA256, DefaultPlanningDataSHA256)
	}
	if doc.Source.PlanningDocSHA256 != DefaultPlanningDocSHA256 {
		t.Errorf("the default's narrative sha256 is %q, the constant holds %q",
			doc.Source.PlanningDocSHA256, DefaultPlanningDocSHA256)
	}
	if DefaultPlanningDataSHA256 != "8f9dc29430750629fe250dbc0b5d9ce6edf14fc8e422b40239392aec18528092" {
		t.Errorf("the carried data sha256 is %q, the record's is 8f9dc294...", DefaultPlanningDataSHA256)
	}
	if DefaultPlanningDocSHA256 != "7801e4cd398c725d14bad33e9371b938fa4cc0608dc1a6da3002d674338b6733" {
		t.Errorf("the carried narrative sha256 is %q, the record's is 7801e4cd...", DefaultPlanningDocSHA256)
	}
}

// The carried seed holds the matrix whole: 130 cells, each role and
// surface pair exactly once, at the Document level, before any load check
// runs.
func TestDefaultDataHoldsTheMatrixWhole(t *testing.T) {
	var doc Document
	if err := json.Unmarshal(DefaultData(), &doc); err != nil {
		t.Fatalf("the embedded default does not decode: %v", err)
	}
	if len(doc.Cells) != 130 {
		t.Fatalf("the carried seed holds %d cells, wanted exactly 130", len(doc.Cells))
	}
	seen := make(map[cellKey]bool, 130)
	for _, cell := range doc.Cells {
		key := cellKey{role: cell.Role, surface: cell.Surface}
		if seen[key] {
			t.Fatalf("the carried seed holds a duplicate cell for %s on %s", cell.Role, cell.Surface)
		}
		seen[key] = true
	}
	if len(seen) != 13*10 {
		t.Fatalf("the carried seed covers %d role and surface pairs, wanted 130", len(seen))
	}
	data := DefaultData()
	data[0] = '{'
	if DefaultData()[0] != '{' {
		t.Fatal("DefaultData handed out the embedded bytes themselves; a caller changed the seed")
	}
}

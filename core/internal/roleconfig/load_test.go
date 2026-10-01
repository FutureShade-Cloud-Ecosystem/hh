package roleconfig

import (
	"encoding/json"
	"errors"
	"testing"
)

// docFromDefault decodes the embedded default into a Document so a test
// can mutate one field, re-marshal and Load: every refusal test runs the
// real loader on a real document.
func docFromDefault(t *testing.T) Document {
	t.Helper()
	var doc Document
	if err := json.Unmarshal(DefaultData(), &doc); err != nil {
		t.Fatalf("the embedded default does not decode: %v", err)
	}
	return doc
}

func loadDoc(t *testing.T, doc Document) (*Config, error) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("the test document does not marshal: %v", err)
	}
	return Load(data)
}

func mustBytes(t *testing.T, doc Document) []byte {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("the test document does not marshal: %v", err)
	}
	return data
}

// refused asserts the loader refuses the document and names the wanted
// substring among the issues: a refusal is an error, and a specific one.
func refused(t *testing.T, doc Document, wantIn string) *ValidationError {
	t.Helper()
	_, err := loadDoc(t, doc)
	if err == nil {
		t.Fatalf("the loader accepted a configuration it must refuse (wanted an issue naming %q)", wantIn)
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("the refusal is a *ValidationError, got %T: %v", err, err)
	}
	for _, issue := range verr.Issues {
		if contains(issue, wantIn) {
			return verr
		}
	}
	t.Fatalf("the refusal does not name %q; issues: %v", wantIn, verr.Issues)
	return nil
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRefuseUnknownRoleInCell(t *testing.T) {
	doc := docFromDefault(t)
	doc.Cells[0].Role = "sales_rep"
	refused(t, doc, `unknown role "sales_rep"`)
}

func TestRefuseUnknownRoleInList(t *testing.T) {
	doc := docFromDefault(t)
	doc.Lists.DeliveryDateCommitters = []string{"yard_manager", "boss"}
	refused(t, doc, `unknown role "boss"`)
}

func TestRefuseUnknownSurface(t *testing.T) {
	doc := docFromDefault(t)
	doc.Cells[3].Surface = "calendar"
	refused(t, doc, `unknown surface "calendar"`)
}

func TestRefuseUnknownLevel(t *testing.T) {
	doc := docFromDefault(t)
	doc.Cells[1].Level = Level("owner")
	refused(t, doc, `unknown level "owner"`)
}

// The walls (D49, X4, X7): HR holds no money power, so every approver list
// of money refuses HR, the refund approvers and each exception kind alike.
func TestRefuseHROnApproverLists(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundApprovers] = json.RawMessage(`["sales_manager", "hr"]`)
	refused(t, doc, SettingRefundApprovers+" names hr")
	for _, kind := range ExceptionKinds() {
		mutated := docFromDefault(t)
		mutated.Lists.ExceptionApprovers[kind] = []string{"financial_controller", RoleHR}
		refused(t, mutated, "exception_approvers."+string(kind)+" names hr")
	}
}

func TestRefuseMissingCell(t *testing.T) {
	doc := docFromDefault(t)
	doc.Cells = doc.Cells[:len(doc.Cells)-1]
	refused(t, doc, "no cell for hr on dealer-admin")
}

func TestRefuseDuplicateCell(t *testing.T) {
	doc := docFromDefault(t)
	doc.Cells = append(doc.Cells, doc.Cells[0])
	refused(t, doc, "duplicate cell")
}

func TestRefuseMissingSetting(t *testing.T) {
	doc := docFromDefault(t)
	delete(doc.Settings, SettingRefundAmountWithoutApproval)
	refused(t, doc, "no "+SettingRefundAmountWithoutApproval+" setting")
}

func TestRefuseUnknownSetting(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings["refund_amount"] = json.RawMessage(`25000`)
	refused(t, doc, `unknown setting "refund_amount"`)
}

func TestRefuseMistypedSetting(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundAmountWithoutApproval] = json.RawMessage(`"250.00"`)
	refused(t, doc, "is not an integer count of cents")

	doc = docFromDefault(t)
	doc.Settings[SettingCreditAtTill] = json.RawMessage(`"yes"`)
	refused(t, doc, "is not true or false")
}

func TestRefuseNegativeRefundAmount(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundAmountWithoutApproval] = json.RawMessage(`-1`)
	refused(t, doc, "is negative")
}

func TestRefuseEmptyRoleList(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingPricingEditors] = json.RawMessage(`[]`)
	refused(t, doc, SettingPricingEditors+" names no role")
}

func TestRefuseEmptyRoleListInLists(t *testing.T) {
	doc := docFromDefault(t)
	doc.Lists.FloorClearersOwnBranch = nil
	refused(t, doc, "floor_clearers_own_branch names no role")
}

func TestRefuseMissingExceptionKind(t *testing.T) {
	doc := docFromDefault(t)
	delete(doc.Lists.ExceptionApprovers, ExceptionCreditCOD)
	refused(t, doc, `no exception_approvers.credit_cod list`)
}

func TestRefuseUnknownExceptionKind(t *testing.T) {
	doc := docFromDefault(t)
	doc.Lists.ExceptionApprovers["grand_opening"] = []string{"general_manager"}
	refused(t, doc, `unknown exception kind "grand_opening"`)
}

func TestRefuseUnparsableDocument(t *testing.T) {
	if _, err := Load([]byte(`{"cells": [`)); err == nil {
		t.Fatal("Load accepted bytes that do not parse")
	}
	if _, err := Load([]byte(`{"extra": true}`)); err == nil {
		t.Fatal("Load accepted a document with a field the format does not name")
	}
}

// Validate is the checks' entry for a document a caller holds
// unmarshalled: the same refusals Load returns after it parses, and on the
// clean document the same Config the guards read.
func TestValidateDirect(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundApprovers] = json.RawMessage(`["hr"]`)
	cfg, err := Validate(&doc)
	if err == nil || cfg != nil {
		t.Fatal("Validate accepted HR on the refund approvers list")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("the Validate refusal is a %T, wanted a *ValidationError: %v", err, err)
	}
	clean := docFromDefault(t)
	got, err := Validate(&clean)
	if err != nil {
		t.Fatalf("Validate refused the decided default: %v", err)
	}
	if level, err := got.Level("inside_sales", "counter-till"); err != nil || level != LevelWork {
		t.Errorf("the validated default reads inside_sales on counter-till as %s, %v", level, err)
	}
}

// The startup contract: the core's startup path loads its configuration
// and stops on the error. This test walks the path a startup walks: with a
// refused configuration, no Config exists to guard with, and the refusal
// is a *ValidationError the startup can report. A refused configuration
// never reaches the guards and never falls back to the default.
func TestRefusedConfigurationStopsTheCore(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundApprovers] = json.RawMessage(`["hr"]`)

	start := func(data []byte) *Config {
		cfg, err := Load(data)
		if err != nil {
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("the startup refusal is a %T, wanted a *ValidationError: %v", err, err)
			}
			return nil
		}
		return cfg
	}

	if cfg := start(mustBytes(t, doc)); cfg != nil {
		t.Fatal("the core started on a configuration the walls refuse: HR named to approve money reached the guards")
	}
	if cfg := start(DefaultData()); cfg == nil {
		t.Fatal("the core refused the decided default, which carries HR on no approver list")
	}
}

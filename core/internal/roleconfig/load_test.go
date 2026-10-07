package roleconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
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

// mutatedDefault replaces the first n occurrences of old with new in the
// embedded seed's raw bytes, for the parse probes: the loader must see the
// bytes a file would carry, not a struct a test built.
func mutatedDefault(t *testing.T, old, new string, n int) []byte {
	t.Helper()
	data := DefaultData()
	if !bytes.Contains(data, []byte(old)) {
		t.Fatalf("the seed does not hold %q", old)
	}
	return bytes.Replace(data, []byte(old), []byte(new), n)
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

// The walls' one check, walked over every approver list of money, the four
// exception kinds, the refund approvers, and the floor clearers (the
// lead's ruling: passing a below floor quote price without leadership is
// the floor exception's approval by another door): HR holds no money
// power (X4, D49), so HR names none of them.
func TestRefuseHROnEveryMoneyApproverList(t *testing.T) {
	for _, kind := range ExceptionKinds() {
		doc := docFromDefault(t)
		doc.Lists.ExceptionApprovers[kind] = []string{"financial_controller", RoleHR}
		refused(t, doc, "exception_approvers."+string(kind)+" names hr")
	}
	doc := docFromDefault(t)
	doc.Settings[SettingRefundApprovers] = json.RawMessage(`["hr"]`)
	refused(t, doc, SettingRefundApprovers+" names hr")

	doc = docFromDefault(t)
	doc.Lists.FloorClearersOwnBranch = []string{"sales_manager", RoleHR}
	refused(t, doc, "floor_clearers_own_branch names hr")
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

// A JSON null loads as the zero value with no error from the decoder, so
// it is refused by name: a null setting is no setting (the package's rule:
// a setting can never silently fall back to a zero value).
func TestRefuseNullSettings(t *testing.T) {
	for _, name := range []string{SettingCreditAtTill, SettingManagerOwnBranchFloorClearing, SettingGeneralManagerSelling, SettingRefundApprovalLimitEnabled} {
		doc := docFromDefault(t)
		doc.Settings[name] = json.RawMessage(`null`)
		refused(t, doc, name+" is not true or false")
	}
	doc := docFromDefault(t)
	doc.Settings[SettingRefundAmountWithoutApproval] = json.RawMessage(`null`)
	refused(t, doc, SettingRefundAmountWithoutApproval+" is not an integer count of cents")
}

func TestRefuseNegativeRefundAmount(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundAmountWithoutApproval] = json.RawMessage(`-1`)
	refused(t, doc, "is negative")
}

// Only the approver lists must name at least one role; an informational
// list the dealer empties with an explicit [] is the dealer's choice, and
// the folded predicates answer false.
func TestEmptyInformationalListsLoad(t *testing.T) {
	doc := docFromDefault(t)
	for _, name := range []string{SettingCostVisibility, SettingPricingEditors, SettingShortageResolvers, SettingDispatchPolicySetters} {
		doc.Settings[name] = json.RawMessage(`[]`)
	}
	doc.Lists.DeliveryDateCommitters = []string{}
	cfg, err := loadDoc(t, doc)
	if err != nil {
		t.Fatalf("the loader refused emptied informational lists: %v", err)
	}
	if ok, _ := cfg.SeesCostAndMargin("inside_sales"); ok {
		t.Error("an emptied cost visibility list still answers true")
	}
	if ok, _ := cfg.CommitsDeliveryDate("yard_manager"); ok {
		t.Error("an emptied delivery date list still commits a date")
	}
	if ok, _ := cfg.ClearsOwnBranchFloor("sales_manager"); !ok {
		t.Error("an emptied delivery date list touched the floor clearers")
	}
}

// A null informational list is not an emptied one: the decoder leaves the
// slice nil and raises no error, so null refuses by name. An explicit []
// stays the dealer's choice.
func TestRefuseNullInformationalLists(t *testing.T) {
	for _, name := range []string{SettingCostVisibility, SettingPricingEditors, SettingShortageResolvers, SettingDispatchPolicySetters} {
		doc := docFromDefault(t)
		doc.Settings[name] = json.RawMessage(`null`)
		refused(t, doc, name+" is not a list of role names")
	}
}

// The delivery date committers must be in the file: a dropped key and a
// null value both refuse, an explicit empty list is the dealer's choice.
// The probes run on the seed's own bytes, the shapes a dealer's file
// carries.
func TestRefuseDroppedOrNullDeliveryDateCommitters(t *testing.T) {
	theList := "    \"delivery_date_committers\": [\n      \"yard_manager\",\n      \"logistics_manager\",\n      \"branch_manager\"\n    ]"

	data := mutatedDefault(t, theList, "    \"delivery_date_committers\": null", 1)
	_, err := Load(data)
	if err == nil {
		t.Fatal("Load accepted a null delivery_date_committers list")
	}
	if !contains(err.Error(), "delivery_date_committers") {
		t.Errorf("the null list refusal does not name the list: %v", err)
	}

	data = mutatedDefault(t, "],\n"+theList, "]", 1)
	_, err = Load(data)
	if err == nil {
		t.Fatal("Load accepted a dropped delivery_date_committers list")
	}
	if !contains(err.Error(), "delivery_date_committers") {
		t.Errorf("the dropped list refusal does not name the list: %v", err)
	}

	doc := docFromDefault(t)
	doc.Lists.DeliveryDateCommitters = []string{}
	if _, err := loadDoc(t, doc); err != nil {
		t.Fatalf("the loader refused an explicit empty delivery date list: %v", err)
	}
}

func TestRefuseEmptyApproverLists(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundApprovers] = json.RawMessage(`[]`)
	refused(t, doc, SettingRefundApprovers+" names no role")

	doc = docFromDefault(t)
	doc.Lists.FloorClearersOwnBranch = nil
	refused(t, doc, "floor_clearers_own_branch names no role")

	doc = docFromDefault(t)
	doc.Lists.ExceptionApprovers[ExceptionCreditCOD] = nil
	refused(t, doc, "exception_approvers.credit_cod names no role")
}

func TestRefuseMissingExceptionKind(t *testing.T) {
	doc := docFromDefault(t)
	delete(doc.Lists.ExceptionApprovers, ExceptionCreditCOD)
	refused(t, doc, `no exception_approvers.credit_cod list`)
}

func TestRefuseUnknownExceptionKind(t *testing.T) {
	doc := docFromDefault(t)
	doc.Lists.ExceptionApprovers["grand_opening"] = []string{"general_manager"}
	_, err := loadDoc(t, doc)
	if err == nil {
		t.Fatal("the loader accepted an unknown exception kind")
	}
	if !contains(err.Error(), "grand_opening") {
		t.Errorf("the refusal does not name the kind: %v", err)
	}
	// Through Validate, the document path the raw key checks do not run
	// on, the kinds' own check refuses it.
	if _, err := Validate(&doc); err == nil {
		t.Fatal("Validate accepted an unknown exception kind")
	}
}

func TestRefuseUnparsableDocument(t *testing.T) {
	if _, err := Load([]byte(`{"cells": [`)); err == nil {
		t.Fatal("Load accepted bytes that do not parse")
	}
	if _, err := Load([]byte(`{"extra": true}`)); err == nil {
		t.Fatal("Load accepted a document with a field the format does not name")
	}
}

// The parse refuses what the format does not name: bytes after the
// document's one value, whatever they are.
func TestRefuseTrailingData(t *testing.T) {
	for _, probe := range []struct {
		name string
		tail string
	}{
		{"a second object", " {}"},
		{"a bare word", " garbage"},
		{"a bare number", " 7"},
	} {
		data := append(append([]byte(nil), DefaultData()...), probe.tail...)
		if _, err := Load(data); err == nil {
			t.Errorf("Load accepted trailing data (%s)", probe.name)
		}
	}
}

// The parse refuses an object naming a key twice, wherever it nests: the
// last one silently winning is how a file says one thing and its loader
// reads another.
func TestRefuseDuplicateKeys(t *testing.T) {
	data := mutatedDefault(t, `"refund_approvers":`,
		`"refund_approvers": ["hr"], "refund_approvers":`, 1)
	_, err := Load(data)
	if err == nil {
		t.Fatal("Load accepted a duplicate key inside settings")
	}
	if !contains(err.Error(), "refund_approvers") {
		t.Errorf("the duplicate key refusal does not name the key: %v", err)
	}

	data = mutatedDefault(t, `"settings": {`, `"settings": {}, "settings": {`, 1)
	_, err = Load(data)
	if err == nil {
		t.Fatal("Load accepted a duplicate key at the top level")
	}
	if !contains(err.Error(), "trailing data") && !contains(err.Error(), "twice") {
		t.Errorf("the top-level duplicate refusal names neither a duplicate nor trailing data: %v", err)
	}
}

// Every fixed key is its own, byte for byte: the decoder would match
// "CELLS" to cells and a cell's "Role" to role, so the raw keys are
// checked before anything decodes.
func TestRefuseWrongCaseKeys(t *testing.T) {
	data := mutatedDefault(t, `"cells":`, `"CELLS":`, 1)
	_, err := Load(data)
	if err == nil {
		t.Fatal("Load accepted a top-level key in the wrong case")
	}
	if !contains(err.Error(), "CELLS") {
		t.Errorf("the wrong case refusal does not name the key: %v", err)
	}

	data = mutatedDefault(t, `"role":`, `"Role":`, 1)
	if _, err := Load(data); err == nil {
		t.Fatal("Load accepted a cell key in the wrong case")
	}

	data = mutatedDefault(t, `"source":`, `"Source":`, 1)
	if _, err := Load(data); err == nil {
		t.Fatal("Load accepted a source key in the wrong case")
	}
}

// Validate refuses a nil document instead of panicking: a refusal, not a
// crash.
func TestValidateRefusesNilDocument(t *testing.T) {
	cfg, err := Validate(nil)
	if err == nil || cfg != nil {
		t.Fatal("Validate accepted a nil document")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("the nil refusal is a %T, wanted a *ValidationError: %v", err, err)
	}
}

// The lead's rulings, each a load check with a way back (the way back is
// deleting the check): the clerk never approves its own refund class, HR
// edits no price, and HR holds no level on the money surface above none.
func TestRefuseLeadRuledWalls(t *testing.T) {
	doc := docFromDefault(t)
	doc.Settings[SettingRefundApprovers] = json.RawMessage(`["inside_sales", "branch_manager"]`)
	refused(t, doc, "the clerk never approves its own refund class")

	doc = docFromDefault(t)
	doc.Settings[SettingPricingEditors] = json.RawMessage(`["hr"]`)
	refused(t, doc, "price setting is money power")

	doc = docFromDefault(t)
	for i := range doc.Cells {
		if doc.Cells[i].Role == RoleHR && doc.Cells[i].Surface == SurfaceMoneyDesk {
			doc.Cells[i].Level = LevelRead
		}
	}
	refused(t, doc, "HR holds no money power, no money settings")

	// The decided default sits on the safe side of all three: HR at none
	// on the money surface loads, as the record seeds it.
	if _, err := Default(); err != nil {
		t.Fatalf("the decided default does not load: %v", err)
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

// The refund approval window (the tenth setting, the lead's ruling after
// the reader landed): the loader refuses a value outside its bounds,
// a null, and a fraction, and the default sits inside the bounds.

func TestRefuseWindowSecondsOutsideTheBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"zero", `0`, "outside the 1 to 3600"},
		{"negative", `-5`, "outside the 1 to 3600"},
		{"above the ceiling", `3601`, "outside the 1 to 3600"},
		{"null", `null`, "is not an integer count of seconds"},
		{"fraction", `2.5`, "is not an integer count of seconds"},
		{"string", `"300"`, "is not an integer count of seconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := mutatedDefault(t, `"refund_approval_window_seconds": 300`,
				`"refund_approval_window_seconds": `+tc.raw, 1)
			_, err := Load(data)
			if err == nil {
				t.Fatalf("the loader accepted a window of %s", tc.raw)
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("the refusal is a *ValidationError, got %T: %v", err, err)
			}
			if !errors.Is(err, err) || !containsIssue(verr, tc.want) {
				t.Fatalf("the refusal must name %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestRefuseMissingWindowSeconds(t *testing.T) {
	data := mutatedDefault(t, `,
  "refund_approval_window_seconds": 300`, ``, 1)
	_, err := Load(data)
	if err == nil {
		t.Fatal("the loader accepted a document with no window setting")
	}
	if !strings.Contains(err.Error(), "no refund_approval_window_seconds setting") {
		t.Fatalf("the refusal must name the missing setting, got: %v", err)
	}
}

func containsIssue(verr *ValidationError, wantIn string) bool {
	for _, issue := range verr.Issues {
		if strings.Contains(issue, wantIn) {
			return true
		}
	}
	return false
}

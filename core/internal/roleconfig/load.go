package roleconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Load parses and validates a role configuration document. It is the entry
// the core's startup path and the tests call: on any refusal the returned
// error names every issue found and the returned Config is nil, so a
// refused configuration stops the core rather than logging past it. The
// error is a *ValidationError; callers can test for it with errors.As.
//
// The parse is as strict as the checks: a document holds one JSON value
// and nothing after it, no object names a key twice (the last one
// silently winning is how a file says one thing and its loader reads
// another), every key is one of the format's byte for byte (the decoder
// would match "CELLS" to cells), no field the format does not name, and
// then the load checks run.
func Load(data []byte) (*Config, error) {
	if err := scanDocument(data); err != nil {
		return nil, fmt.Errorf("roleconfig: the document is refused: %w", err)
	}
	if err := refuseWrongCaseKeys(data); err != nil {
		return nil, fmt.Errorf("roleconfig: the document is refused: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("roleconfig: the document does not parse: %w", err)
	}
	cfg, err := Validate(&doc)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default loads the embedded decided default (D48). The startup path calls
// this and stops on the error.
func Default() (*Config, error) {
	return Load(defaultData)
}

// Validate runs the load checks over a document already in memory: the
// same checks Load runs after it parses, the same refusal. The startup
// path and the tests call Load or Default; Validate is the entry for a
// caller holding the document unmarshalled.
func Validate(doc *Document) (*Config, error) {
	if doc == nil {
		return nil, &ValidationError{Issues: []string{"the document is nil"}}
	}
	return validate(doc)
}

// ValidationError is the refusal Load returns: every load check that
// failed, each named on its own issue line. It is fatal to the startup
// path by contract.
type ValidationError struct {
	Issues []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("roleconfig: the configuration is refused, %d issues: %s",
		len(e.Issues), strings.Join(e.Issues, "; "))
}

// scanDocument walks the raw JSON once, before any field is decoded, and
// refuses what the format does not name: bytes after the document's one
// value, and any object naming the same key twice, wherever it nests.
func scanDocument(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	type frame struct {
		object  bool
		keys    map[string]struct{}
		nextKey bool
	}
	var stack []*frame
	rootDone := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			if !rootDone {
				return fmt.Errorf("the document holds no value")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if rootDone {
			return fmt.Errorf("trailing data after the document")
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, &frame{object: true, keys: map[string]struct{}{}, nextKey: true})
			case '[':
				stack = append(stack, &frame{})
			default:
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					rootDone = true
				} else if f := stack[len(stack)-1]; f.object {
					f.nextKey = true
				}
			}
		case string:
			if len(stack) == 0 {
				rootDone = true // a bare top-level string; the decode refuses the shape
				continue
			}
			f := stack[len(stack)-1]
			if f.object && f.nextKey {
				if _, dup := f.keys[t]; dup {
					return fmt.Errorf("an object names %q twice", t)
				}
				f.keys[t] = struct{}{}
				f.nextKey = false
			} else if f.object {
				f.nextKey = true
			}
		default:
			if len(stack) == 0 {
				rootDone = true // a bare top-level number or literal
				continue
			}
			if f := stack[len(stack)-1]; f.object {
				f.nextKey = true
			}
		}
	}
}

// refuseWrongCaseKeys holds every fixed key of the format to itself, byte
// for byte: Go's decoder matches field names case insensitively, so
// without this a key in the wrong case silently lands on its field. The
// settings keys and the exception kinds are map keys and already match
// byte for byte, refused by name in the checks.
func refuseWrongCaseKeys(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	if top == nil {
		return fmt.Errorf("the document is not an object")
	}
	if err := keysWithin(top, "the document", "source", "cells", "lists", "settings"); err != nil {
		return err
	}
	if raw, ok := top["source"]; ok {
		var source map[string]json.RawMessage
		if err := json.Unmarshal(raw, &source); err != nil {
			return err
		}
		if err := keysWithin(source, "source", "planning_data", "planning_data_sha256", "planning_doc", "planning_doc_sha256"); err != nil {
			return err
		}
	}
	if raw, ok := top["lists"]; ok {
		var lists map[string]json.RawMessage
		if err := json.Unmarshal(raw, &lists); err != nil {
			return err
		}
		if err := keysWithin(lists, "lists", "exception_approvers", "floor_clearers_own_branch", "delivery_date_committers"); err != nil {
			return err
		}
		if approvers, ok := lists["exception_approvers"]; ok {
			var kinds map[string]json.RawMessage
			if err := json.Unmarshal(approvers, &kinds); err != nil {
				return err
			}
			allowed := make([]string, 0, len(exceptionKindOrder))
			for _, kind := range exceptionKindOrder {
				allowed = append(allowed, string(kind))
			}
			if err := keysWithin(kinds, "exception_approvers", allowed...); err != nil {
				return err
			}
		}
	}
	if raw, ok := top["cells"]; ok {
		var cells []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &cells); err != nil {
			return err
		}
		for i, cell := range cells {
			if err := keysWithin(cell, fmt.Sprintf("cell %d", i), "role", "surface", "level"); err != nil {
				return err
			}
		}
	}
	return nil
}

// keysWithin refuses any key of raw that is not one of the allowed names,
// byte for byte.
func keysWithin(raw map[string]json.RawMessage, where string, allowed ...string) error {
	permit := make(map[string]struct{}, len(allowed))
	for _, k := range allowed {
		permit[k] = struct{}{}
	}
	for k := range raw {
		if _, ok := permit[k]; !ok {
			return fmt.Errorf("%s names %q, which is not one of its keys byte for byte", where, k)
		}
	}
	return nil
}

// validate runs the load checks and builds the Config. The checks refuse:
// a cell or a role list naming an unknown role, surface or level; a missing
// or duplicate cell; a missing, unknown or mistyped setting, null
// included; and any approver list of money naming HR, by one general walk
// over every such list (the walls, D49 and X4: HR holds no money power).
func validate(doc *Document) (*Config, error) {
	var issues []string

	seen := make(map[cellKey]bool, len(roleVocabulary)*len(surfaceVocabulary))
	for i, cell := range doc.Cells {
		if !IsRole(cell.Role) {
			issues = append(issues, fmt.Sprintf("cell %d names unknown role %q", i, cell.Role))
			continue
		}
		if !IsSurface(cell.Surface) {
			issues = append(issues, fmt.Sprintf("cell %d names unknown surface %q", i, cell.Surface))
			continue
		}
		if _, ok := levelRank[cell.Level]; !ok {
			issues = append(issues, fmt.Sprintf("cell %d (%s on %s) names unknown level %q",
				i, cell.Role, cell.Surface, string(cell.Level)))
			continue
		}
		if cell.Role == RoleHR && cell.Surface == SurfaceMoneyDesk && cell.Level != LevelNone {
			issues = append(issues, fmt.Sprintf("the cell for %s on %s holds %s: HR holds no money power, no money settings (X4, the lead's ruling)",
				RoleHR, SurfaceMoneyDesk, cell.Level))
		}
		key := cellKey{role: cell.Role, surface: cell.Surface}
		if seen[key] {
			issues = append(issues, fmt.Sprintf("duplicate cell for %s on %s", cell.Role, cell.Surface))
			continue
		}
		seen[key] = true
	}
	for _, role := range roleVocabulary {
		for _, surface := range surfaceVocabulary {
			if !seen[cellKey{role: role, surface: surface}] {
				issues = append(issues, fmt.Sprintf("no cell for %s on %s", role, surface))
			}
		}
	}

	lists, listIssues := validateLists(doc.Lists)
	issues = append(issues, listIssues...)

	settings, settingIssues := validateSettings(doc.Settings)
	issues = append(issues, settingIssues...)

	// The walls' one check, written once so a list is covered by being on
	// this walk and nowhere else: every approver list of money refuses HR.
	// The floor clearers walk here by the lead's ruling: passing a below
	// floor quote price without leadership is the floor exception's
	// approval by another door, and HR holds no money power.
	moneyApprovers := []namedRoleList{
		{SettingRefundApprovers, settings.RefundApprovers},
		{"floor_clearers_own_branch", lists.FloorClearersOwnBranch},
	}
	for _, kind := range exceptionKindOrder {
		moneyApprovers = append(moneyApprovers, namedRoleList{approverListName(kind), lists.ExceptionApprovers[kind]})
	}
	for _, list := range moneyApprovers {
		for _, role := range list.roles {
			if role == RoleHR {
				issues = append(issues, fmt.Sprintf("%s names %s: the walls refuse it, HR holds no money power (X4, D49)",
					list.name, RoleHR))
			}
		}
	}

	if len(issues) > 0 {
		return nil, &ValidationError{Issues: issues}
	}
	cfg := &Config{
		cells:     make(map[cellKey]Level, len(doc.Cells)),
		cellOrder: append([]Cell(nil), doc.Cells...),
		lists:     lists,
		settings:  settings,
	}
	for _, cell := range doc.Cells {
		cfg.cells[cellKey{role: cell.Role, surface: cell.Surface}] = cell.Level
	}
	return cfg, nil
}

// namedRoleList pairs a list with the name the refusal message carries.
type namedRoleList struct {
	name  string
	roles []string
}

// approverListName names an exception kind's approver list, for the
// refusal message.
func approverListName(kind ExceptionKind) string {
	return "exception_approvers." + string(kind)
}

// validateLists checks the cross cutting role lists: every kind of
// exception present, every name a known role, no empty or duplicate entry,
// and the approver lists naming at least one role. An informational list
// may be empty: that is the dealer's choice.
func validateLists(lists Lists) (Lists, []string) {
	var issues []string

	approverKinds := make(map[ExceptionKind][]string, len(exceptionKindOrder))
	for _, kind := range exceptionKindOrder {
		roles, ok := lists.ExceptionApprovers[kind]
		if !ok {
			issues = append(issues, fmt.Sprintf("no %s list for exception kind %q",
				approverListName(kind), string(kind)))
			continue
		}
		clean, kindIssues := checkApproverList(roles, approverListName(kind))
		issues = append(issues, kindIssues...)
		approverKinds[kind] = clean
	}
	for kind := range lists.ExceptionApprovers {
		known := false
		for _, k := range exceptionKindOrder {
			if k == kind {
				known = true
				break
			}
		}
		if !known {
			issues = append(issues, fmt.Sprintf("unknown exception kind %q", string(kind)))
		}
	}

	floorClearers, fIssues := checkApproverList(lists.FloorClearersOwnBranch, "floor_clearers_own_branch")
	issues = append(issues, fIssues...)
	// The list must be in the file: on a nil slice the key is absent or
	// null, and both refuse. An explicit empty list is the dealer's
	// choice, the same ruling the informational settings carry; a dropped
	// or null list would silently stop every date commit.
	if lists.DeliveryDateCommitters == nil {
		issues = append(issues, fmt.Sprintf("no %s list: a dropped or null list refuses, an explicit empty list is the dealer's choice",
			"delivery_date_committers"))
	}
	dateCommitters, dIssues := checkRoleList(lists.DeliveryDateCommitters, "delivery_date_committers")
	issues = append(issues, dIssues...)

	return Lists{
		ExceptionApprovers:     approverKinds,
		FloorClearersOwnBranch: floorClearers,
		DeliveryDateCommitters: dateCommitters,
	}, issues
}

// validateSettings decodes the nine dealer settings by name. A missing
// setting, an unknown key, a value of the wrong shape (JSON null
// included), an unknown or duplicate role name, and a negative refund
// amount are all refusals.
func validateSettings(raw map[string]json.RawMessage) (Settings, []string) {
	var issues []string
	var settings Settings

	for _, key := range settingKeys {
		if _, ok := raw[key]; !ok {
			issues = append(issues, fmt.Sprintf("no %s setting", key))
		}
	}
	for key := range raw {
		known := false
		for _, k := range settingKeys {
			if k == key {
				known = true
				break
			}
		}
		if !known {
			issues = append(issues, fmt.Sprintf("unknown setting %q", key))
		}
	}
	if len(issues) > 0 {
		return settings, issues
	}

	settings.CostVisibility = decodeRoleList(raw[SettingCostVisibility], SettingCostVisibility, &issues)
	settings.CreditAtTill = decodeBool(raw[SettingCreditAtTill], SettingCreditAtTill, &issues)
	settings.RefundAmountWithoutApprovalCents = decodeCents(raw[SettingRefundAmountWithoutApproval], SettingRefundAmountWithoutApproval, &issues)
	settings.RefundApprovers = decodeApproverList(raw[SettingRefundApprovers], SettingRefundApprovers, &issues)
	settings.ManagerOwnBranchFloorClearing = decodeBool(raw[SettingManagerOwnBranchFloorClearing], SettingManagerOwnBranchFloorClearing, &issues)
	settings.PricingEditors = decodeRoleList(raw[SettingPricingEditors], SettingPricingEditors, &issues)
	settings.ShortageResolvers = decodeRoleList(raw[SettingShortageResolvers], SettingShortageResolvers, &issues)
	settings.DispatchPolicySetters = decodeRoleList(raw[SettingDispatchPolicySetters], SettingDispatchPolicySetters, &issues)
	settings.GeneralManagerSelling = decodeBool(raw[SettingGeneralManagerSelling], SettingGeneralManagerSelling, &issues)
	settings.RefundApprovalWindowSeconds = decodeWindowSeconds(raw[SettingRefundApprovalWindowSeconds], SettingRefundApprovalWindowSeconds, &issues)

	// The clerk never approves its own refund class: inside_sales rings
	// the refund, so naming it on the refund approvers empties the rule
	// (the lead's ruling). The way back, if a later ruling allows it, is
	// deleting this loop.
	for _, role := range settings.RefundApprovers {
		if role == RoleInsideSales {
			issues = append(issues, fmt.Sprintf("%s names %s: the clerk never approves its own refund class (the lead's ruling)",
				SettingRefundApprovers, RoleInsideSales))
		}
	}
	// HR edits no price: price setting is money power (the lead's ruling).
	// The way back, if a later ruling allows it, is deleting this loop.
	for _, role := range settings.PricingEditors {
		if role == RoleHR {
			issues = append(issues, fmt.Sprintf("%s names %s: price setting is money power, and HR holds none (the lead's ruling)",
				SettingPricingEditors, RoleHR))
		}
	}

	return settings, issues
}

// checkRoleList holds the rules every role list obeys: no empty name, no
// unknown role, no duplicate. An empty list is allowed here; only the
// approver lists must name at least one role (checkApproverList). It
// returns a copied list for the Config so no caller can reach into the
// document.
func checkRoleList(roles []string, name string) ([]string, []string) {
	var issues []string
	seen := make(map[string]bool, len(roles))
	clean := make([]string, 0, len(roles))
	for _, r := range roles {
		switch {
		case r == "":
			issues = append(issues, fmt.Sprintf("%s holds an empty name", name))
		case !IsRole(r):
			issues = append(issues, fmt.Sprintf("%s names unknown role %q", name, r))
		case seen[r]:
			issues = append(issues, fmt.Sprintf("%s names %s twice", name, r))
		default:
			seen[r] = true
			clean = append(clean, r)
		}
	}
	return clean, issues
}

// checkApproverList adds the approver lists' one extra rule: a list that
// can approve or clear money names at least one role, or the approval has
// nobody to hold.
func checkApproverList(roles []string, name string) ([]string, []string) {
	clean, issues := checkRoleList(roles, name)
	if len(roles) == 0 {
		issues = append(issues, fmt.Sprintf("%s names no role", name))
	}
	return clean, issues
}

// isJSONNull reports whether the raw value is JSON null.
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func decodeBool(raw json.RawMessage, name string, issues *[]string) bool {
	var v bool
	// JSON null leaves v at its zero value and no error, so it is refused
	// by name: a null setting is no setting.
	if err := json.Unmarshal(raw, &v); err != nil || isJSONNull(raw) {
		*issues = append(*issues, fmt.Sprintf("%s is not true or false", name))
	}
	return v
}

// decodeCents decodes an integer count of cents. Money is integer cents,
// never a float; a fractional, negative or null amount is refused.
func decodeCents(raw json.RawMessage, name string, issues *[]string) int64 {
	var v int64
	// JSON null leaves v at zero and no error, so it is refused by name.
	if err := json.Unmarshal(raw, &v); err != nil || isJSONNull(raw) {
		*issues = append(*issues, fmt.Sprintf("%s is not an integer count of cents", name))
		return 0
	}
	if v < 0 {
		*issues = append(*issues, fmt.Sprintf("%s is negative", name))
	}
	return v
}

// decodeWindowSeconds decodes the refund approval window in seconds: an
// integer inside the package's bounds. A null, a fraction, a value below
// the floor or above the ceiling is refused: a window of zero would refuse
// every approval and an unbounded one would let any live token stand in
// for a manager at the counter.
func decodeWindowSeconds(raw json.RawMessage, name string, issues *[]string) int {
	var v int
	// JSON null leaves v at zero and no error, so it is refused by name.
	if err := json.Unmarshal(raw, &v); err != nil || isJSONNull(raw) {
		*issues = append(*issues, fmt.Sprintf("%s is not an integer count of seconds", name))
		return 0
	}
	if v < MinRefundApprovalWindowSeconds || v > MaxRefundApprovalWindowSeconds {
		*issues = append(*issues, fmt.Sprintf("%s holds %d, outside the %d to %d the window bounds",
			name, v, MinRefundApprovalWindowSeconds, MaxRefundApprovalWindowSeconds))
	}
	return v
}

// decodeRoleList decodes a JSON array of role names through checkRoleList.
// An empty array is the dealer's choice on the informational lists; a null
// is not a choice, it is a dropped list, and it refuses.
func decodeRoleList(raw json.RawMessage, name string, issues *[]string) []string {
	var roles []string
	// JSON null leaves the slice nil and raises no error, so it is refused
	// by name.
	if err := json.Unmarshal(raw, &roles); err != nil || isJSONNull(raw) {
		*issues = append(*issues, fmt.Sprintf("%s is not a list of role names", name))
		return nil
	}
	clean, listIssues := checkRoleList(roles, name)
	*issues = append(*issues, listIssues...)
	return clean
}

// decodeApproverList decodes a JSON array of role names through
// checkApproverList: an approver list of money names at least one role.
func decodeApproverList(raw json.RawMessage, name string, issues *[]string) []string {
	var roles []string
	if err := json.Unmarshal(raw, &roles); err != nil {
		*issues = append(*issues, fmt.Sprintf("%s is not a list of role names", name))
		return nil
	}
	clean, listIssues := checkApproverList(roles, name)
	*issues = append(*issues, listIssues...)
	return clean
}

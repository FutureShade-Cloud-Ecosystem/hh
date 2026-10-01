package roleconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Load parses and validates a role configuration document. It is the entry
// the core's startup path and the tests call: on any refusal the returned
// error names every issue found and the returned Config is nil, so a
// refused configuration stops the core rather than logging past it. The
// error is a *ValidationError; callers can test for it with errors.As.
func Load(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("roleconfig: the document does not parse: %w", err)
	}
	return validate(&doc)
}

// Default loads the embedded decided default (D48). The startup path calls
// this and stops on the error.
func Default() (*Config, error) {
	return Load(defaultData)
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

// validate runs the load checks and builds the Config. The checks refuse:
// a cell or a role list naming an unknown role, surface or level; a missing
// or duplicate cell; a missing, unknown or mistyped setting; and any
// approver list of money naming HR (the walls, D49 and X4: HR holds no
// money power, so the check runs over every approver list generally, not
// over the refund approvers alone).
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

// approverListNames the walls' approver lists, for the refusal message.
func approverListName(kind ExceptionKind) string {
	return "exception_approvers." + string(kind)
}

// validateLists checks the cross cutting role lists: every kind of
// exception present, every name a known role, no empty or duplicate entry,
// and HR on no approver list of money.
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
		clean, kindIssues := checkRoleList(roles, approverListName(kind))
		for _, name := range clean {
			if name == RoleHR {
				issues = append(issues, fmt.Sprintf("%s names %s: the walls refuse it, HR holds no money power (X4, D49)",
					approverListName(kind), RoleHR))
			}
		}
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

	floorClearers, fIssues := checkRoleList(lists.FloorClearersOwnBranch, "floor_clearers_own_branch")
	issues = append(issues, fIssues...)
	dateCommitters, dIssues := checkRoleList(lists.DeliveryDateCommitters, "delivery_date_committers")
	issues = append(issues, dIssues...)

	return Lists{
		ExceptionApprovers:     approverKinds,
		FloorClearersOwnBranch: floorClearers,
		DeliveryDateCommitters: dateCommitters,
	}, issues
}

// validateSettings decodes the nine dealer settings by name. A missing
// setting, an unknown key, a value of the wrong shape, an unknown or
// duplicate role name, and a negative refund amount are all refusals. The
// refund approvers are an approver list of money: HR is refused there too.
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
	settings.RefundApprovers = decodeRoleList(raw[SettingRefundApprovers], SettingRefundApprovers, &issues)
	for _, name := range settings.RefundApprovers {
		if name == RoleHR {
			issues = append(issues, fmt.Sprintf("%s names %s: the walls refuse it, HR holds no money power (X7, D49)",
				SettingRefundApprovers, RoleHR))
		}
	}
	settings.ManagerOwnBranchFloorClearing = decodeBool(raw[SettingManagerOwnBranchFloorClearing], SettingManagerOwnBranchFloorClearing, &issues)
	settings.PricingEditors = decodeRoleList(raw[SettingPricingEditors], SettingPricingEditors, &issues)
	settings.ShortageResolvers = decodeRoleList(raw[SettingShortageResolvers], SettingShortageResolvers, &issues)
	settings.DispatchPolicySetters = decodeRoleList(raw[SettingDispatchPolicySetters], SettingDispatchPolicySetters, &issues)
	settings.GeneralManagerSelling = decodeBool(raw[SettingGeneralManagerSelling], SettingGeneralManagerSelling, &issues)

	return settings, issues
}

// checkRoleList holds the rules every role list obeys: no empty list, no
// empty name, no unknown role, no duplicate. It returns a copied list for
// the Config so no caller can reach into the document.
func checkRoleList(roles []string, name string) ([]string, []string) {
	var issues []string
	if len(roles) == 0 {
		issues = append(issues, fmt.Sprintf("%s names no role", name))
	}
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

func decodeBool(raw json.RawMessage, name string, issues *[]string) bool {
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		*issues = append(*issues, fmt.Sprintf("%s is not true or false", name))
	}
	return v
}

// decodeCents decodes an integer count of cents. Money is integer cents,
// never a float; a fractional or negative amount is refused.
func decodeCents(raw json.RawMessage, name string, issues *[]string) int64 {
	var v int64
	if err := json.Unmarshal(raw, &v); err != nil {
		*issues = append(*issues, fmt.Sprintf("%s is not an integer count of cents", name))
		return 0
	}
	if v < 0 {
		*issues = append(*issues, fmt.Sprintf("%s is negative", name))
	}
	return v
}

// decodeRoleList decodes a JSON array of role names through checkRoleList.
func decodeRoleList(raw json.RawMessage, name string, issues *[]string) []string {
	var roles []string
	if err := json.Unmarshal(raw, &roles); err != nil {
		*issues = append(*issues, fmt.Sprintf("%s is not a list of role names", name))
		return nil
	}
	clean, listIssues := checkRoleList(roles, name)
	*issues = append(*issues, listIssues...)
	return clean
}

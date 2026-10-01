// Package roleconfig holds the dealer's role configuration the core's
// guards read (D48): each staff role's level on each surface, the cross
// cutting role lists the rules name, and the nine dealer settings. It is
// plain, framework free Go: no UI, no framework, no database, no schema.
//
// The decided default (ROLE-ACCESS-r3 and its data role-access-r3.json)
// ships embedded in this package as the seed of record; per dealer
// overrides are later work. Under D49 the hard walls are built into the
// product and cannot be switched off: what sits inside them is the dealer's
// configuration, and the loader's checks refuse a configuration that would
// break a wall, an approver of money naming HR for example.
//
// The startup contract: the core's startup path loads its configuration
// through Default (or Load) and stops on the error. A refused configuration
// never reaches the guards as a zero value and never falls back to the
// default behind the refusal's back.
package roleconfig

import "encoding/json"

// Level is one cell's access level, the five the record names
// (ROLE-ACCESS-r3, "Levels, marks and rows").
type Level string

const (
	LevelNone   Level = "none"
	LevelRead   Level = "read"
	LevelWork   Level = "work"
	LevelManage Level = "manage"
	LevelAdmin  Level = "admin"
)

// levelRank orders the levels so a guard can ask whether a role's level
// reaches a route's level. A name outside the five is in the map by no
// door: AtLeast and Holds refuse it, never rank it.
var levelRank = map[Level]int{
	LevelNone:   0,
	LevelRead:   1,
	LevelWork:   2,
	LevelManage: 3,
	LevelAdmin:  4,
}

// AtLeast reports whether the level reaches want. An unknown level on
// either side reaches nothing: a typo or an empty name answers false,
// never true.
func (l Level) AtLeast(want Level) bool {
	lr, ok := levelRank[l]
	if !ok {
		return false
	}
	wr, ok := levelRank[want]
	if !ok {
		return false
	}
	return lr >= wr
}

// ExceptionKind names a kind of exception whose approvers the
// configuration holds (rule X4, and the refund rule X7).
type ExceptionKind string

const (
	// ExceptionFloorQuote is a quote price below the margin floor, the
	// quote flow only (row 481: the till has no floor).
	ExceptionFloorQuote ExceptionKind = "floor"
	// ExceptionJournal is a journal over the threshold.
	ExceptionJournal ExceptionKind = "journal"
	// ExceptionFreight is freight above the quote.
	ExceptionFreight ExceptionKind = "freight"
	// ExceptionCreditCOD is a credit or COD exception.
	ExceptionCreditCOD ExceptionKind = "credit_cod"
)

// exceptionKindOrder is the stable order of the four kinds.
var exceptionKindOrder = []ExceptionKind{
	ExceptionFloorQuote,
	ExceptionJournal,
	ExceptionFreight,
	ExceptionCreditCOD,
}

// ExceptionKinds returns the four exception kinds in a stable order. The
// slice is a copy; the caller cannot change the vocabulary.
func ExceptionKinds() []ExceptionKind {
	return append([]ExceptionKind(nil), exceptionKindOrder...)
}

// The configuration's vocabulary: the catalog's thirteen staff roles and
// the ten internal surfaces (ROLE-ACCESS-r3; docs/analysis/role-catalog.yaml).
// The vocabulary is the product's shape and lives in code; each cell's
// level is the dealer's data inside it.
var roleVocabulary = []string{
	"inside_sales",
	"sales_manager",
	"outside_sales",
	"yard_team",
	"yard_manager",
	"logistics_manager",
	"driver",
	"branch_manager",
	"procurement_manager",
	"payables_receivables",
	"financial_controller",
	"general_manager",
	"hr",
}

var surfaceVocabulary = []string{
	"counter-till",
	"quote-desk",
	"yard-pick",
	"drive-run",
	"dispatch-board",
	"buying-and-stock",
	"money-desk",
	"manager-home",
	"house-messages",
	"dealer-admin",
}

// Roles returns the thirteen role ids of the vocabulary. The slice is a
// copy; the caller cannot change the vocabulary.
func Roles() []string {
	return append([]string(nil), roleVocabulary...)
}

// Surfaces returns the ten surface ids of the vocabulary. The slice is a
// copy; the caller cannot change the vocabulary.
func Surfaces() []string {
	return append([]string(nil), surfaceVocabulary...)
}

// IsRole reports whether s names a role of the vocabulary.
func IsRole(s string) bool {
	for _, r := range roleVocabulary {
		if r == s {
			return true
		}
	}
	return false
}

// IsSurface reports whether s names a surface of the vocabulary.
func IsSurface(s string) bool {
	for _, v := range surfaceVocabulary {
		if v == s {
			return true
		}
	}
	return false
}

// RoleHR is HR's role id. The walls refuse it on every approver list of
// money (D49's example: the configuration's checks refuse a setting that
// would break a wall; X4: HR holds no money power).
const RoleHR = "hr"

// RoleInsideSales is the counter clerk's role id: the clerk rings a
// refund, so the walls refuse it on the refund approvers list (the lead's
// ruling: the clerk never approves its own refund class).
const RoleInsideSales = "inside_sales"

// RoleGeneralManager is the General Manager's role id: general_manager_selling,
// when a dealer turns it on, adds this role to the delivery date committers.
const RoleGeneralManager = "general_manager"

// SurfaceMoneyDesk is the money surface's id. The walls refuse HR any
// level on it above none (X4: no money settings).
const SurfaceMoneyDesk = "money-desk"

// The JSON keys of the nine dealer settings. A misspelled key in a
// configuration is refused, not ignored, so a setting can never silently
// fall back to a zero value.
const (
	SettingCostVisibility                = "cost_visibility"
	SettingCreditAtTill                  = "credit_at_till"
	SettingRefundAmountWithoutApproval   = "refund_amount_without_approval_cents"
	SettingRefundApprovers               = "refund_approvers"
	SettingManagerOwnBranchFloorClearing = "manager_own_branch_floor_clearing"
	SettingPricingEditors                = "pricing_editors"
	SettingShortageResolvers             = "shortage_resolvers"
	SettingDispatchPolicySetters         = "dispatch_policy_setters"
	SettingGeneralManagerSelling         = "general_manager_selling"
)

// settingKeys lists the nine keys. Load refuses a document where one is
// missing or one beyond these nine appears.
var settingKeys = []string{
	SettingCostVisibility,
	SettingCreditAtTill,
	SettingRefundAmountWithoutApproval,
	SettingRefundApprovers,
	SettingManagerOwnBranchFloorClearing,
	SettingPricingEditors,
	SettingShortageResolvers,
	SettingDispatchPolicySetters,
	SettingGeneralManagerSelling,
}

// Source names the planning record a configuration was derived from. The
// default's Source names role-access-r3.json and ROLE-ACCESS-r3.md by
// sha256; a dealer override file may omit it.
type Source struct {
	PlanningData       string `json:"planning_data,omitempty"`
	PlanningDataSHA256 string `json:"planning_data_sha256,omitempty"`
	PlanningDoc        string `json:"planning_doc,omitempty"`
	PlanningDocSHA256  string `json:"planning_doc_sha256,omitempty"`
}

// Cell is one role's level on one surface.
type Cell struct {
	Role    string `json:"role"`
	Surface string `json:"surface"`
	Level   Level  `json:"level"`
}

// Lists holds the cross cutting role lists the rules name: who approves
// each kind of exception (X4, and X7's refund approvers travel as a
// setting), who clears the floor on its own quote price in its own branch
// (X5), who commits a delivery date (X11: the Yard Team commits none).
type Lists struct {
	ExceptionApprovers     map[ExceptionKind][]string `json:"exception_approvers"`
	FloorClearersOwnBranch []string                   `json:"floor_clearers_own_branch"`
	DeliveryDateCommitters []string                   `json:"delivery_date_committers"`
}

// Document is the JSON document of a role configuration: the format of a
// dealer's configuration file, of the embedded seed, and so Load's input.
// Settings stay raw here so the loader can refuse a missing or unknown
// setting by name instead of decoding a zero value past it.
type Document struct {
	Source   Source                     `json:"source,omitempty"`
	Cells    []Cell                     `json:"cells"`
	Lists    Lists                      `json:"lists"`
	Settings map[string]json.RawMessage `json:"settings"`
}

// Settings are the nine dealer settings the rulings name, decoded and
// typed. Money is integer cents (the refund amount 250.00 is held as
// 25000), never a float.
type Settings struct {
	// CostVisibility names who sees cost and margin (X3).
	CostVisibility []string
	// CreditAtTill reports whether the counter clerk sees the customer's
	// balance, credit limit and headroom at the till (row 353).
	CreditAtTill bool
	// RefundAmountWithoutApprovalCents is the refund amount without
	// approval, in integer cents.
	RefundAmountWithoutApprovalCents int64
	// RefundApprovers names who approves a refund above the amount, of the
	// sale's branch (X7).
	RefundApprovers []string
	// ManagerOwnBranchFloorClearing reports whether a manager's own below
	// floor quote price goes through in its own branch, logged (X5, the
	// quote flow only).
	ManagerOwnBranchFloorClearing bool
	// PricingEditors names who edits prices (X6).
	PricingEditors []string
	// ShortageResolvers names who resolves a shortage (X11).
	ShortageResolvers []string
	// DispatchPolicySetters names who sets the dispatch policy (X13).
	DispatchPolicySetters []string
	// GeneralManagerSelling reports whether the General Manager sells and
	// commits dates (row 457: off, Read on the till, quotes and dispatch).
	GeneralManagerSelling bool
}

// cellKey indexes one cell.
type cellKey struct {
	role    string
	surface string
}

// Config is a loaded, validated role configuration: what the guards read.
// Build one only through Load or Default; a Config the load checks refused
// does not exist.
type Config struct {
	cells     map[cellKey]Level
	cellOrder []Cell
	lists     Lists
	settings  Settings
}

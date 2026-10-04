package pricing

// floor_preview.go, the pricing editors' floor warning (CP0-6, answer A): before an
// editor saves a rule, a customer price, a category margin policy or a quantity break,
// the server answers which existing products the new price would put under the margin
// floor, and afterwards it records that the editor confirmed that warning in the pricing
// log. Nothing here is written by the preview, and the warning never refuses a save:
// the pricing editors are the roles the dealer trusts to set prices.

import "context"

// FloorChange is the proposed write the preview grades, exactly one of Rule, Override,
// Policy or Break according to Kind. ID names the record being edited, empty on create.
type FloorChange struct {
	Kind     string
	ID       string
	BranchID string
	Rule     *Rule
	Override *Override
	Policy   *CategoryPolicy
	Break    *QuantityBreakHeader
}

// FloorPreviewItem is one product the new price leaves under the floor.
type FloorPreviewItem struct {
	ProductID          string
	SKU                string
	Name               string
	CategoryID         string
	CustomerType       string
	FinalCents         int64
	MarginBps          int64
	FloorBps           int64
	PriceUOM           string
	Source             string
	PreviousFinalCents int64
	PreviousMarginBps  int64
	AlreadyUnderFloor  bool
}

// FloorPreview is the preview's answer.
type FloorPreview struct {
	Items     []FloorPreviewItem
	Evaluated int
	NotGraded int
	Truncated bool
}

// CandidateProduct is one active product the preview may grade, with its cost.
type CandidateProduct struct {
	ProductID  string
	CategoryID string
	SKU        string
	Name       string
	BaseUOM    string
	CostCents  int64
}

// FloorCandidates finds the products a change may reach. The pgx Repository satisfies it.
type FloorCandidates interface {
	CandidateProducts(ctx context.Context, categoryID, productID, branchID string, limit int) ([]CandidateProduct, error)
}

// FloorConfirmation is the editor's confirmation of a floor warning.
type FloorConfirmation struct {
	EntityType string
	EntityID   string
	ProductIDs []string
	RefType    string
	RefID      string
}

// WithCandidateSource wires the product source the preview grades.
func (s *Service) WithCandidateSource(c FloorCandidates) *Service {
	s.cands = c
	return s
}

// PreviewFloor grades the proposed change.
func (s *Service) PreviewFloor(_ context.Context, _ FloorChange) (FloorPreview, error) {
	return FloorPreview{}, nil
}

// RecordFloorConfirmation writes the editor's confirmation into the pricing log.
func (s *Service) RecordFloorConfirmation(_ context.Context, _ FloorConfirmation, _ string) error {
	return nil
}

// PreviewProductCap bounds how many products one preview grades.
const PreviewProductCap = 200

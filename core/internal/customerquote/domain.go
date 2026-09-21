package customerquote

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The carried HH Pro domain rules, one for one where the frozen brief's
// acceptance demands it. Citations point at the HH Pro reference
// (hh_pro_dibbits/src/core). Money is integer cents computed here, never
// client-side (the house money invariant); quantities are fixed-precision
// decimals carried as strings and converted through the decimal at this
// boundary, never by float multiplication.

// Status is the customer-quote status set (R1: the seven statuses of the HH
// Pro lifecycle, exactly the frozen CHECK set).
type Status string

const (
	StatusDraft            Status = "draft"
	StatusSent             Status = "sent"
	StatusViewed           Status = "viewed"
	StatusChangesRequested Status = "changes-requested"
	StatusAccepted         Status = "accepted"
	StatusDeclined         Status = "declined"
	StatusExpired          Status = "expired"
)

// Presentation is the selections-versus-commodities split (R9): selections
// carry imagery and narrative, commodities carry a bare line. The split is
// presentation only; it never touches price or authority.
const (
	PresentationSelection = "selection"
	PresentationCommodity = "commodity"
)

// PriceSource follows the frozen plan_line vocabulary: a line priced by the
// dealer carries erp, quoted or estimate; unpriced carries unpriced (R2's
// never-priced case).
const (
	PriceSourceERP      = "erp"
	PriceSourceQuoted   = "quoted"
	PriceSourceEstimate = "estimate"
	PriceSourceUnpriced = "unpriced"
)

// SpecialOrderPrefix is the legacy SO- sku encoding, read for traceability and
// never written as the meaning (R10: the boolean plus description plus
// optional supplier reference are the schema's special-order predicate, D16;
// the quote-desk gate itself is the stage machine's, outside this module).
const SpecialOrderPrefix = "SO-"

// IsSpecialOrderSku reads the legacy encoding (R10, the reference predicate).
func IsSpecialOrderSku(sku string) bool {
	return strings.HasPrefix(sku, SpecialOrderPrefix)
}

// Line is one frozen presentation line (R4: the lines freeze at send, and the
// frozen payload is what the homeowner sees; R9: the presentation fields ride
// for selections). UnitCostCents is what the contractor pays; it is never
// shown to the customer (the sell-side rule: the supplier's name appears
// nowhere on the document, and the contractor's cost is not their customer's
// business).
type Line struct {
	ID                      string     `json:"id"`
	Name                    string     `json:"name"`
	Qty                     string     `json:"qty"` // fixed-point decimal string
	UOM                     string     `json:"uom"`
	UnitCostCents           int64      `json:"unit_cost_cents"`
	PriceSource             string     `json:"price_source"`
	PriceValidUntil         *time.Time `json:"price_valid_until,omitempty"`
	Presentation            string     `json:"presentation"`
	SKU                     string     `json:"sku,omitempty"`
	Description             string     `json:"description,omitempty"`
	ImageURL                string     `json:"image_url,omitempty"`
	Note                    string     `json:"note,omitempty"`
	IsSpecialOrder          bool       `json:"is_special_order"`
	SpecialOrderDescription string     `json:"special_order_description,omitempty"`
	SupplierRef             string     `json:"supplier_ref,omitempty"`
}

// LabourLine is one labour line (R7): flat rate, or hourly rate times hours.
type LabourLine struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	RateType    string `json:"rate_type"` // flat | hourly
	RateCents   int64  `json:"rate_cents"`
	Hours       string `json:"hours,omitempty"` // fixed-point decimal string
}

// OverheadLine is one overhead line (R7): a flat amount in cents, or a
// percentage carried in basis points (the HH Pro surface's percent converts to
// basis points at this boundary; the value field's meaning is selected by
// amount_type).
type OverheadLine struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	AmountType string `json:"amount_type"` // flat | percent
	Value      int64  `json:"value"`       // cents when flat, basis points when percent
}

// IsLinePriced is the R2 predicate, carried from the reference's isPriced
// (src/core/domain/project.ts): never-priced (price_source unpriced) and
// priced-and-lapsed both count as unpriced. A proposal with an unpriced line
// is a promise the contractor would have to eat; the unitCost null case
// refuses rather than defaulting to zero.
func IsLinePriced(line Line, now time.Time) bool {
	if line.PriceSource == PriceSourceUnpriced {
		return false
	}
	if line.PriceValidUntil != nil && !now.Before(*line.PriceValidUntil) {
		return false
	}
	return true
}

// UnpricedLineNames returns the names of the lines the dealer is not currently
// standing behind, for the R2 refusal sentence.
func UnpricedLineNames(lines []Line, now time.Time) []string {
	var names []string
	for _, l := range lines {
		if !IsLinePriced(l, now) {
			names = append(names, l.Name)
		}
	}
	return names
}

// SupplierPriceHorizon is the earliest supplier price expiry across the
// lines (R3: the cap on how long the contractor may hold the offer open;
// nil means every line is dealer-priced with no expiry).
func SupplierPriceHorizon(lines []Line) *time.Time {
	var earliest *time.Time
	for _, l := range lines {
		if l.PriceValidUntil == nil {
			continue
		}
		if earliest == nil || l.PriceValidUntil.Before(*earliest) {
			t := *l.PriceValidUntil
			earliest = &t
		}
	}
	return earliest
}

// CapValidUntil applies the R3 cap at build and update: never promise the
// homeowner longer than the dealer is holding the price (the reference's
// maxValidUntil rule, applied wherever valid_until is set).
func CapValidUntil(requested time.Time, horizon *time.Time) time.Time {
	if horizon != nil && horizon.Before(requested) {
		return *horizon
	}
	return requested
}

// IsOfferExpired is the offer bound (R one of the two time bounds): the offer
// lapses at valid_until (the reference's isQuoteExpired).
func IsOfferExpired(validUntil *time.Time, now time.Time) bool {
	if validUntil == nil {
		return false
	}
	return !now.Before(*validUntil)
}

// CanRespond names the statuses a customer can act on (R8's gate, the
// reference's canRespond): a live offer in sent, viewed or
// changes-requested; the change-request recording admits the same set, so the
// offer remains respondable after a change request.
func CanRespond(status Status, validUntil *time.Time, now time.Time) bool {
	if IsOfferExpired(validUntil, now) {
		return false
	}
	switch status {
	case StatusSent, StatusViewed, StatusChangesRequested:
		return true
	default:
		return false
	}
}

// The money arithmetic, carried from the reference's money helpers
// (src/core/lib/money.ts): multiply cents by a possibly fractional quantity
// with rounding at the boundary, and apply a percentage carried in basis
// points. All quantities parse through the decimal string at scale 10^4 (the
// schema's own DECIMAL(19,4) grain); every intermediate is integer.

const qtyScale = 10000

// parseQtyScaled parses a fixed-point decimal string into an integer scaled by
// qtyScale. It refuses negatives, exponents and any shape the decimal string
// of a quantity cannot honestly carry.
func parseQtyScaled(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("customerquote: empty quantity")
	}
	if strings.ContainsAny(s, "+-eE ") {
		return 0, fmt.Errorf("customerquote: quantity %q is not a plain decimal", s)
	}
	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if strings.Contains(fracPart, ".") {
		return 0, fmt.Errorf("customerquote: quantity %q is not a plain decimal", s)
	}
	if intPart == "" && !hasFrac {
		return 0, fmt.Errorf("customerquote: quantity %q is not a decimal", s)
	}
	if hasFrac && len(fracPart) > 4 {
		return 0, fmt.Errorf("customerquote: quantity %q carries more than four decimal places", s)
	}
	allDigits := func(p string) bool {
		if p == "" {
			return true
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
		return true
	}
	if !allDigits(intPart) || !allDigits(fracPart) {
		return 0, fmt.Errorf("customerquote: quantity %q is not a decimal", s)
	}
	total := parseInt(intPart) * qtyScale
	if hasFrac {
		total += parseInt(padRight(fracPart, 4))
	}
	return total, nil
}

func parseInt(s string) int64 {
	var v int64
	for i := 0; i < len(s); i++ {
		v = v*10 + int64(s[i]-'0')
	}
	return v
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += "0"
	}
	return s
}

// roundHalfUp rounds a/b for non-negative a, half away from zero, matching the
// reference's Math.round on positive money.
func roundHalfUp(a, b int64) int64 {
	if a >= 0 {
		return (a + b/2) / b
	}
	return -((-a + b/2) / b)
}

// MultiplyCents is the carried multiplyCents: cents times a decimal quantity,
// rounded.
func MultiplyCents(cents int64, qty string) (int64, error) {
	q, err := parseQtyScaled(qty)
	if err != nil {
		return 0, err
	}
	return roundHalfUp(cents*q, qtyScale), nil
}

// ApplyBPS is the carried applyPercent with the percentage in basis points:
// cents plus-or-minus the fraction bps/10000, rounded.
func ApplyBPS(cents int64, bps int64) int64 {
	return roundHalfUp(cents*bps, 10000)
}

// Totals is the computed read model (R7), ports computeQuoteTotals one for
// one: material cost from frozen lines with per-line rounding, markup from
// markup basis points, labour at flat or hourly, overhead flat or percentage
// on the marked-up materials plus labour base, the grand total, and margin for
// the tenant's own view only (never the public payload).
type Totals struct {
	MaterialCostCents int64 `json:"material_cost_cents"`
	MarkupCents       int64 `json:"markup_cents"`
	MaterialsCents    int64 `json:"materials_cents"`
	LabourCents       int64 `json:"labour_cents"`
	OverheadCents     int64 `json:"overhead_cents"`
	GrandCents        int64 `json:"grand_cents"`
	MarginCents       int64 `json:"margin_cents"`
	MarginPercent     int64 `json:"margin_percent"`
}

// LabourTotal is one labour line's total (flat rate, or hourly rate times
// hours with rounding).
func LabourTotal(line LabourLine) (int64, error) {
	if line.RateType == "flat" {
		return line.RateCents, nil
	}
	if line.RateType != "hourly" {
		return 0, fmt.Errorf("customerquote: labour rate type %q is neither flat nor hourly", line.RateType)
	}
	h, err := parseQtyScaled(line.Hours)
	if err != nil {
		return 0, fmt.Errorf("customerquote: labour hours: %w", err)
	}
	return roundHalfUp(line.RateCents*h, qtyScale), nil
}

// ComputeTotals ports computeQuoteTotals (R7). Money is integer cents
// computed here; the percent surface converts at the boundary only.
func ComputeTotals(lines []Line, markupBps int64, labour []LabourLine, overhead []OverheadLine) (Totals, error) {
	var materialCost int64
	for _, l := range lines {
		lineTotal, err := MultiplyCents(l.UnitCostCents, l.Qty)
		if err != nil {
			return Totals{}, fmt.Errorf("customerquote: line %q: %w", l.Name, err)
		}
		materialCost += lineTotal
	}
	markup := ApplyBPS(materialCost, markupBps)
	materials := materialCost + markup
	var labourTotal int64
	for _, l := range labour {
		v, err := LabourTotal(l)
		if err != nil {
			return Totals{}, err
		}
		labourTotal += v
	}
	// Percentage overheads are taken on marked-up materials plus labour,
	// which is how a contractor actually computes permits, disposal and
	// general conditions.
	overheadBase := materials + labourTotal
	var overheadTotal int64
	for _, o := range overhead {
		switch o.AmountType {
		case "flat":
			overheadTotal += o.Value
		case "percent":
			overheadTotal += ApplyBPS(overheadBase, o.Value)
		default:
			return Totals{}, fmt.Errorf("customerquote: overhead amount type %q is neither flat nor percent", o.AmountType)
		}
	}
	grand := materials + labourTotal + overheadTotal
	margin := grand - materialCost
	marginPct := int64(0)
	if grand > 0 {
		marginPct = roundHalfUp(margin*100, grand)
	}
	return Totals{
		MaterialCostCents: materialCost,
		MarkupCents:       markup,
		MaterialsCents:    materials,
		LabourCents:       labourTotal,
		OverheadCents:     overheadTotal,
		GrandCents:        grand,
		MarginCents:       margin,
		MarginPercent:     marginPct,
	}, nil
}

// LineSellUnitCents is the customer-facing per-line sell price: the
// contractor's unit cost with the markup applied per line (the reference's
// applyPercent on the unit cost). The document's group totals come from
// ComputeTotals over the aggregate, exactly as the reference's page rendered
// them; the per-line price is presentation, and hide_line_prices suppresses it.
func LineSellUnitCents(line Line, markupBps int64) int64 {
	return ApplyBPS(line.UnitCostCents, 10000+markupBps)
}

// ValidateLineSet applies the shape rules every stored line set must hold:
// non-empty (the reference's no-lines refusal), decimal quantities, a known
// price source and presentation, and the overflow-safety bounds on markup and
// percent overhead inputs. It returns the names of unpriced lines separately
// so R2's refusal sentence can name them.
func ValidateLineSet(lines []Line, markupBps int64, overhead []OverheadLine, now time.Time) ([]string, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("%s", SentenceNoLines)
	}
	if markupBps < 0 || markupBps > 1000000 {
		return nil, fmt.Errorf("customerquote: markup basis points %d outside the supported range", markupBps)
	}
	for _, l := range lines {
		if _, err := parseQtyScaled(l.Qty); err != nil {
			return nil, fmt.Errorf("customerquote: line %q: %w", l.Name, err)
		}
		if l.UnitCostCents < 0 {
			return nil, fmt.Errorf("customerquote: line %q: negative unit cost", l.Name)
		}
		switch l.Presentation {
		case PresentationSelection, PresentationCommodity:
		case "":
			return nil, fmt.Errorf("customerquote: line %q: presentation is required", l.Name)
		default:
			return nil, fmt.Errorf("customerquote: line %q: unknown presentation %q", l.Name, l.Presentation)
		}
		switch l.PriceSource {
		case PriceSourceERP, PriceSourceQuoted, PriceSourceEstimate, PriceSourceUnpriced:
		default:
			return nil, fmt.Errorf("customerquote: line %q: unknown price source %q", l.Name, l.PriceSource)
		}
	}
	for _, o := range overhead {
		switch o.AmountType {
		case "flat", "percent":
		default:
			return nil, fmt.Errorf("customerquote: overhead amount type %q is neither flat nor percent", o.AmountType)
		}
		if o.AmountType == "percent" && (o.Value < 0 || o.Value > 1000000) {
			return nil, fmt.Errorf("customerquote: overhead percent %d outside the supported range", o.Value)
		}
		if o.AmountType == "flat" && o.Value < 0 {
			return nil, fmt.Errorf("customerquote: negative overhead amount")
		}
	}
	return UnpricedLineNames(lines, now), nil
}

// CustomerContact is the customer_contact JSONB shape: the homeowner's name
// and optional contact channels. The name is the send gate (R6).
type CustomerContact struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

// ChangeRequest is one recorded change request (R8), appended on the
// change_requests column.
type ChangeRequest struct {
	ID        string    `json:"id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// ValidTypedName applies the accept-path name bounds (5.2): trimmed, at least
// two characters (the reference's minimum), at most MaxTypedName (the carried
// dealer bound so the column cannot be used as free text).
func ValidTypedName(name string) bool {
	n := strings.TrimSpace(name)
	return utf8.RuneCountInString(n) >= MinTypedName && utf8.RuneCountInString(n) <= MaxTypedName
}

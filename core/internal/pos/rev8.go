package pos

// rev8.go, DIB-488 rev 8: the role-gated margin glance (item 10), the DIB-502 suggested add-ons
// read (item 8), and the 0170 policy validation arms (the curated chip set; margin visibility is
// the stored dealer switch the glance consults beside the configuration's list, the margin glance
// policy ruling: the list decides who MAY be considered, the switch decides whether the answer
// leaves the server, both must allow).
//
// THE MARGIN GATING RULING (the commander's critical rev-8 constraint): margin % and est. profit
// are computed SERVER-SIDE from the pricing engine's own numbers, revenue through the SAME
// counter resolve the ring prices with (the till never asks the margin gate, D48 row 481), cost
// from the engine's cost source (the per-branch WAC row, product_branch_costs.avg_unit_cost_cents),
// and they are gated at the MARSHAL layer: the response struct is all-omitempty, and a caller the
// dealer's two knobs do not both allow receives the zero value, i.e. a
// `{}` payload with the fields BYTE-ABSENT. This mirrors the portal's marshal-and-assert-absent
// idiom; dib488_rev8_test.go asserts byte-absence.

import (
	"context"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// validateMarginVisibility normalizes/refuses the 0170 dealer switch. Omitted ("") normalizes to
// the column default (MANAGER_UP); unknown values are a 422 in words before the CHECK could 500.
func validateMarginVisibility(v string) (string, error) {
	switch v {
	case "":
		return MarginVisibilityManagerUp, nil
	case MarginVisibilityManagerUp, MarginVisibilityAllStaff, MarginVisibilityHidden:
		return v, nil
	default:
		return "", clientErr(http.StatusUnprocessableEntity,
			"pos: margin_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN")
	}
}

// validateBalanceVisibility mirrors validateMarginVisibility for 0181, omitted normalizes to the
// column default (ALL_STAFF), a bad word is a 422 in words BEFORE the CHECK constraint could 500.
func validateBalanceVisibility(v string) (string, error) {
	switch v {
	case "":
		return BalanceVisibilityAllStaff, nil
	case BalanceVisibilityManagerUp, BalanceVisibilityAllStaff, BalanceVisibilityHidden:
		return v, nil
	default:
		return "", clientErr(http.StatusUnprocessableEntity,
			"pos: balance_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN")
	}
}

// validateQuickChipSet normalizes/refuses the curated chip categories (item 4): at most 8 entries,
// each a non-empty comma-free string of at most 40 chars. nil/empty = "not curated" (the service
// derives a top-selling default at read time) and is always fine.
func validateQuickChipSet(set []string) ([]string, error) {
	if len(set) == 0 {
		return nil, nil
	}
	if len(set) > 8 {
		return nil, clientErr(http.StatusUnprocessableEntity,
			"pos: quick_chip_set holds at most 8 categories")
	}
	out := make([]string, 0, len(set))
	for _, c := range set {
		t := strings.TrimSpace(c)
		if t == "" || len([]rune(t)) > 40 || strings.Contains(t, ",") {
			return nil, clientErr(http.StatusUnprocessableEntity,
				"pos: each quick_chip_set category must be 1-40 characters with no comma")
		}
		out = append(out, t)
	}
	return out, nil
}

// marginAllowed answers whether THIS caller may receive margin/profit, in
// three gates. First the branch wall (X2): a branch the caller's resolved
// authority does not reach is REFUSED, never blanked, the refusal carried
// as the error so the handler writes the wall's 403. Then the dealer's two
// knobs, each of which must allow (the margin glance policy ruling). The
// configuration's cost and margin list (X3) decides who MAY be considered:
// a caller it does not name receives nothing, never a fallback to the 0170
// switch. The policy's margin_visibility then decides whether the
// considered caller's answer leaves the server, resolved through the same
// store seam the policy read serves: ALL_STAFF admits everyone considered,
// HIDDEN admits nobody, and MANAGER_UP, like every unknown or empty value,
// admits managers only (fail closed; the purchasing module's cost gate
// resolves unknowns the same way). An unreadable policy fails CLOSED and
// SILENTLY, false with no error, the byte-absent glance: a money gate's
// failure mode is silence, never a 500 on a display read, while the branch
// wall's refusal stays an error. The nil gate fails closed (the lead's
// P1-1 ruling): a service built with no configuration refuses the read, it
// never falls back to a permissive default. An unresolved caller keeps the
// pinned empty answer, an unmapped role receives nothing.
func (s *Service) marginAllowed(ctx context.Context, branchID string) (bool, error) {
	if s.roles == nil {
		return false, errRoleConfigUnwired()
	}
	caller, err := s.caller(ctx)
	if err != nil {
		// An unresolved caller is the pinned empty answer (the route's guard
		// is the door); the wall below judges a RESOLVED authority.
		return false, nil
	}
	if err := requireBranchReach(caller, branchID); err != nil {
		return false, err
	}
	role, ok := canonicalRole(caller)
	if !ok {
		return false, nil
	}
	allowed, err := s.roles.SeesCostAndMargin(role)
	if err != nil {
		return false, nil
	}
	if !allowed {
		return false, nil
	}
	// The policy's switch decides whether the considered caller's answer
	// leaves the server. An unreadable row refuses silently: the glance is
	// a display read, a database hiccup blanks it rather than 500ing the
	// counter.
	p, err := s.store.ReadPosPolicy(ctx, branchID)
	if err != nil {
		return false, nil
	}
	switch p.MarginVisibility {
	case MarginVisibilityAllStaff:
		return true, nil
	case MarginVisibilityHidden:
		return false, nil
	default: // MANAGER_UP, the column default, and where every unknown or empty value lands
		return caller.IsManager(), nil
	}
}

// marginBpsOf computes gross margin in bps from integer cents (identity.MarginBps's arithmetic,
// kept local so a display glance never imports the enforcement gate's semantics).
func marginBpsOf(revenueCents, costCents int64) int64 {
	if revenueCents <= 0 {
		return 0
	}
	return ((revenueCents - costCents) * 10000) / revenueCents
}

// MarginGlance derives the CURRENT ticket's per-line margin + the footer pair, role-gated at the
// marshal layer (see the file header). Display-only: it never writes, never blocks, and a line
// that cannot answer (no cost row, unpriceable) is ABSENT from the payload and excluded from the
// footer honestly (Partial says so).
func (s *Service) MarginGlance(ctx context.Context, in MarginGlanceInput) (MarginGlance, error) {
	if in.BranchID == "" {
		return MarginGlance{}, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	allowed, err := s.marginAllowed(ctx, in.BranchID)
	if err != nil {
		return MarginGlance{}, err
	}
	if !allowed {
		// THE VISIBILITY GATE: the zero value marshals as `{}`, margin/profit BYTE-ABSENT for this caller.
		return MarginGlance{}, nil
	}
	if len(in.Lines) == 0 {
		return MarginGlance{}, nil
	}
	// Customer type for the revenue derivation, the same posture the terminal's reprice sends.
	customerType := strings.TrimSpace(in.CustomerType)
	if in.CustomerID != "" {
		// A glance prices a NEW document at the account's type, so it is held to the customer wall like the
		// sale it previews (customerFor's ruling): an account the caller cannot reach answers 404 "customer not
		// found", the same as an id that is no account.
		caller, err := s.caller(ctx)
		if err != nil {
			return MarginGlance{}, err
		}
		cust, err := s.customerFor(ctx, caller, in.CustomerID)
		if err != nil {
			return MarginGlance{}, err
		}
		if cust.CustomerType != "" {
			customerType = cust.CustomerType
		}
	}
	if customerType == "" {
		customerType = "RETAIL"
	}
	ids := make([]string, 0, len(in.Lines))
	for _, li := range in.Lines {
		if li.ProductID != "" {
			ids = append(ids, li.ProductID)
		}
	}
	costs, err := s.store.ReadBranchCosts(ctx, ids, in.BranchID)
	if err != nil {
		return MarginGlance{}, err
	}
	out := MarginGlance{Lines: make([]MarginGlanceLine, len(in.Lines))}
	var revSum, costSum int64
	known := 0
	for i, li := range in.Lines {
		wac, hasCost := costs[li.ProductID]
		if !hasCost || li.ProductID == "" || li.QtyEntered <= 0 {
			out.Partial = true
			continue // this line's fields stay absent, no cost is not a 100% margin
		}
		// Revenue through the SAME counter resolve the ring prices with (D48 row 481): the till
		// never asks the margin gate, so this resolve carries no gate question and no audit write
		// on either path, and the glance marries cost to revenue ITSELF below, so the numbers
		// stay byte-identical to the terminal's own reprice.
		lp, derr := s.pricing.DeriveCounterLinePrice(ctx, pricing.ResolveInput{
			CustomerID:   in.CustomerID,
			CustomerType: customerType,
			ProductID:    li.ProductID,
			BranchID:     in.BranchID,
			QtyEntered:   li.QtyEntered,
			UOMEntered:   li.UOMEntered,
		})
		if derr != nil || lp.LineTotalCents <= 0 {
			out.Partial = true
			continue // unpriceable → absent, never a fake number
		}
		// WAC is per BASE uom; the engine's QtyBase is the entered qty in base units.
		lineCost := int64(float64(wac)*lp.QtyBase + 0.5)
		m := marginBpsOf(lp.LineTotalCents, lineCost)
		p := lp.LineTotalCents - lineCost
		out.Lines[i] = MarginGlanceLine{MarginBps: &m, EstProfitCents: &p}
		revSum += lp.LineTotalCents
		costSum += lineCost
		known++
	}
	if known > 0 {
		fm := marginBpsOf(revSum, costSum)
		fp := revSum - costSum
		out.MarginBps = &fm
		out.EstProfitCents = &fp
	} else {
		// Nothing answerable: the whole payload stays `{}` (plus partial), the UI hides the column.
		out.Lines = nil
	}
	return out, nil
}

// AddonSuggestions answers the DIB-502 strip: up to `limit` (≤3) suggestion tiles for the current
// ticket, from the 0169 category-level pairing map, excluding products already on the ticket.
// Every empty outcome, empty ticket, no categories, empty map, no candidates, is an ordinary
// empty list, never an error (the strip hides entirely).
func (s *Service) AddonSuggestions(ctx context.Context, ticketProductIDs []string, limit int) (AddonSuggestions, error) {
	out := AddonSuggestions{Suggestions: []AddonSuggestion{}}
	if limit <= 0 || limit > 3 {
		limit = 3
	}
	if len(ticketProductIDs) == 0 {
		return out, nil
	}
	pairings, err := s.store.ReadAddonPairings(ctx)
	if err != nil {
		return out, err
	}
	if len(pairings) == 0 {
		return out, nil
	}
	// The ticket lines' categories, via the existing addon-candidate read run in reverse: we need
	// each ticket product's application. Reuse ListAddonCandidates' product read narrowly, a
	// dedicated tiny read keeps the seam honest.
	apps, err := s.store.ReadProductApplications(ctx, ticketProductIDs)
	if err != nil {
		return out, err
	}
	// Compose the suggested categories in pairing-rank order, first-seen-wins across lines.
	var suggested []string
	seen := map[string]bool{}
	for _, id := range ticketProductIDs {
		app := apps[id]
		if app == "" {
			continue
		}
		for _, sug := range pairings[app] {
			key := strings.ToLower(sug)
			if !seen[key] {
				seen[key] = true
				suggested = append(suggested, sug)
			}
		}
	}
	if len(suggested) == 0 {
		return out, nil
	}
	exclude := append([]string{}, ticketProductIDs...)
	cands, err := s.store.ListAddonCandidates(ctx, suggested, exclude, limit)
	if err != nil {
		return out, err
	}
	for _, c := range cands {
		uom := c.SoldBy
		if uom == "" {
			uom = c.BaseUOM
		}
		qty := c.MinimumOrder
		if qty <= 0 {
			qty = 1
		}
		size := strings.TrimSpace(c.ProductSize)
		if size == "" {
			size = strings.TrimSpace(c.DimensionsLWH)
		}
		// The seed uses a literal em-dash for "no dimensions", that is an ABSENT size line, not a
		// size line that says dash.
		if size == "\u2014" || size == "-" {
			size = ""
		}
		out.Suggestions = append(out.Suggestions, AddonSuggestion{
			ProductID: c.ProductID, SKU: c.SKU, Name: c.Name, SizeLine: size,
			UOM: uom, DefaultQty: qty, ListPriceCents: c.ListPriceCents, PriceUOM: c.PriceUOM,
			HasImage: c.HasImage, Application: c.Application,
		})
	}
	return out, nil
}

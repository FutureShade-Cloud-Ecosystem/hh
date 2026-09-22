package product

// coverage.go, DIB-80 / S1 · the hardscape material calculator's DATA HALF.
//
// WHAT THIS IS
// ------------
// A rep at the counter is asked "how much material for a 420 square foot patio?". Answering it needs
// three facts per product that live in three different tables:
//
//	1. HOW MUCH ONE UNIT COVERS , products.coverage_sf_per_unit, the promoted column, per BASE unit.
//	2. WHAT UNIT IT SELLS IN    , product_rounding_rules.sell_increment_uom (the unit the quote line
//	                               carries), and product_uoms.qty_in_base to get from that unit to the
//	                               base unit the coverage is denominated in.
//	3. WHETHER IT ROUNDS        , product_rounding_rules.break_pallet_allowed + the PLT factor, and
//	                               uoms.dimension, which is what says whether depth is even a question.
//
// This file loads exactly that, a BASIS row per product, no arithmetic on the customer's numbers.
// The arithmetic is in app/src/_material-calc.ts, next to the inputs a rep types, so a keystroke does
// not cost a round trip; what the server owns is the DATA and its provenance, which is the half that
// can be silently wrong.
//
// THE COVERAGE FALLBACK IS THE INTERESTING PART
// ---------------------------------------------
// products.coverage_sf_per_unit is NULL for every row the demo seed writes, and per DATA_MODEL §2
// the four convenience columns (pallet_count, weight_per_unit_kg, pieces_per_sf,
// coverage_sf_per_unit) "duplicate product_uoms facts for facet/query speed, product_uoms is the
// source of truth on conflict". So the truth for a paver is already in the database, twice over:
//
//	products.pieces_per_sf = 4.10          → 4.10 pieces make one square foot
//	product_uoms('SF').qty_in_base = 4.10  → one SF is 4.10 base pieces
//
// Coverage per base piece is the reciprocal: 1 / 4.10 = 0.2439 sq ft. So a row reports coverage from
// the promoted column when it HAS one, and otherwise derives it from the SF conversion factor and
// SAYS SO (CoverageSource). What it never does is invent one: a product with neither is returned with
// no coverage and no source, and the calculator prints "no coverage data for this product" rather
// than a number nobody can defend.
//
// Reciprocal-of-a-factor is not a modelling choice, it is the same identity pricing/uomplan.go relies
// on for its reverse edges ("An edge is usable in reverse at 1/factor").
//
// WHY NOT REUSE pricing.PlanUOM
// -----------------------------
// It is the right engine for a DIFFERENT question. PlanUOM converts a quantity a human already knows
// between units and rounds up at the pick face; it has no notion of coverage or waste, and it walks
// pricing_uom_conversions, which the demo seeds with TWO edges for ONE product, both
// SEEDED_DEFAULT. A 30-product calculator cannot stand on that table today. The rounding vocabulary
// here is deliberately the same (exact, rounded, overage) so the two surfaces read alike, and the
// day pricing_uom_conversions is populated with confirmed edges this loader is where they get wired.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// maxCoverageIDs caps an ids= request. The whole catalogue is 30 rows today and the unfiltered read
// is the normal case; the cap exists so a hand-built URL cannot ask for an unbounded IN list.
const maxCoverageIDs = 500

// CoverageBasis is one product's material-calculator basis: the coverage fact, the unit it sells in,
// and the conversions needed to move between them. Every quantity is a float64 in its named unit -
// there is no money here and nothing is persisted from it, so the cents/DECIMAL discipline that
// governs prices does not apply; what does apply is that a MISSING number is nil, never 0. "This
// product covers 0 sq ft per unit" and "nobody has recorded what this product covers" are different
// facts and the second one is the common one.
type CoverageBasis struct {
	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Category  string `json:"category"` // category code, '' when uncategorised

	// BaseUOM is the stocking unit, the unit CoverageSFPerUnit is denominated in.
	BaseUOM string `json:"base_uom"`
	// BaseDimension is the base unit's uoms.dimension: count | area | weight | volume | length.
	BaseDimension string `json:"base_dimension"`
	// SellUOM is the unit a quote line carries: product_rounding_rules.sell_increment_uom, falling
	// back to products.price_uom and then base_uom, so this is never empty.
	SellUOM string `json:"sell_uom"`
	// SellDimension is SellUOM's uoms.dimension, what tells the calculator whether this product is
	// an AREA question (pavers by the square foot) or a VOLUME one (gravel, where depth matters).
	SellDimension string `json:"sell_dimension"`
	// SellUOMFactor is product_uoms.qty_in_base for SellUOM: how many BASE units one sell unit is.
	// nil when the sell unit has no conversion row, the row is then honest about not being able to
	// express coverage in the unit it sells in, instead of assuming 1.
	SellUOMFactor *float64 `json:"sell_uom_factor"`

	// CoverageSFPerUnit is the square footage ONE BASE UNIT covers. nil = no coverage data.
	CoverageSFPerUnit *float64 `json:"coverage_sf_per_unit"`
	// CoverageSource names where that number came from, and is empty exactly when the number is nil:
	//   "column", products.coverage_sf_per_unit, the A-18-era promoted column (authoritative)
	//   "uom"   , derived as 1 / the SF conversion factor (product_uoms, the DATA_MODEL's truth)
	CoverageSource string `json:"coverage_source"`

	// PalletFactor is product_uoms.qty_in_base for 'PLT', base units on one pallet. nil when the
	// product has no pallet row.
	PalletFactor *float64 `json:"pallet_factor"`
	// BreakPalletAllowed mirrors product_rounding_rules; false + a known PalletFactor is the one case
	// where the sell increment is a whole PALLET rather than a single unit.
	BreakPalletAllowed bool `json:"break_pallet_allowed"`
	// WasteFactorPct is the product's OWN recommended waste (5.00 for pavers/slabs in the seed). It
	// is advice carried to the surface, not an override: the rep's entered waste is what computes.
	WasteFactorPct float64 `json:"waste_factor_pct"`

	// DensityTPerCYD is tonnes per cubic yard for a product sold by WEIGHT, the fact that turns a
	// volume of gravel into a tonnage. It is read from products.metadata->>'density_t_per_cyd'
	// (BT-A-006's documented home for unpromoted facts) because no column carries it; nil when it is
	// absent, and the calculator then reports the cubic yards and says the tonnage cannot be derived.
	DensityTPerCYD *float64 `json:"density_t_per_cyd"`
}

// coverageStore is the OPTIONAL persistence capability behind the calculator basis, asserted onto
// whatever store the Service was built with rather than added to productStore.
//
// That is the ListPriceUOMReader pattern and it is load-bearing here: productStore is an unexported
// interface implemented by *Repository AND by the fakes in service_test.go, so widening it would
// break every existing fake, a change to a test file is a change to evidence, and evidence should
// not have to move for a feature that adds a read.
type coverageStore interface {
	CoverageBasis(ctx context.Context, ids []string) ([]CoverageBasis, error)
}

// CoverageBasis returns the calculator basis for the given product ids, or for every ACTIVE product
// when ids is empty.
//
// A store with no coverage capability yields an EMPTY slice and no error. That is the same ruling as
// the frontend's asRows: nothing was in it, and empty is not a fault. A calculator that paints an
// error banner because the module it is talking to predates the feature has told the user their
// database is broken, which it is not.
func (s *Service) CoverageBasis(ctx context.Context, ids []string) ([]CoverageBasis, error) {
	cs, ok := s.store.(coverageStore)
	if !ok {
		return []CoverageBasis{}, nil
	}
	clean := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
		if len(clean) == maxCoverageIDs {
			break
		}
	}
	rows, err := cs.CoverageBasis(ctx, clean)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []CoverageBasis{}
	}
	return rows, nil
}

// - the SQL -

// coverageBasisSQL is one query for the whole basis. The joins are all LEFT: a product with no
// rounding rule, no SF factor and no pallet row is still a row in the answer, it is the row the
// calculator has to be honest about, and dropping it would turn "we do not know" into "this product
// does not exist".
//
// pu_sf is joined on the SF row specifically (not on the sell unit) because the DERIVED coverage
// identity is about SQUARE FEET: 1 / (base units per SF). The sell unit's own factor is a separate
// join, used to express coverage in the unit the line carries.
const coverageBasisSQL = `
SELECT p.id::text,
       p.sku::text,
       p.name,
       COALESCE(c.code::text, ''),
       p.base_uom::text,
       COALESCE(ub.dimension, ''),
       COALESCE(NULLIF(rr.sell_increment_uom::text, ''), p.price_uom::text) AS sell_uom,
       COALESCE(us.dimension, ''),
       pu_sell.qty_in_base::text,
       p.coverage_sf_per_unit::text,
       pu_sf.qty_in_base::text,
       pu_plt.qty_in_base::text,
       COALESCE(rr.break_pallet_allowed, TRUE),
       COALESCE(rr.waste_factor_pct, 0)::text,
       NULLIF(p.metadata->>'density_t_per_cyd', '')
  FROM products p
  LEFT JOIN product_categories     c      ON c.id = p.category_id
  LEFT JOIN product_rounding_rules rr     ON rr.product_id = p.id
  LEFT JOIN uoms                   ub     ON ub.code = p.base_uom
  LEFT JOIN uoms                   us     ON us.code = COALESCE(NULLIF(rr.sell_increment_uom::text, ''), p.price_uom::text)
  LEFT JOIN product_uoms           pu_sell ON pu_sell.product_id = p.id
                                          AND pu_sell.uom_code = COALESCE(NULLIF(rr.sell_increment_uom::text, ''), p.price_uom::text)
  LEFT JOIN product_uoms           pu_sf   ON pu_sf.product_id = p.id AND pu_sf.uom_code = 'SF'
  LEFT JOIN product_uoms           pu_plt  ON pu_plt.product_id = p.id AND pu_plt.uom_code = 'PLT'
 WHERE %s
 ORDER BY p.name, p.id`

// CoverageBasis loads the calculator basis rows (DIB-80). ids empty = every active product.
func (r *Repository) CoverageBasis(ctx context.Context, ids []string) ([]CoverageBasis, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	where := "p.is_active"
	var args []any
	if len(ids) > 0 {
		// The id filter deliberately does NOT also require is_active: a caller naming a specific
		// product asked about THAT product, and silently answering nothing for a discontinued SKU
		// somebody is looking at on screen is the fault this module keeps writing tests against.
		where = "p.id = ANY($1::uuid[])"
		args = append(args, ids)
	}
	rows, err := r.pool.Query(ctx, fmt.Sprintf(coverageBasisSQL, where), args...)
	if err != nil {
		return nil, fmt.Errorf("coverage basis: %w", err)
	}
	defer rows.Close()
	out := []CoverageBasis{}
	for rows.Next() {
		var b CoverageBasis
		// Every DECIMAL is read as TEXT and parsed, the same way ConvertUOM does: pgx would hand a
		// DECIMAL(19,8) back through a float64 conversion we do not control, and NULL has to stay
		// distinguishable from 0.
		var coverageTxt, sfFactorTxt, pltFactorTxt, sellFactorTxt, wasteTxt, densityTxt *string
		if err := rows.Scan(
			&b.ProductID, &b.SKU, &b.Name, &b.Category,
			&b.BaseUOM, &b.BaseDimension, &b.SellUOM, &b.SellDimension,
			&sellFactorTxt, &coverageTxt, &sfFactorTxt, &pltFactorTxt,
			&b.BreakPalletAllowed, &wasteTxt, &densityTxt,
		); err != nil {
			return nil, fmt.Errorf("coverage basis scan: %w", err)
		}
		b.SellUOMFactor = parseDecimalPtr(sellFactorTxt)
		b.PalletFactor = parseDecimalPtr(pltFactorTxt)
		b.DensityTPerCYD = positivePtr(parseDecimalPtr(densityTxt))
		if w := parseDecimalPtr(wasteTxt); w != nil {
			b.WasteFactorPct = *w
		}
		applyCoverage(&b, parseDecimalPtr(coverageTxt), parseDecimalPtr(sfFactorTxt))
		if b.SellUOM == "" {
			b.SellUOM = b.BaseUOM
			b.SellDimension = b.BaseDimension
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coverage basis rows: %w", err)
	}
	return out, nil
}

// applyCoverage settles the ONE derived fact on the row: how much square footage a base unit covers,
// and where that number came from.
//
// Order is precedence, and precedence is "what a human recorded beats what we worked out": the
// promoted column wins when it is present AND positive, the SF conversion factor's reciprocal is the
// fallback, and neither leaves the row with nil coverage and an empty source. A zero or negative
// value in either place is treated as absent rather than trusted, coverage of zero square feet is
// not a fact about a paver, it is a broken import, and dividing by it would print Infinity at the
// counter.
//
// Exported-adjacent and split out from the scan loop on purpose: it is the rule the unit tests drive
// directly, with no database in the room.
func applyCoverage(b *CoverageBasis, column, sfFactor *float64) {
	if column != nil && *column > 0 {
		v := *column
		b.CoverageSFPerUnit = &v
		b.CoverageSource = "column"
		return
	}
	if sfFactor != nil && *sfFactor > 0 {
		// One SF is *sfFactor base units, so one base unit covers 1/*sfFactor SF.
		v := 1 / *sfFactor
		b.CoverageSFPerUnit = &v
		b.CoverageSource = "uom"
		return
	}
	b.CoverageSFPerUnit = nil
	b.CoverageSource = ""
}

// parseDecimalPtr turns a nullable DECIMAL-as-text into *float64. An unparseable value is nil rather
// than an error: one product with a corrupt factor must not take the whole calculator down, and nil
// is a state this payload already models honestly.
func parseDecimalPtr(s *string) *float64 {
	if s == nil {
		return nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	if err != nil {
		return nil
	}
	return &v
}

// positivePtr passes a pointer through only when it holds a positive number. Used for the density,
// where 0 t/yd³ is not a density.
func positivePtr(p *float64) *float64 {
	if p == nil || *p <= 0 {
		return nil
	}
	return p
}

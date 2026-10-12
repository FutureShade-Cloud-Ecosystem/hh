package product

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/likeutil"
	"github.com/jackc/pgx/v5"
)

// isNoRows is the module-local "the query matched nothing" predicate, kept next to the two counter
// reads that need it so neither hard-codes the pgx sentinel inline.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// counterStore is the W3 counter-search seam.
//
// A SEPARATE, optional interface rather than two more methods on productStore, for the same reason
// customer.scopedAccountSearcher is separate: productStore is implemented outside this package
// (internal/portal composes the Service with its own fake catalogue), and widening it would be a
// breaking change to a module this swarm does not own. *Repository implements it, so the real
// deployment always has the counter surface; a Service built over a store that does not gets a clean
// 503 from errNoCounterStore rather than a nil-method panic.
type counterStore interface {
	CounterSearch(ctx context.Context, q CounterSearchQuery) ([]CounterHit, error)
	ResolveScan(ctx context.Context, code string) (CounterHit, bool, error)
}

// errNoCounterStore is the answer when the wired store predates the counter surface.
var errNoCounterStore = fmt.Errorf("product: counter search is not available on this catalogue store")

// counter returns the counter seam if the wired store implements it.
func (s *Service) counter() (counterStore, bool) {
	cs, ok := s.store.(counterStore)
	return cs, ok
}

// - GABLE:pos-search + GABLE:search-synonyms + GABLE:pos-barcode -
//
// The POS grid used to fetch the whole active catalogue once and filter it in the browser with
// `name.includes(q) || sku.includes(q)`. That works at 30 products and stops working somewhere before
// 3,000: the payload grows without bound, the filter has no notion of relevance (a substring hit deep
// in a description outranks the SKU you typed), and it cannot see anything the payload does not carry
//, barcodes, aliases, branch stock. This is the server-side replacement.
//
// RELEVANCE IS ORDERED, NOT SCORED. A counter search is not a recommendation engine. The cashier
// typed something they already know; the job is to put the thing they meant first, deterministically:
//
//	0  SKU exact / barcode exact , they scanned it or typed it in full. Nothing outranks this.
//	1  SKU prefix                , they typed the first half of a SKU.
//	2  name prefix               , "techo" → Techo-Bloc everything.
//	3  name substring            , "blu" → Techo-Bloc Blu 60.
//	4  description substring     , the long tail of the same intent.
//	5  synonym                   , the customer's word, not the catalogue's ("patio stone" → Slab).
//	6  trigram similarity        , the typo net (pg_trgm), last because it is the only fuzzy arm.
//
// Ties inside a rank break on trigram similarity, then name, so the order is stable across calls -
// a list that reshuffles under an unchanged query is worse than a list that ranks imperfectly.
//
// SYNONYMS ARE AN OR-BRANCH, NOT A REWRITE. A rewrite ("patio stone" becomes "Slab") loses the
// original term, so a product literally NAMED "Patio Stone" would stop matching itself. Adding the
// alias as another arm keeps both.

// CounterHit is one search result for the counter: enough to render a tile and add a line, nothing
// more. Prices come from the pricing engine at add-time, as they always have, ListPriceCents here
// is the retail ANCHOR for display, never the sell price.
type CounterHit struct {
	ProductID   string `json:"product_id"`
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Collection  string `json:"collection,omitempty"`
	Application string `json:"application,omitempty"`
	ColorFamily string `json:"color_family,omitempty"`
	BaseUOM     string `json:"base_uom"`
	SoldBy      string `json:"sold_by,omitempty"`
	// HasImage lets the UI render a thumbnail without first issuing a request that 404s for the
	// (currently overwhelming) majority of products that have no image.
	HasImage bool `json:"has_image"`
	// MatchReason is the human-readable rank arm, shown in the UI as a small chip so a cashier can
	// tell WHY a row is in the list ("matched: alias") instead of distrusting the search.
	MatchReason string `json:"match_reason"`
	// OnHandBranch is on-hand in base UOM at the requested branch. A pointer: "no stock record" and
	// "zero on hand" are different facts and a counter must not be shown 0 for the former.
	OnHandBranch   *float64           `json:"on_hand_branch,omitempty"`
	OnHandByBranch map[string]float64 `json:"on_hand_by_branch,omitempty"`
	ListPriceCents *int64             `json:"list_price_cents,omitempty"`
}

// CounterSearchQuery is the debounced instant-search request.
type CounterSearchQuery struct {
	Q        string
	BranchID string // scopes the on-hand display; "" → every branch's on-hand is returned
	Limit    int
}

// match-reason labels, derived from the SQL rank so the ordering rationale lives in exactly one place.
const (
	matchExact       = "EXACT"       // sku or barcode, in full
	matchSKUPrefix   = "SKU_PREFIX"  //
	matchNamePrefix  = "NAME_PREFIX" //
	matchNameSub     = "NAME"        //
	matchDescription = "DESCRIPTION" //
	matchSynonym     = "ALIAS"       // a product_search_synonyms hit
	matchFuzzy       = "FUZZY"       // pg_trgm similarity, the typo net
)

func matchReasonFor(rank int) string {
	switch rank {
	case 0:
		return matchExact
	case 1:
		return matchSKUPrefix
	case 2:
		return matchNamePrefix
	case 3:
		return matchNameSub
	case 4:
		return matchDescription
	case 5:
		return matchSynonym
	default:
		return matchFuzzy
	}
}

const (
	defaultCounterLimit = 20
	maxCounterLimit     = 50
	// minCounterTermLen keeps a single keystroke from scanning the catalogue. One character matches
	// almost everything, so the result is noise the cashier has to read past, and it is the query
	// that costs the most. Two is where the search starts being useful.
	minCounterTermLen = 2
)

// CounterSearch runs the ranked, synonym-aware counter search and enriches each hit with the branch
// on-hand and retail anchor through the SAME zero-trust seams the catalogue list uses (this module
// never SELECTs stock_balances or pricing_list_prices).
func (s *Service) CounterSearch(ctx context.Context, q CounterSearchQuery) ([]CounterHit, error) {
	q.Q = strings.TrimSpace(q.Q)
	if len(q.Q) < minCounterTermLen {
		// Not an error the UI should surface as a failure, it is simply too early to search. An
		// empty list keeps the debounced input quiet while the cashier is still typing.
		return []CounterHit{}, nil
	}
	if q.Limit <= 0 {
		q.Limit = defaultCounterLimit
	}
	if q.Limit > maxCounterLimit {
		q.Limit = maxCounterLimit
	}
	cs, ok := s.counter()
	if !ok {
		return nil, errNoCounterStore
	}
	hits, err := cs.CounterSearch(ctx, q)
	if err != nil {
		return nil, err
	}
	s.enrichHits(ctx, hits, q.BranchID)
	return hits, nil
}

// ResolveScan turns one scanned or typed code into a single product. It is the barcode rail: the
// scanner is a keyboard that types digits and presses Enter, so the endpoint has to answer "which
// product is this" with no ambiguity and no picker.
//
// Barcode first, then SKU. A yard label and a SKU are different namespaces, and the barcode is the
// one a scanner produces, checking it first means the physical scan never loses to a coincidental
// SKU match. A code that resolves to nothing is a 404 the UI turns into "unknown code", NOT an empty
// search result: a scan that silently finds nothing is how the wrong item ends up on a ticket.
func (s *Service) ResolveScan(ctx context.Context, code, branchID string) (CounterHit, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return CounterHit{}, httpx.BadRequestf("scan code is required")
	}
	cs, ok := s.counter()
	if !ok {
		return CounterHit{}, errNoCounterStore
	}
	hit, found, err := cs.ResolveScan(ctx, code)
	if err != nil {
		return CounterHit{}, err
	}
	if !found {
		return CounterHit{}, ErrNotFound
	}
	one := []CounterHit{hit}
	s.enrichHits(ctx, one, branchID)
	return one[0], nil
}

// enrichHits attaches branch on-hand and the retail anchor in ONE bulk call each (the DIB-404
// reasoning: one round trip per row is what makes a list feel broken as the catalogue grows). Both
// are best-effort, a stock or pricing read that fails degrades the row to "no detail", it does not
// fail the search. A cashier can still ring a product whose on-hand read timed out.
func (s *Service) enrichHits(ctx context.Context, hits []CounterHit, branchID string) {
	if len(hits) == 0 {
		return
	}
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ProductID)
	}
	if s.branchQty != nil {
		if byProduct, err := s.branchQty.OnHandByBranch(ctx, ids); err == nil {
			for i := range hits {
				perBranch := byProduct[hits[i].ProductID]
				if len(perBranch) == 0 {
					continue
				}
				hits[i].OnHandByBranch = perBranch
				if branchID != "" {
					if v, ok := perBranch[branchID]; ok {
						qty := v
						hits[i].OnHandBranch = &qty
					}
				}
			}
		}
	}
	if s.listPrices != nil {
		if prices, err := s.listPrices.ListPriceCentsFor(ctx, ids); err == nil {
			for i := range hits {
				if cents, ok := prices[hits[i].ProductID]; ok {
					v := cents
					hits[i].ListPriceCents = &v
				}
			}
		}
	}
}

// - store -

// counterSelect is the projection shared by the ranked search and the single-code scan resolve, so
// both return the identical row shape and the UI has one renderer.
const counterSelect = `
  p.id::text, p.sku::text, p.name, COALESCE(p.description,''), COALESCE(p.collection,''),
  COALESCE(p.application,''), COALESCE(p.color_family,''), COALESCE(p.base_uom::text,''),
  COALESCE(p.sold_by,''),
  EXISTS (SELECT 1 FROM product_images pi WHERE pi.product_id = p.id)`

// CounterSearch is the ranked catalogue search behind GET /api/v1/products/counter-search.
//
// $1 = the raw term (trigram operand + citext synonym key + exact comparisons)
// $2 = the SAME term with LIKE metacharacters escaped, used for every prefix/substring arm so a '%'
//
//	a cashier types is matched literally instead of turning the lookup into a full scan
//
// $3 = limit
func (r *Repository) CounterSearch(ctx context.Context, q CounterSearchQuery) ([]CounterHit, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// syn resolves the typed term to its canonical catalogue words ONCE, then the arms below reuse it.
	// A product-scoped alias pins one product; a global alias matches the canonical word against the
	// fields that carry catalogue vocabulary.
	const synMatch = `
    EXISTS (SELECT 1 FROM syn s
             WHERE (s.product_id IS NOT NULL AND s.product_id = p.id)
                OR (s.product_id IS NULL AND (
                      p.name ILIKE '%' || s.canonical || '%'
                   OR COALESCE(p.collection,'') ILIKE '%' || s.canonical || '%'
                   OR COALESCE(p.application,'') ILIKE '%' || s.canonical || '%'
                   OR COALESCE(p.color_family,'') ILIKE '%' || s.canonical || '%')))`

	sql := `
WITH syn AS (
  SELECT s.canonical::text AS canonical, s.product_id
    FROM product_search_synonyms s
   WHERE s.is_active AND s.term = $1::citext
)
SELECT ` + counterSelect + `,
  CASE
    WHEN p.sku::text ILIKE $2 ESCAPE '\' THEN 0
    WHEN EXISTS (SELECT 1 FROM product_barcodes bc WHERE bc.product_id = p.id AND bc.code = $1::citext) THEN 0
    WHEN p.sku::text ILIKE $2 || '%' ESCAPE '\' THEN 1
    WHEN p.name ILIKE $2 || '%' ESCAPE '\' THEN 2
    WHEN p.name ILIKE '%' || $2 || '%' ESCAPE '\' THEN 3
    WHEN COALESCE(p.description,'') ILIKE '%' || $2 || '%' ESCAPE '\' THEN 4
    WHEN ` + synMatch + ` THEN 5
    ELSE 6
  END AS rank,
  similarity(p.name, $1) AS sim
  FROM products p
 WHERE p.is_active
   AND (
        p.sku::text ILIKE $2 || '%' ESCAPE '\'
     OR p.name ILIKE '%' || $2 || '%' ESCAPE '\'
     OR COALESCE(p.description,'') ILIKE '%' || $2 || '%' ESCAPE '\'
     OR EXISTS (SELECT 1 FROM product_barcodes bc WHERE bc.product_id = p.id AND bc.code = $1::citext)
     OR ` + synMatch + `
     OR p.name % $1
   )
 ORDER BY rank, sim DESC NULLS LAST, p.name
 LIMIT $3`
	rows, err := r.pool.Query(ctx, sql, q.Q, likeutil.EscapeLikePattern(q.Q), q.Limit)
	if err != nil {
		return nil, fmt.Errorf("counter search %q: %w", q.Q, err)
	}
	defer rows.Close()
	var out []CounterHit
	for rows.Next() {
		var h CounterHit
		var rank int
		var sim *float64
		if err := rows.Scan(&h.ProductID, &h.SKU, &h.Name, &h.Description, &h.Collection,
			&h.Application, &h.ColorFamily, &h.BaseUOM, &h.SoldBy, &h.HasImage, &rank, &sim); err != nil {
			return nil, fmt.Errorf("counter search %q: %w", q.Q, err)
		}
		h.MatchReason = matchReasonFor(rank)
		out = append(out, h)
	}
	return out, rows.Err()
}

// ResolveScan looks one code up as a barcode, then as a SKU. Both comparisons are on CITEXT/exact
// equality, a scan is not a search, and a "close enough" match here would put the wrong product on
// a ticket.
func (r *Repository) ResolveScan(ctx context.Context, code string) (CounterHit, bool, error) {
	if r.pool == nil {
		return CounterHit{}, false, errNoDB
	}
	sql := `
SELECT ` + counterSelect + `,
  CASE WHEN bc.id IS NOT NULL THEN 'BARCODE' ELSE 'SKU' END AS via
  FROM products p
  LEFT JOIN product_barcodes bc ON bc.product_id = p.id AND bc.code = $1::citext
 WHERE p.is_active AND (bc.id IS NOT NULL OR p.sku::citext = $1::citext)
 ORDER BY (bc.id IS NOT NULL) DESC
 LIMIT 1`
	var h CounterHit
	var via string
	err := r.pool.QueryRow(ctx, sql, code).Scan(&h.ProductID, &h.SKU, &h.Name, &h.Description,
		&h.Collection, &h.Application, &h.ColorFamily, &h.BaseUOM, &h.SoldBy, &h.HasImage, &via)
	if err != nil {
		if isNoRows(err) {
			return CounterHit{}, false, nil
		}
		return CounterHit{}, false, fmt.Errorf("resolve scan %q: %w", code, err)
	}
	h.MatchReason = matchExact
	return h, true, nil
}

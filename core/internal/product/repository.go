package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoDB = errors.New("product: database not configured")

// productColumns is the canonical SELECT list (kept in one place so List/Get scan identically).
const productColumns = `id::text, sku::text, name, description, manufacturer, collection,
	color, finish, application, color_family, texture, product_size, sold_by, base_uom::text, dimensions_lwh,
	is_featured, is_special_order, contractor_pricing_event_discount,
	full_skid_quantity, layer_quantity, pallet_count, minimum_order_qty::text, is_active, metadata`

// Repository owns the products table (pgx). Zero cross-module access.
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func scanProduct(row interface {
	Scan(dest ...any) error
}) (Product, error) {
	var p Product
	var meta []byte
	if err := row.Scan(
		&p.ID, &p.SKU, &p.Name, &p.Description, &p.Manufacturer, &p.Collection,
		&p.Color, &p.Finish, &p.Application, &p.ColorFamily, &p.Texture, &p.ProductSize, &p.SoldBy, &p.BaseUOM, &p.DimensionsLWH,
		&p.IsFeatured, &p.IsSpecialOrder, &p.ContractorPricingEventDiscount,
		&p.FullSkidQuantity, &p.LayerQuantity, &p.PalletCount, &p.MinimumOrderQty, &p.IsActive, &meta,
	); err != nil {
		return Product{}, err
	}
	if len(meta) > 0 {
		if err := json.Unmarshal(meta, &p.Metadata); err != nil {
			return Product{}, fmt.Errorf("product: decode metadata for %s: %w", p.ID, err)
		}
	}
	if p.Metadata == nil {
		p.Metadata = map[string]any{}
	}
	return p, nil
}

// buildFilterWhere assembles the parameterized WHERE clause + args from a Filter. Every
// value is a bound parameter (no interpolation); the q text-search is an ILIKE over
// name+sku+description. `p` is the column prefix (e.g. "p." for an aliased JOIN query, or
// "" for a plain products query), a fixed, caller-controlled literal, never user input. The
// optional inStockIDs slice (resolved by the service through the StockReader seam) restricts
// to those product ids, the product module never SELECTs the stock ledger itself. A
// non-nil, empty inStockIDs means "in-stock requested but nothing is in stock" → an
// impossible predicate so the result is empty.
func buildFilterWhere(f Filter, inStockIDs []string, p string) (where []string, args []any) {
	where = []string{"1=1"}
	add := func(col, val string) {
		if val != "" {
			args = append(args, val)
			where = append(where, fmt.Sprintf("%s%s = $%d", p, col, len(args)))
		}
	}
	add("finish", f.Finish)
	add("color_family", f.ColorFamily)
	add("application", f.Application)
	add("color", f.Color)
	add("manufacturer", f.Manufacturer)
	add("collection", f.Collection)
	if f.CategoryID != "" {
		args = append(args, f.CategoryID)
		where = append(where, fmt.Sprintf("%scategory_id = $%d", p, len(args)))
	}
	if f.Q != "" {
		args = append(args, "%"+escapeLikePattern(f.Q)+"%")
		where = append(where, fmt.Sprintf(
			`(%sname ILIKE $%d ESCAPE '\' OR %ssku::text ILIKE $%d ESCAPE '\' OR %sdescription ILIKE $%d ESCAPE '\')`,
			p, len(args), p, len(args), p, len(args)))
	}
	if f.ActiveOnly {
		where = append(where, p+"is_active")
	}
	if inStockIDs != nil {
		if len(inStockIDs) == 0 {
			where = append(where, "FALSE") // in-stock filter on, but no product is in stock
		} else {
			args = append(args, inStockIDs)
			where = append(where, fmt.Sprintf("%sid = ANY($%d::uuid[])", p, len(args)))
		}
	}
	return where, args
}

// List returns products matching the faceted filter. The WHERE is built from
// non-empty filter fields only, every value parameterized (no interpolation). When the
// filter requests in-stock-only, the service passes the branch-scoped in-stock id set in
// (nil = no in-stock restriction).
func (r *Repository) List(ctx context.Context, f Filter, inStockIDs []string) ([]Product, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	where, args := buildFilterWhere(f, inStockIDs, "")
	// ORDER BY name, id, a TOTAL order, which OFFSET paging requires and `name` alone does not
	// give: products.name is not unique (two branches of a collection ship the same name in
	// different colours), and Postgres is free to return tied rows in any order per query. Under
	// OFFSET that means a row can be served on two consecutive pages while another is served on
	// neither, the pager silently loses products. `id` is the tiebreak because it is the only
	// column on this table guaranteed unique. The tiebreak is applied unpaged too, so an unpaged
	// list stops reshuffling its ties between calls.
	q := fmt.Sprintf("SELECT %s FROM products WHERE %s ORDER BY name, id", productColumns, strings.Join(where, " AND "))
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if f.Offset > 0 {
		args = append(args, f.Offset)
		q += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Count returns how many products match the filter BEFORE Limit/Offset, the denominator a pager
// needs. It reuses buildFilterWhere so the count and the page can never be computed from two
// different definitions of "matching"; only the projection differs.
func (r *Repository) Count(ctx context.Context, f Filter, inStockIDs []string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	where, args := buildFilterWhere(f, inStockIDs, "")
	var n int
	err := r.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT count(*) FROM products WHERE %s", strings.Join(where, " AND ")), args...).Scan(&n)
	return n, err
}

// Facets returns, per facet field, the distinct values + a count that reflects the
// currently-applied filter (M4). The count for a value = number of products matching the
// whole filter that carry that value. Every value is a bound parameter; the facet COLUMN is
// validated against the facetFields allowlist before it is interpolated into GROUP BY, so a
// caller can never inject a column. The category facet reads the joined category code.
func (r *Repository) Facets(ctx context.Context, f Filter, inStockIDs []string) ([]Facet, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	out := make([]Facet, 0, len(facetOrder))
	for _, field := range facetOrder {
		col, ok := facetFields[field]
		if !ok {
			continue
		}
		var q string
		if field == "category" {
			// category value is the joined category code (empty codes/uncategorised excluded).
			// Predicates are prefixed "p." to bind to the aliased products table.
			pw, pargs := buildFilterWhere(f, inStockIDs, "p.")
			q = fmt.Sprintf(`
				SELECT c.code::text AS v, COUNT(*) AS n
				FROM products p
				JOIN product_categories c ON c.id = p.category_id
				WHERE %s AND c.code IS NOT NULL AND c.code::text <> ''
				GROUP BY c.code::text
				ORDER BY v`, strings.Join(pw, " AND "))
			rows, err := r.pool.Query(ctx, q, pargs...)
			if err != nil {
				return nil, fmt.Errorf("facet %s: %w", field, err)
			}
			values, ferr := scanFacetValues(rows)
			if ferr != nil {
				return nil, fmt.Errorf("facet %s: %w", field, ferr)
			}
			out = append(out, Facet{Field: field, Values: values})
			continue
		}
		// col is allowlisted (facetFields), safe to interpolate; the empty string is excluded.
		where, args := buildFilterWhere(f, inStockIDs, "")
		q = fmt.Sprintf(`
				SELECT %s AS v, COUNT(*) AS n
				FROM products
				WHERE %s AND %s <> ''
				GROUP BY %s
				ORDER BY v`, col, strings.Join(where, " AND "), col, col)
		rows, err := r.pool.Query(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("facet %s: %w", field, err)
		}
		values, ferr := scanFacetValues(rows)
		if ferr != nil {
			return nil, fmt.Errorf("facet %s: %w", field, ferr)
		}
		out = append(out, Facet{Field: field, Values: values})
	}
	return out, nil
}

// scanFacetValues drains a (value, count) result set into a FacetValue slice, always closing
// the rows. Returns a non-nil empty slice (never nil) so a facet with no values serializes
// as [] rather than null.
func scanFacetValues(rows pgx.Rows) ([]FacetValue, error) {
	defer rows.Close()
	values := []FacetValue{}
	for rows.Next() {
		var fv FacetValue
		if err := rows.Scan(&fv.Value, &fv.Count); err != nil {
			return nil, err
		}
		values = append(values, fv)
	}
	return values, rows.Err()
}

// Get loads one product by id.
func (r *Repository) Get(ctx context.Context, id string) (Product, error) {
	if r.pool == nil {
		return Product{}, errNoDB
	}
	// A malformed (non-UUID) id can never match the uuid PK, map it to ErrNotFound before the query,
	// which would otherwise raise SQLSTATE 22P02 → HTTP 500. Same 404 as an absent product, matching
	// the order/audit/gl not-found contract.
	if !looksLikeUUID(id) {
		return Product{}, ErrNotFound
	}
	q := fmt.Sprintf("SELECT %s FROM products WHERE id = $1", productColumns)
	p, err := scanProduct(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Product{}, ErrNotFound
		}
		return Product{}, fmt.Errorf("get product %q: %w", id, err)
	}
	return p, nil
}

// Update applies a validated, normalized attribute set (keys already vetted by the
// service against editableColumns) and returns the refreshed row.
func (r *Repository) Update(ctx context.Context, id string, attrs map[string]any) (Product, error) {
	if r.pool == nil {
		return Product{}, errNoDB
	}
	if !looksLikeUUID(id) {
		return Product{}, ErrNotFound
	}
	if len(attrs) == 0 {
		return r.Get(ctx, id)
	}
	cols := make([]string, 0, len(attrs))
	for c := range attrs {
		cols = append(cols, c)
	}
	sort.Strings(cols) // deterministic SQL

	set := make([]string, 0, len(cols)+1)
	args := make([]any, 0, len(cols)+1)
	for _, c := range cols {
		// Defense in depth (L-7): the column name is interpolated into the SQL, so
		// re-verify it against the allowlist here even though the service already did.
		// A future caller that bypasses the service cannot inject an arbitrary column.
		if _, ok := editableColumns[c]; !ok {
			return Product{}, fmt.Errorf("update product: column %q is not editable", c)
		}
		v := attrs[c]
		if c == "metadata" {
			b, err := json.Marshal(v)
			if err != nil {
				return Product{}, fmt.Errorf("encode metadata: %w", err)
			}
			v = b
		}
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", c, len(args)))
	}
	set = append(set, "updated_at = now()")
	args = append(args, id)

	q := fmt.Sprintf("UPDATE products SET %s WHERE id = $%d", strings.Join(set, ", "), len(args))
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return Product{}, fmt.Errorf("update product %q: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return Product{}, fmt.Errorf("update product %q: %w", id, ErrNotFound)
	}
	return r.Get(ctx, id)
}

// Search is the pg_trgm fuzzy/by-SKU catalog seam for the procurement picker (P4.1). It
// WORD-matches name (word similarity, GIN idx_products_name_trgm) OR prefix-matches the
// SKU (idx_products_sku_trgm), ordered by word similarity then whole-name similarity then name.
// category is read from the related product_categories.code via LEFT JOIN (empty when
// uncategorised); base_uom lives on products. The SKU prefix branch keeps exact code lookups
// from depending on any trigram threshold. The service validates the non-empty query and
// clamps limit before this runs.
//
// - DIB-482 (M3-G1): WHY WORD SIMILARITY AND NOT `%` -
// This seam used `p.name % $1`, which is WHOLE-STRING trigram similarity: it compares the term to
// the ENTIRE product name and asks whether the two strings are alike. Catalog names in this yard are
// long and multi-word, 'Best Way Stone Antico Paver', and a shopper types the one word they know:
// the collection. Whole-string similarity scores that pair
//
//	similarity('Best Way Stone Antico Paver', 'Antico') = 0.250
//
// which is BELOW pg_trgm's 0.3 default threshold, so the operator returns false and the type-ahead
// showed nothing for a product that is sitting in the catalog. The failure got WORSE the better the
// name was: a longer, more descriptive name dilutes the score further. 'Techo' found 0 of the 5
// Techo-Bloc products on the seeded catalog for exactly this reason.
//
// `$1 <% p.name` asks the question the operator actually wants asked, "is this term similar to some
// WORD in that name", and scores the same pair 1.0. It is not a threshold tweak: lowering
// similarity_threshold far enough to catch 'Antico' would also admit everything else at 0.25 and turn
// the picker into noise. The index still serves it: `<%` and `%>` are commutators, so the planner
// rewrites `$1 <% p.name` to the indexable `p.name %> $1` and reads idx_products_name_trgm (verified
// by EXPLAIN, Bitmap Index Scan, Index Cond `name %> $1`).
//
// The SKU branch is UNCHANGED and still a plain prefix ILIKE: an operator typing a code wants that
// code, and no similarity measure should get a vote in it.
func (r *Repository) Search(ctx context.Context, query string, limit int) ([]CatalogResult, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// $1 carries the raw term for the pg_trgm word-similarity (<%) operator, word_similarity() and
	// similarity(). $2 is a SEPARATE, escaped LIKE prefix so that '%' and '_' in the user term match
	// literally in the SKU ILIKE branch instead of acting as wildcards (the escaped term is
	// still a bound parameter, never interpolated into the SQL). is_active filters out
	// discontinued products so the line-add picker only surfaces sellable catalog items.
	//
	// THE ORDER BY IS THREE-DEEP AND EACH LEVEL EARNS ITS PLACE. word_similarity first, because it is
	// the measure the WHERE clause just used, ranking by a measure the filter did not use is how a
	// row that barely qualified ends up on top. similarity SECOND, which is the ranking this seam
	// always had and the reason it is kept: with both at word-similarity 1.0, a product actually
	// NAMED 'Antico' should beat 'Best Way Stone Antico Paver', and whole-string similarity (1.0 vs
	// 0.25) is precisely the measure that says so. p.name last, so equal matches come back in a
	// stable order instead of whatever the heap felt like.
	const q = `SELECT p.id::text, p.sku::text, p.name, COALESCE(c.code::text, ''), p.base_uom::text
	FROM products p
	LEFT JOIN product_categories c ON c.id = p.category_id
	WHERE ($1 <% p.name OR p.sku::text ILIKE $2 ESCAPE '\') AND p.is_active
	ORDER BY word_similarity($1, p.name) DESC, similarity(p.name, $1) DESC, p.name
	LIMIT $3`
	rows, err := r.pool.Query(ctx, q, query, escapeLikePattern(query)+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("search products %q: %w", query, err)
	}
	defer rows.Close()

	var out []CatalogResult
	for rows.Next() {
		var cr CatalogResult
		if err := rows.Scan(&cr.ProductID, &cr.SKU, &cr.Name, &cr.Category, &cr.BaseUOM); err != nil {
			return nil, fmt.Errorf("search products %q: %w", query, err)
		}
		out = append(out, cr)
	}
	return out, rows.Err()
}

// likeEscaper escapes the SQL LIKE/ILIKE metacharacters so a user search term is matched
// literally under `ESCAPE '\'`: backslash first (it is the escape char), then % and _ wildcards.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLikePattern neutralizes LIKE wildcards in a user-supplied term before it is used as an
// ILIKE prefix, so '%' and '_' cannot turn a prefix lookup into a wildcard scan.
func escapeLikePattern(s string) string { return likeEscaper.Replace(s) }

// ErrNotFound is returned when a product (or its category lookup) does not exist.
// Exported so the pricing module can map a not-found from the GetCategory catalog seam
// to its own 404 path (errors.Is), rather than letting it surface as a 500.
var ErrNotFound = errors.New("not found")

// looksLikeUUID reports whether s is a canonical 36-char 8-4-4-4-12 hex UUID (the shape of a
// products.id). It mirrors the order/audit/gl package validators so a malformed id can be rejected
// before it reaches the Postgres uuid PK (which would otherwise raise SQLSTATE 22P02 → HTTP 500).
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// GetCategory returns the product's category_id as text ("" when uncategorised).
// Catalog read seam for the pricing module (A-07); category_id lives on products,
// so this stays within this module's own table ownership.
func (r *Repository) GetCategory(ctx context.Context, productID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var cat *string
	err := r.pool.QueryRow(ctx, `SELECT category_id::text FROM products WHERE id = $1`, productID).Scan(&cat)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get category for %q: %w", productID, err)
	}
	if cat == nil {
		return "", nil
	}
	return *cat, nil
}

// GetBaseUOM returns products.base_uom (CITEXT NOT NULL) for the product. Catalog read
// seam for the pricing module (A-07); base_uom lives on products, so this stays within
// this module's own table ownership. ErrNotFound when the product does not exist.
func (r *Repository) GetBaseUOM(ctx context.Context, productID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var u string
	err := r.pool.QueryRow(ctx, `SELECT base_uom::text FROM products WHERE id = $1`, productID).Scan(&u)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get base uom for %q: %w", productID, err)
	}
	return u, nil
}

// ConvertUOM returns product_uoms.qty_in_base for (productID, fromUOM), how many
// base units one fromUOM represents (the base UOM is 1). Read as text to preserve
// DECIMAL(19,8) scale, then parsed. ErrNoConversion when the factor is absent.
func (r *Repository) ConvertUOM(ctx context.Context, productID, fromUOM string) (float64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var s string
	err := r.pool.QueryRow(ctx,
		`SELECT qty_in_base::text FROM product_uoms WHERE product_id = $1 AND uom_code = $2`,
		productID, fromUOM).Scan(&s)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNoConversion
		}
		return 0, fmt.Errorf("convert uom %q for %q: %w", fromUOM, productID, err)
	}
	f, perr := strconv.ParseFloat(s, 64)
	if perr != nil {
		return 0, fmt.Errorf("convert uom %q for %q: bad factor %q: %w", fromUOM, productID, s, perr)
	}
	return f, nil
}

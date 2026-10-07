// categoryread.go is the staff read of the product categories (the Quote Desk server reads,
// productListCategories, GET /api/v1/product-categories): id, code, name and is_active for the
// pricing screens, which hold category ids (the margin policy grid, the overrides) and need the
// names to show them. The table is this module's (0002), so the read lives here, beside the unit
// catalogue read it is modelled on, and the route is gated as the contract words it: Dealer Admin
// at read, the grant the pricing reads already use.
package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Category is one row of the category table as the staff read answers it. Retired categories are
// returned and marked, because a policy or an override can still name one and the screen has to be
// able to say which.
type Category struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
}

// CategoryReader is the category read the staff list runs through. The Repository is the concrete
// reader; the narrow interface keeps the Service honest about the one fact it reads.
type CategoryReader interface {
	// ListCategories answers at most limit rows of the org's categories in code order, strictly after
	// afterCode (the empty string from the top).
	ListCategories(ctx context.Context, orgID string, limit int, afterCode string) ([]Category, error)
}

// WithCategoryReader wires the category read the staff list serves through. A Service without one
// answers the route unavailable, never as an empty list of categories.
func (s *Service) WithCategoryReader(r CategoryReader) *Service { s.categories = r; return s }

// CategoriesPage answers one cursor page. The stable ordering key is the code, unique (0002), so the
// cursor is exact. The limit+1 walk decides whether a further page exists. The org is the caller's
// resolved org, never a request field.
func (s *Service) CategoriesPage(ctx context.Context, orgID string, limit int, afterCode string) ([]Category, string, error) {
	if s.categories == nil {
		return nil, "", errNoCategoryReader
	}
	if limit <= 0 {
		limit = httpx.DefaultPageLimit
	}
	if limit > httpx.MaxPageLimit {
		limit = httpx.MaxPageLimit
	}
	rows, err := s.categories.ListCategories(ctx, orgID, limit+1, afterCode)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[len(rows)-1].Code
	}
	return rows, next, nil
}

// errNoCategoryReader is the fail closed answer of a Service wired without the category read: the
// handler maps it to the unavailable family.
var errNoCategoryReader = errors.New("product: no category reader wired")

// ListCategories answers at most limit category rows in code order, strictly after afterCode. The
// org fence is the load writer's own (loadwriter.go): a row stamped to this org, or a row no org
// has been stamped on, and never another org's.
func (r *Repository) ListCategories(ctx context.Context, orgID string, limit int, afterCode string) ([]Category, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, code::text, name, is_active
		FROM product_categories
		WHERE deleted_at IS NULL
		  AND (org_id IS NULL OR org_id = $3::uuid)
		  AND ($1 = '' OR code > $1::citext)
		ORDER BY code
		LIMIT $2`, afterCode, limit, orgID)
	if err != nil {
		return nil, fmt.Errorf("product: category list: %w", err)
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.IsActive); err != nil {
			return nil, fmt.Errorf("product: category list scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("product: category list walk: %w", err)
	}
	return out, nil
}

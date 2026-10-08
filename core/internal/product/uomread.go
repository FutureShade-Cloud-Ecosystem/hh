// uomread.go is the staff read of the unit catalogue (HH-LOAD-1 LD-3,
// productListUnits, GET /api/v1/uoms): the catalogue's rows for the
// screens that will need them, Dealer Admin at read. The catalogue is
// this module's (0002, the deployment wide vocabulary of the plan's
// reading 3), so the read lives here; the route is registered with the
// product module's own routes and gated as the contract words it, the
// census carrying the dealer-admin surface annotation.
package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// Uom is one row of the unit catalogue as the staff read answers it.
type Uom struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Dimension string `json:"dimension"`
	IsActive  bool   `json:"is_active"`
}

// UomReader is the catalogue read the staff units list runs through.
// The Repository is the concrete reader; the narrow interface keeps the
// Service honest about the one fact it reads.
type UomReader interface {
	// ListUOMs answers at most limit rows of the catalogue in code
	// order, strictly after afterCode (the empty string from the top).
	ListUOMs(ctx context.Context, limit int, afterCode string) ([]Uom, error)
}

// WithUomReader wires the catalogue read the units list serves through.
// A Service without one answers the units route the fail closed way:
// the read cannot be served, so the handler refuses it unavailable,
// never as an empty catalogue.
func (s *Service) WithUomReader(r UomReader) *Service { s.uoms = r; return s }

// UnitsPage answers one cursor page of the catalogue. The stable
// ordering key is the code, the catalogue's own primary key: unique, so
// the cursor is exact, and CITEXT's order is the walk's order. The
// limit+1 walk decides whether a further page exists; the service owns
// that decision, the handler stays thin.
func (s *Service) UnitsPage(ctx context.Context, limit int, afterCode string) ([]Uom, string, error) {
	if s.uoms == nil {
		return nil, "", errNoUomReader
	}
	if limit <= 0 {
		limit = httpx.DefaultPageLimit
	}
	if limit > httpx.MaxPageLimit {
		limit = httpx.MaxPageLimit
	}
	rows, err := s.uoms.ListUOMs(ctx, limit+1, afterCode)
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

// errNoUomReader is the fail closed answer of a Service wired without
// the catalogue read: the route cannot serve, and the handler maps this
// to the unavailable family, never to an empty catalogue.
var errNoUomReader = errors.New("product: no uom reader wired")

// ListUOMs answers at most limit catalogue rows in code order, strictly
// after afterCode (the empty string from the top). The code is the
// stable ordering key, the catalogue's own primary key.
func (r *Repository) ListUOMs(ctx context.Context, limit int, afterCode string) ([]Uom, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT code::text, name, dimension, is_active
		FROM uoms
		WHERE ($1 = '' OR code > $1)
		ORDER BY code
		LIMIT $2`, afterCode, limit)
	if err != nil {
		return nil, fmt.Errorf("product: uom list: %w", err)
	}
	defer rows.Close()
	out := []Uom{}
	for rows.Next() {
		var u Uom
		if err := rows.Scan(&u.Code, &u.Name, &u.Dimension, &u.IsActive); err != nil {
			return nil, fmt.Errorf("product: uom list scan: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("product: uom list walk: %w", err)
	}
	return out, nil
}

// UnitCatalogueReader is the load doors' read of the catalogue: whether
// the catalogue carries one code, inside the caller's transaction. The
// supplier SKU door (HH-LOAD-1 LD-5, the purchasing module's writer)
// checks a pack unit through it, the narrow interface the module
// boundary rules ask for; a nil reader fails the check loudly at the
// first ask, never as a silent clear.
type UnitCatalogueReader struct{}

// UnitCodeKnownTx answers whether the catalogue carries the code.
func (UnitCatalogueReader) UnitCodeKnownTx(ctx context.Context, tx pgx.Tx, code string) (bool, error) {
	var known bool
	if err := tx.QueryRow(ctx, `SELECT true FROM uoms WHERE code = $1`, code).Scan(&known); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("product: unit catalogue code read: %w", err)
	}
	return true, nil
}

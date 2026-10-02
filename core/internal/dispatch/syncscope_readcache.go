// syncscope_readcache.go is the dispatch module's pull-only fleet
// Stores (HH-3.5 #169): trucks and drivers, the registry the board
// reads. The POST answers the contract's refusal at the route (Colton's
// START ruling); the readcache skeleton's write primitives fail
// closed. The frozen schema gives the two fleet tables no org column:
// their org fence is the branch (branch_id resolves through locations,
// the shared branch table every module reads for scoping), which is the
// same fence the module's own board reads use.
package dispatch

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// trucksColumns is the sync projection, p-qualified for the branch
// join.
const trucksColumns = `t.id::text, t.branch_id::text, t.code::text, t.name, t.truck_type,
	t.capacity_kg::text, t.is_active, t.created_at, t.updated_at,
	t.seq, (t.deleted_at IS NOT NULL) AS deleted`

// truckRow is the wire shape of one cached truck row.
type truckRow struct {
	ID         string    `json:"id"`
	BranchID   string    `json:"branch_id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	TruckType  string    `json:"truck_type"`
	CapacityKg *string   `json:"capacity_kg"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// scanTruckRow reads the 11-target row and projects it.
func scanTruckRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r truckRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.BranchID, &r.Code, &r.Name, &r.TruckType, &r.CapacityKg,
		&r.IsActive, &r.CreatedAt, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("dispatch: trucks projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("dispatch: trucks projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// driversColumns is the sync projection, d-qualified for the branch
// join.
const driversColumns = `d.id::text, d.branch_id::text, COALESCE(d.user_id::text,''), d.name,
	d.phone, d.licence_class, d.is_active, d.created_at, d.updated_at,
	d.seq, (d.deleted_at IS NOT NULL) AS deleted`

// driverRow is the wire shape of one cached driver row.
type driverRow struct {
	ID           string    `json:"id"`
	BranchID     string    `json:"branch_id"`
	UserID       string    `json:"user_id"`
	Name         string    `json:"name"`
	Phone        string    `json:"phone"`
	LicenceClass string    `json:"licence_class"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// scanDriverRow reads the 11-target row and projects it.
func scanDriverRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r driverRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.BranchID, &r.UserID, &r.Name, &r.Phone, &r.LicenceClass,
		&r.IsActive, &r.CreatedAt, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("dispatch: drivers projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("dispatch: drivers projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// fleetPredicate is the org predicate for the fleet scopes.
func fleetPredicate(f sync.ScopeFilter) ([]any, bool) {
	if f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// NewTrucksStore builds the trucks Store.
func NewTrucksStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + trucksColumns + ` FROM dispatch_trucks t
		 JOIN locations l ON l.id = t.branch_id
		 WHERE l.org_id = $1::uuid AND t.seq IS NOT NULL AND t.seq > $2
		 ORDER BY t.seq ASC LIMIT $3`,
		ByID: `SELECT ` + trucksColumns + ` FROM dispatch_trucks t
		 JOIN locations l ON l.id = t.branch_id
		 WHERE l.org_id = $1::uuid AND t.id = $2::uuid`,
		Args: fleetPredicate,
		Scan: scanTruckRow,
	})
}

// NewDriversStore builds the drivers Store.
func NewDriversStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + driversColumns + ` FROM dispatch_drivers d
		 JOIN locations l ON l.id = d.branch_id
		 WHERE l.org_id = $1::uuid AND d.seq IS NOT NULL AND d.seq > $2
		 ORDER BY d.seq ASC LIMIT $3`,
		ByID: `SELECT ` + driversColumns + ` FROM dispatch_drivers d
		 JOIN locations l ON l.id = d.branch_id
		 WHERE l.org_id = $1::uuid AND d.id = $2::uuid`,
		Args: fleetPredicate,
		Scan: scanDriverRow,
	})
}

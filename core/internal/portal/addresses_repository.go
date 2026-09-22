package portal

// addresses_repository.go, the pgx read behind GET /addresses. Lives on *projectRepository, which
// is the portal's already-composed, pool-backed, tenant-scoped store (see projects.go): the sites
// read joins the SAME tables that store already owns (projects, orders) plus two columns beside
// them, and standing up a second pool-backed repository to read them would have bought a
// composition-root edit and nothing else.

import (
	"context"
	"strings"
	"time"
)

// listAddressesSQL unions the account's three real sources of sites. Read it as three independent
// tenant-scoped SELECTs, because that is what it is, each one carries customer_id itself, so no
// arm of the union can be widened by a mistake in another.
//
//	arm 1  the account's own address                     (customers.billing_address, JSONB)
//	arm 2  the jobs board's sites                        (projects.job_address, free text)
//	arm 3  sites this account's orders were delivered to (delivery_stops via orders)
//
// Arm 3 is DISTINCT ON the normalised address so twelve deliveries to one driveway are one site,
// and the row that survives is the most recently delivered one, the id and the timestamp a picker
// wants. It is scoped through orders.customer_id: a stop is reachable only via an order that
// belongs to the caller, which is the same fence orders.go uses and the reason a dispatch table can
// be read here without exposing anybody else's geography.
//
// Ordering is deterministic and useful in that order: delivery sites first (somebody has physically
// been there), most recently used first, then job sites alphabetically, then the account address.
const listAddressesSQL = `
WITH billing AS (
    SELECT 'billing:' || c.id::text                        AS id,
           'billing'                                       AS kind,
           c.name                                          AS label,
           COALESCE(c.billing_address->>'line1', '')       AS line1,
           COALESCE(c.billing_address->>'line2', '')       AS line2,
           COALESCE(c.billing_address->>'city', '')        AS city,
           COALESCE(c.billing_address->>'province', '')    AS province,
           COALESCE(c.billing_address->>'postal', '')      AS postal,
           NULL::timestamptz                               AS last_used_at,
           3                                               AS kind_rank
      FROM customers c
     WHERE c.id = $1::uuid
       AND c.billing_address <> '{}'::jsonb
),
job_sites (id, kind, label, line1, line2, city, province, postal, last_used_at, kind_rank) AS (
    -- projects.job_address is free TEXT (migration 0048), so the whole string lands in line1
    -- rather than being guessed apart into city/postal. A parser that splits "42 Riverside Dr,
    -- Trenton, ON" correctly nine times out of ten produces one wrong delivery address per ten
    -- jobs, and the tenth one is a truck in the wrong driveway.
    SELECT 'project:' || p.id::text, 'job_site', p.name,
           btrim(p.job_address), '', '', '', '',
           NULL::timestamptz, 2
      FROM projects p
     WHERE p.customer_id = $1::uuid
       AND btrim(p.job_address) <> ''
),
delivery_sites (id, kind, label, line1, line2, city, province, postal, last_used_at, kind_rank) AS (
    SELECT DISTINCT ON (lower(btrim(ds.address_line)), lower(btrim(ds.city)), lower(btrim(ds.postal_code)))
           'site:' || ds.id::text, 'delivery',
           COALESCE(NULLIF(btrim(ds.contact_name), ''), btrim(ds.address_line)),
           btrim(ds.address_line), '', btrim(ds.city), '', btrim(ds.postal_code),
           ds.delivered_at, 1
      FROM delivery_stops ds
      JOIN orders o ON o.id = ds.order_id
     WHERE o.customer_id = $1::uuid
       AND btrim(ds.address_line) <> ''
     ORDER BY lower(btrim(ds.address_line)), lower(btrim(ds.city)), lower(btrim(ds.postal_code)),
              ds.delivered_at DESC NULLS LAST, ds.id
)
SELECT id, kind, label, line1, line2, city, province, postal, last_used_at
  FROM (
        SELECT id, kind, label, line1, line2, city, province, postal, last_used_at, kind_rank FROM delivery_sites
        UNION ALL
        SELECT id, kind, label, line1, line2, city, province, postal, last_used_at, kind_rank FROM job_sites
        UNION ALL
        SELECT id, kind, label, line1, line2, city, province, postal, last_used_at, kind_rank FROM billing
       ) sites
 ORDER BY kind_rank, last_used_at DESC NULLS LAST, label, id`

// ListAddresses returns every delivery site of ONE customer. customerID is a bound parameter in
// every arm of the union, it is never interpolated and never optional.
func (r *projectRepository) ListAddresses(ctx context.Context, customerID string) ([]Address, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, listAddressesSQL, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Address
	for rows.Next() {
		var a Address
		var lastUsed *time.Time
		if err := rows.Scan(&a.ID, &a.Kind, &a.Label, &a.Line1, &a.Line2,
			&a.City, &a.Province, &a.Postal, &lastUsed); err != nil {
			return nil, err
		}
		if lastUsed != nil {
			utc := lastUsed.UTC()
			a.LastUsedAt = &utc
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAddress resolves one site by id, WITHIN the account.
//
// It resolves against the account's own list rather than parsing the synthetic id apart and issuing
// a per-kind lookup. Two reasons, and the second is the load-bearing one:
//
//  1. There is then exactly ONE definition of "this account's sites", a get can never return
//     something the list does not show, or miss something it does.
//  2. The tenant fence is not re-implemented. A second, id-shaped query is a second place to forget
//     the customer_id predicate, and that omission is precisely the IDOR this route is written to
//     be immune to. Here there is no second query to forget it in.
//
// The list is an account's delivery sites, tens of rows, not thousands, so the scan is free.
func (r *projectRepository) GetAddress(ctx context.Context, customerID, addressID string) (Address, error) {
	addressID = strings.TrimSpace(addressID)
	if addressID == "" {
		return Address{}, ErrAddressNotFound
	}
	list, err := r.ListAddresses(ctx, customerID)
	if err != nil {
		return Address{}, err
	}
	for _, a := range list {
		if a.ID == addressID {
			return a, nil
		}
	}
	// Unknown id, malformed id, and ANOTHER TENANT'S id are one answer. A distinct "forbidden"
	// here would confirm the row exists, which is the fact the fence exists to withhold.
	return Address{}, ErrAddressNotFound
}

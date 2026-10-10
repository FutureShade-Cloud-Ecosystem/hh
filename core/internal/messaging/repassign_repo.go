package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// - DIB-273/DIB-333/DIB-271 persistence: rep_assignments + the house queue -
//
// These methods own the 0136 tables. They read customers/orders/users/locations ONLY for display
// joins and branch resolution, the same identity-is-SoT read seam the rest of this repository uses
// (SyncMembership reads users/user_roles/user_branch_grants); every WRITE stays inside the
// rep_assignments / msg_* namespace.

// RepAssignmentsForCustomer returns EVERY assignment row for one customer, the customer-level row and
// any per-order overrides, in one query. The RULING's resolution order is applied in Go
// (resolveAssignment), not in SQL, so the ruling lives in exactly one reviewable place and reversing
// it never requires touching a query. The order-override rows sort first purely for display stability.
func (r *Repository) RepAssignmentsForCustomer(ctx context.Context, customerID string) ([]RepAssignment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ra.id::text, ra.customer_id::text, COALESCE(c.name,''),
		       COALESCE(ra.order_id::text,''), COALESCE(o.order_no,''),
		       ra.rep_user_id::text, COALESCE(u.display_name,''),
		       ra.source, COALESCE(ra.assigned_by::text,''), ra.assigned_at, ra.note
		FROM rep_assignments ra
		LEFT JOIN customers c ON c.id = ra.customer_id
		LEFT JOIN orders o ON o.id = ra.order_id
		LEFT JOIN users u ON u.id = ra.rep_user_id
		WHERE ra.customer_id = $1
		ORDER BY (ra.order_id IS NULL), ra.assigned_at DESC`, customerID)
	if err != nil {
		return nil, fmt.Errorf("rep assignments for customer: %w", err)
	}
	defer rows.Close()
	return scanRepAssignments(rows)
}

// ListRepAssignments is the admin/rep list read model. Both filters are optional ("" = no constraint),
// matching the audit/export filter convention.
func (r *Repository) ListRepAssignments(ctx context.Context, repUserID, customerID string) ([]RepAssignment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ra.id::text, ra.customer_id::text, COALESCE(c.name,''),
		       COALESCE(ra.order_id::text,''), COALESCE(o.order_no,''),
		       ra.rep_user_id::text, COALESCE(u.display_name,''),
		       ra.source, COALESCE(ra.assigned_by::text,''), ra.assigned_at, ra.note
		FROM rep_assignments ra
		LEFT JOIN customers c ON c.id = ra.customer_id
		LEFT JOIN orders o ON o.id = ra.order_id
		LEFT JOIN users u ON u.id = ra.rep_user_id
		WHERE ($1 = '' OR ra.rep_user_id = $1::uuid)
		  AND ($2 = '' OR ra.customer_id = $2::uuid)
		ORDER BY COALESCE(c.name,''), (ra.order_id IS NULL) DESC, ra.assigned_at DESC
		LIMIT 500`, repUserID, customerID)
	if err != nil {
		return nil, fmt.Errorf("list rep assignments: %w", err)
	}
	defer rows.Close()
	return scanRepAssignments(rows)
}

func scanRepAssignments(rows pgx.Rows) ([]RepAssignment, error) {
	out := []RepAssignment{}
	for rows.Next() {
		var a RepAssignment
		if err := rows.Scan(&a.ID, &a.CustomerID, &a.CustomerName, &a.OrderID, &a.OrderNo,
			&a.RepUserID, &a.RepName, &a.Source, &a.AssignedBy, &a.AssignedAt, &a.Note); err != nil {
			return nil, fmt.Errorf("scan rep assignment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertRepAssignment writes the customer-level row (OrderID empty) or the per-order override
// (OrderID set). Both shapes upsert on their own partial unique index (0136), so re-assigning a
// customer MOVES the book to the new rep rather than creating a second, ambiguous row, there is
// never more than one answer to "who owns this customer".
//
// The two shapes need two statements because Postgres cannot target a partial unique index whose
// predicate differs per row from one ON CONFLICT clause.
func (r *Repository) UpsertRepAssignment(ctx context.Context, a RepAssignment) (RepAssignment, error) {
	if r.pool == nil {
		return RepAssignment{}, errNoDB
	}
	var (
		id  string
		at  time.Time
		err error
	)
	assignedBy := any(nil)
	if a.AssignedBy != "" {
		assignedBy = a.AssignedBy
	}
	if a.OrderID == "" {
		err = r.pool.QueryRow(ctx, `
			INSERT INTO rep_assignments (customer_id, order_id, rep_user_id, source, assigned_by, note)
			VALUES ($1, NULL, $2, $3, $4, $5)
			ON CONFLICT (customer_id) WHERE order_id IS NULL DO UPDATE
			  SET rep_user_id = EXCLUDED.rep_user_id,
			      source      = EXCLUDED.source,
			      assigned_by = EXCLUDED.assigned_by,
			      note        = EXCLUDED.note,
			      updated_at  = now()
			RETURNING id::text, assigned_at`,
			a.CustomerID, a.RepUserID, a.Source, assignedBy, a.Note).Scan(&id, &at)
	} else {
		err = r.pool.QueryRow(ctx, `
			INSERT INTO rep_assignments (customer_id, order_id, rep_user_id, source, assigned_by, note)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (order_id) WHERE order_id IS NOT NULL DO UPDATE
			  SET rep_user_id = EXCLUDED.rep_user_id,
			      customer_id = EXCLUDED.customer_id,
			      source      = EXCLUDED.source,
			      assigned_by = EXCLUDED.assigned_by,
			      note        = EXCLUDED.note,
			      updated_at  = now()
			RETURNING id::text, assigned_at`,
			a.CustomerID, a.OrderID, a.RepUserID, a.Source, assignedBy, a.Note).Scan(&id, &at)
	}
	if err != nil {
		return RepAssignment{}, fmt.Errorf("upsert rep assignment: %w", err)
	}
	a.ID, a.AssignedAt = id, at
	return a, nil
}

// DeleteRepAssignment removes the customer-level row (orderID empty) or one order override. Returns
// the number of rows removed so the caller can 404 an unassign of something never assigned.
//
// Deleting the CUSTOMER-LEVEL row is exactly the "unassign → house queue" path: with no row left,
// resolveAssignment yields LevelNone and the next inbound routes to the house queue.
func (r *Repository) DeleteRepAssignment(ctx context.Context, customerID, orderID string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var ct int64
	if orderID == "" {
		tag, err := r.pool.Exec(ctx,
			`DELETE FROM rep_assignments WHERE customer_id = $1 AND order_id IS NULL`, customerID)
		if err != nil {
			return 0, fmt.Errorf("delete rep assignment: %w", err)
		}
		ct = tag.RowsAffected()
	} else {
		tag, err := r.pool.Exec(ctx,
			`DELETE FROM rep_assignments WHERE customer_id = $1 AND order_id = $2`, customerID, orderID)
		if err != nil {
			return 0, fmt.Errorf("delete rep assignment: %w", err)
		}
		ct = tag.RowsAffected()
	}
	return int(ct), nil
}

// RepDMChannelExists reports whether the rep's customer-message DM channel is already provisioned
// WITH its member row (the seeded shape). The persona seeding guards on this before calling
// EnsureRepDMChannel, whose upsert DO UPDATE arm touches the row's updated_at on every call, a touch
// the demo populate's write-nothing discipline refuses.
func (r *Repository) RepDMChannelExists(ctx context.Context, userID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var displayName, email string
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(display_name,''), COALESCE(email::text,'') FROM users WHERE id = $1`, userID).
		Scan(&displayName, &email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("resolve rep: %w", err)
	}
	handle := displayName
	if handle == "" {
		handle, _, _ = strings.Cut(email, "@")
	}
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM msg_channels ch
		  JOIN msg_members m ON m.channel_id = ch.id AND m.user_id = $1
		  WHERE ch.slug = $2)`,
		userID, repDMSlug(handle, userID)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("rep dm channel exists: %w", err)
	}
	return exists, nil
}

// EnsureRepDMChannel lazily + idempotently provisions the rep's customer-message DM channel and
// returns its id. It is EnsureApprovalChannel's twin (0075) and deliberately so: same slug discipline
// (userID-suffixed so two reps sharing a display name never share a channel), same DO NOTHING
// convergence with the wrong-kind refusal (a row of another kind holding the slug is a squat, never
// adopted), same EXPLICIT membership so the FR-1 derived-membership prune never garbage-collects
// the rep out of their own inbox.
func (r *Repository) EnsureRepDMChannel(ctx context.Context, userID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var displayName, email string
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(display_name,''), COALESCE(email::text,'') FROM users WHERE id = $1`, userID).
		Scan(&displayName, &email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", fmt.Errorf("resolve rep: %w", err)
	}
	handle := displayName
	if handle == "" {
		handle, _, _ = strings.Cut(email, "@")
	}
	slug := repDMSlug(handle, userID)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var channelID string
	// DO NOTHING (not DO UPDATE): the conflict arm decides whether the row
	// this provision converges on is its own (idempotent re-run) or a
	// squat of another kind, which is refused loudly, never adopted.
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, topic, tier)
		VALUES ('DM', $1, $2, 'Customer messages routed to you', 'STANDARD')
		ON CONFLICT (slug) DO NOTHING
		RETURNING id::text`, slug, "Customers, "+handle).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		var kind string
		kerr := tx.QueryRow(ctx,
			`SELECT kind FROM msg_channels WHERE slug = $1`, slug).Scan(&kind)
		if errors.Is(kerr, pgx.ErrNoRows) {
			return "", errNotFound // the conflicting row vanished mid-flight
		}
		if kerr != nil {
			return "", fmt.Errorf("load conflicting rep dm channel: %w", kerr)
		}
		if kind != KindDM {
			// The slug embeds this rep's own user id and this provision is
			// its only rightful writer: a row of another kind is a squat,
			// and seating the rep into it would route the rep's customers
			// into a room someone else owns and reads.
			return "", fmt.Errorf("ensure rep dm channel: %w", ErrChannelSlugTaken)
		}
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM msg_channels WHERE slug = $1`, slug).Scan(&channelID); err != nil {
			return "", fmt.Errorf("load rep dm channel: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("ensure rep dm channel: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT')
		ON CONFLICT (channel_id, user_id) DO NOTHING`, channelID, userID); err != nil {
		return "", fmt.Errorf("ensure rep dm member: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return channelID, nil
}

// BranchHouseSlug returns the house-queue channel slug for a branch (DIB-271). The house channel is
// picked by WHAT MAKES IT ONE, never by a slug pattern a caller can found into: the slug is the
// branch's own house name, derived through the same branch map the yard rooms resolve through
// (yard-trenton to house-trenton, so one branch to name mapping serves both families), and the row
// must be the system's (kind TEAM, auto_scope set: every seeded system room carries auto_scope and
// no caller founded room ever does). A caller shaped house- row therefore cannot be picked whatever
// its name sorts like, and the house- family is reserved against caller founding beside the other
// system families, so the route cannot write one either.
//
// The naming contract for a new branch's house room: its slug is exactly house-<lower(locations.code)>
// (a branch coded NWB is house-nwb), unless BranchYardSlug's map names the branch (TRT is
// house-trenton, KGN is house-kingston). The room must be a TEAM row naming the branch with auto_scope
// set. It is NOT named by the city, as 0136's comment on the derivation reads; 0136 cannot be edited,
// and the two seeded rooms are the only ones the map renames. A house room seeded under any other name
// is skipped and its inbound lands in #house-unassigned (never dropped). No Go change is needed for a
// new branch once its room is seeded under that name.
//
// A branch with no house channel, and an inbound with no branch at all, falls back to the GLOBAL
// #house-unassigned floor. The fallback is the point: an inbound customer message is NEVER dropped
// because routing could not find a home.
func (r *Repository) BranchHouseSlug(ctx context.Context, branchID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if branchID == "" {
		return houseSlugUnassigned, nil
	}
	yard, err := r.BranchYardSlug(ctx, branchID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return houseSlugUnassigned, nil
		}
		return "", err
	}
	house := "house-" + strings.TrimPrefix(yard, "yard-")
	var slug string
	err = r.pool.QueryRow(ctx, `
		SELECT slug::text FROM msg_channels
		WHERE slug = $1 AND branch_id = $2 AND kind = 'TEAM' AND auto_scope IS NOT NULL AND is_archived = FALSE`,
		house, branchID).Scan(&slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return houseSlugUnassigned, nil
		}
		return "", fmt.Errorf("resolve house slug: %w", err)
	}
	return slug, nil
}

// CustomerFacts resolves a customer's display name + home branch, the two things routing needs that
// live outside msg_*. Returns errNotFound for an unknown customer so an inbound naming a bogus
// customer is a 404 rather than a message routed into the void.
func (r *Repository) CustomerFacts(ctx context.Context, customerID string) (name, branchID string, err error) {
	if r.pool == nil {
		return "", "", errNoDB
	}
	err = r.pool.QueryRow(ctx,
		`SELECT COALESCE(name,''), COALESCE(home_branch_id::text,'') FROM customers WHERE id = $1`,
		customerID).Scan(&name, &branchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errNotFound
		}
		return "", "", fmt.Errorf("resolve customer: %w", err)
	}
	return name, branchID, nil
}

// - DIB-271: the house-queue conversation ledger -

// UpsertHouseConversation records that a customer's message landed in a house channel, bumping the
// last-seen stamp and the counter. It is the queue's READ model only, it never decides routing.
// A conversation that was already CLAIMED keeps its claim (the claim is sticky), so a late-arriving
// message on a claimed conversation does not re-open it.
func (r *Repository) UpsertHouseConversation(ctx context.Context, customerID, branchID, channelID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	branch := any(nil)
	if branchID != "" {
		branch = branchID
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_house_conversations (customer_id, branch_id, channel_id, message_count)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (customer_id, channel_id) DO UPDATE
		  SET last_at = now(),
		      message_count = msg_house_conversations.message_count + 1
		RETURNING id::text`, customerID, branch, channelID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert house conversation: %w", err)
	}
	return id, nil
}

// ListHouseConversations returns a house channel's queue, unclaimed first then most recent. openOnly
// narrows it to the working set.
func (r *Repository) ListHouseConversations(ctx context.Context, channelID string, openOnly bool) ([]HouseConversation, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT hc.id::text, hc.customer_id::text, COALESCE(c.name,''), COALESCE(hc.branch_id::text,''),
		       hc.channel_id::text, hc.first_at, hc.last_at, hc.message_count,
		       COALESCE(hc.claimed_by::text,''), COALESCE(u.display_name,''), hc.claimed_at
		FROM msg_house_conversations hc
		LEFT JOIN customers c ON c.id = hc.customer_id
		LEFT JOIN users u ON u.id = hc.claimed_by
		WHERE hc.channel_id = $1 AND (NOT $2 OR hc.claimed_by IS NULL)
		ORDER BY (hc.claimed_by IS NULL) DESC, hc.last_at DESC
		LIMIT 200`, channelID, openOnly)
	if err != nil {
		return nil, fmt.Errorf("list house conversations: %w", err)
	}
	defer rows.Close()
	out := []HouseConversation{}
	for rows.Next() {
		var c HouseConversation
		var claimedAt *time.Time
		if err := rows.Scan(&c.ID, &c.CustomerID, &c.CustomerName, &c.BranchID, &c.ChannelID,
			&c.FirstAt, &c.LastAt, &c.MessageCount, &c.ClaimedBy, &c.ClaimedByName, &claimedAt); err != nil {
			return nil, fmt.Errorf("scan house conversation: %w", err)
		}
		c.ClaimedAt = claimedAt
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetHouseConversation loads one conversation (for the claim authz + the post-claim response).
func (r *Repository) GetHouseConversation(ctx context.Context, id string) (HouseConversation, error) {
	if r.pool == nil {
		return HouseConversation{}, errNoDB
	}
	var c HouseConversation
	var claimedAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT hc.id::text, hc.customer_id::text, COALESCE(c.name,''), COALESCE(hc.branch_id::text,''),
		       hc.channel_id::text, hc.first_at, hc.last_at, hc.message_count,
		       COALESCE(hc.claimed_by::text,''), COALESCE(u.display_name,''), hc.claimed_at
		FROM msg_house_conversations hc
		LEFT JOIN customers c ON c.id = hc.customer_id
		LEFT JOIN users u ON u.id = hc.claimed_by
		WHERE hc.id = $1`, id).
		Scan(&c.ID, &c.CustomerID, &c.CustomerName, &c.BranchID, &c.ChannelID,
			&c.FirstAt, &c.LastAt, &c.MessageCount, &c.ClaimedBy, &c.ClaimedByName, &claimedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HouseConversation{}, errNotFound
		}
		return HouseConversation{}, fmt.Errorf("get house conversation: %w", err)
	}
	c.ClaimedAt = claimedAt
	return c, nil
}

// ClaimHouseConversation is the DIB-271 claim, done as ONE transaction over BOTH tables: it stamps the
// queue row AND writes the customer-level rep_assignments row that makes the claim STICKY. The two
// must not be able to diverge, a stamped-but-unassigned conversation would keep routing to the house
// queue while the UI showed it claimed, which is the exact confusion the queue exists to remove.
//
// The claim stamp uses a CONDITIONAL update (claimed_by IS NULL) so two reps racing on the same
// conversation resolve deterministically: the first commits, the second gets zero rows and
// ErrAlreadyClaimed (409), never a silent steal.
func (r *Repository) ClaimHouseConversation(ctx context.Context, conversationID, userID string) (HouseConversation, error) {
	if r.pool == nil {
		return HouseConversation{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return HouseConversation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var customerID string
	err = tx.QueryRow(ctx, `
		UPDATE msg_house_conversations
		SET claimed_by = $2, claimed_at = now()
		WHERE id = $1 AND claimed_by IS NULL
		RETURNING customer_id::text`, conversationID, userID).Scan(&customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either the row is gone (404) or someone already claimed it (409), distinguish, so the UI
			// can tell "vanished" from "lost the race". The probe reads through tx: asking the shared
			// pool for a second connection while tx holds the first starves the pool when pool-size
			// losers are in flight together.
			var exists bool
			if e2 := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM msg_house_conversations WHERE id = $1)`, conversationID).Scan(&exists); e2 == nil && exists {
				return HouseConversation{}, ErrAlreadyClaimed
			}
			return HouseConversation{}, errNotFound
		}
		return HouseConversation{}, fmt.Errorf("claim conversation: %w", err)
	}

	// The claim IS the assignment (customer-level, source CLAIM), this is what makes it sticky.
	if _, err := tx.Exec(ctx, `
		INSERT INTO rep_assignments (customer_id, order_id, rep_user_id, source, assigned_by, note)
		VALUES ($1, NULL, $2, 'CLAIM', $2, 'Claimed from the house queue')
		ON CONFLICT (customer_id) WHERE order_id IS NULL DO UPDATE
		  SET rep_user_id = EXCLUDED.rep_user_id,
		      source      = 'CLAIM',
		      assigned_by = EXCLUDED.assigned_by,
		      note        = EXCLUDED.note,
		      updated_at  = now()`, customerID, userID); err != nil {
		return HouseConversation{}, fmt.Errorf("claim assignment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return HouseConversation{}, err
	}
	return r.GetHouseConversation(ctx, conversationID)
}

package dispatch

// repository_freightraise.go, persistence for the parked freight raise (0195).
//
// EVERY WRITE IN THIS FILE CAN RUN ON A CALLER'S TRANSACTION, and that is the whole reason it is a
// separate file. The apply half of this feature does not run on dispatch's own connection: it runs
// inside the transaction identity opens to stamp an approval decision, so that the split and the
// APPROVED stamp commit or roll back together. A repository whose methods can only reach a pool
// cannot participate in somebody else's atomicity, and "post first, stamp after" is the exact bug
// 0075 closed for journal entries.
//
// So the file is written against `runner`, the three-method subset both *pgxpool.Pool and pgx.Tx
// satisfy, and the existing pool-shaped entry points delegate to the same bodies. There is still
// ONE statement per fact; what varies is which connection executes it.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// runner is the subset of pgx that a pool and a transaction both satisfy. Declared here rather than
// imported from pgx because pgx has no such named type, and declaring it at the point of use states
// exactly which three capabilities this file depends on.
type runner interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// errNoRaise, no such parked raise, or one that is gone. Kept distinct from ErrNotFound so the
// decide bridge can tell "the proposal vanished" from "the order vanished".
var errNoRaise = errors.New("dispatch: no such freight raise")

const raiseCols = `id::text, order_id::text, branch_id::text, order_no, customer_name,
       quoted_freight_cents, current_freight_cents, proposed_freight_cents, loads, amounts_cents,
       requested_by::text, requested_by_role, requested_at, state,
       COALESCE(approval_id::text,''), COALESCE(decided_by::text,''), decided_at`

func scanRaise(row pgx.Row) (FreightRaise, error) {
	var r FreightRaise
	err := row.Scan(&r.ID, &r.OrderID, &r.BranchID, &r.OrderNo, &r.CustomerName,
		&r.QuotedCents, &r.CurrentCents, &r.ProposedCents, &r.Loads, &r.Amounts,
		&r.RequestedBy, &r.RequestedByRole, &r.RequestedAt, &r.State,
		&r.ApprovalID, &r.DecidedBy, &r.DecidedAt)
	return r, err
}

// InsertFreightRaise parks one proposal. It writes no decision and no approval id: this row is inert
// until the queue speaks, and the approval it belongs to is attached in the next breath.
//
// A unique violation here is the one-parked-raise-per-order index doing its job under a race. It is
// returned raw so the service can turn it into the sentence a dispatcher can act on; a 500 carrying
// an index name would tell them nothing and tell an attacker the schema.
func (r *Repository) InsertFreightRaise(ctx context.Context, in FreightRaise) (FreightRaise, error) {
	pool, err := r.db()
	if err != nil {
		return FreightRaise{}, err
	}
	row := pool.QueryRow(ctx, `
INSERT INTO delivery_freight_raises
  (order_id, branch_id, order_no, customer_name, quoted_freight_cents, current_freight_cents,
   proposed_freight_cents, loads, amounts_cents, requested_by, requested_by_role)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING `+raiseCols,
		in.OrderID, in.BranchID, in.OrderNo, in.CustomerName, in.QuotedCents, in.CurrentCents,
		in.ProposedCents, in.Loads, in.Amounts, in.RequestedBy, in.RequestedByRole)
	return scanRaise(row)
}

// ParkedRaiseForOrder reads the one live proposal an order may have. found=false is not an error: an
// order with nothing parked is the normal case, and a read path that returns an error for "nothing
// here" makes every caller write the same unwrapping.
func (r *Repository) ParkedRaiseForOrder(ctx context.Context, orderID string) (FreightRaise, bool, error) {
	pool, err := r.db()
	if err != nil {
		return FreightRaise{}, false, err
	}
	raise, err := scanRaise(pool.QueryRow(ctx,
		`SELECT `+raiseCols+` FROM delivery_freight_raises WHERE order_id = $1 AND state = 'PARKED'`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return FreightRaise{}, false, nil
	}
	if err != nil {
		return FreightRaise{}, false, fmt.Errorf("parked freight raise: %w", err)
	}
	return raise, true, nil
}

// GetFreightRaise reads one proposal by id, the card read behind the approvals queue.
func (r *Repository) GetFreightRaise(ctx context.Context, id string) (FreightRaise, error) {
	pool, err := r.db()
	if err != nil {
		return FreightRaise{}, err
	}
	raise, err := scanRaise(pool.QueryRow(ctx, `SELECT `+raiseCols+` FROM delivery_freight_raises WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return FreightRaise{}, errNoRaise
	}
	return raise, err
}

// AttachRaiseApproval records which approval row decides this proposal. It is a second statement
// rather than a column on the INSERT because the approval cannot exist before its subject does, the
// request's subject_ref IS this row's id, which is the same ordering the JE park-bridge uses.
func (r *Repository) AttachRaiseApproval(ctx context.Context, raiseID, approvalID string) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`UPDATE delivery_freight_raises SET approval_id = $2 WHERE id = $1 AND state = 'PARKED'`, raiseID, approvalID)
	return err
}

// DeleteParkedRaise unwinds a proposal whose approval could not be filed.
//
// IT IS SCOPED TO 'PARKED' AND TO NOTHING ELSE MATTERS. A row that has been decided is evidence for a
// figure on a customer's invoice and must never be removable by a code path whose job is to clean up
// after a failed filing, so the WHERE names the state as well as the id, and a decided row survives
// a mis-aimed call.
func (r *Repository) DeleteParkedRaise(ctx context.Context, raiseID string) error {
	pool, err := r.db()
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `DELETE FROM delivery_freight_raises WHERE id = $1 AND state = 'PARKED'`, raiseID)
	return err
}

// LockFreightRaise reads a proposal FOR UPDATE on the caller's transaction, the first thing the
// apply does, so two deciders arriving together are serialized on the row rather than both reading
// PARKED and both applying.
func (r *Repository) LockFreightRaise(ctx context.Context, q runner, id string) (FreightRaise, error) {
	raise, err := scanRaise(q.QueryRow(ctx,
		`SELECT `+raiseCols+` FROM delivery_freight_raises WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return FreightRaise{}, errNoRaise
	}
	return raise, err
}

// MarkFreightRaiseDecided moves a PARKED proposal to APPLIED or REFUSED and stamps who decided it.
//
// The WHERE names the CURRENT state, so this is a compare-and-swap and not a blind write: applied=false
// means somebody else got there first, which the caller turns into a conflict rather than continuing
// to write freight on the strength of a decision that had already been made.
func (r *Repository) MarkFreightRaiseDecided(ctx context.Context, q runner, id, state, decidedBy string, at time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `
UPDATE delivery_freight_raises
   SET state = $2, decided_by = NULLIF($3,'')::uuid, decided_at = $4
 WHERE id = $1 AND state = 'PARKED'`, id, state, decidedBy, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// - the load-side writes, on either connection -

// loadsForOrderOn is LoadsForOrder's body. LoadsForOrder is the pool-shaped entry point that has
// always existed; the apply path calls this one with the decide transaction so it reads the loads
// under the same lock ordering it is about to write them under.
func (r *Repository) loadsForOrderOn(ctx context.Context, q runner, orderID string) ([]Stop, error) {
	rows, err := q.Query(ctx, stopSelect+
		` WHERE s.order_id = $1 AND s.status NOT IN ('CANCELLED','FAILED') ORDER BY s.load_seq`, orderID)
	if err != nil {
		return nil, fmt.Errorf("loads for order: %w", err)
	}
	defer rows.Close()
	out := []Stop{}
	for rows.Next() {
		st, err := scanStop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// LoadsForOrderTx is the exported tx-shaped read the apply path uses.
func (r *Repository) LoadsForOrderTx(ctx context.Context, q runner, orderID string) ([]Stop, error) {
	return r.loadsForOrderOn(ctx, q, orderID)
}

// LockLoadsForOrderTx takes the order's live load rows, ascending id, FOR NO KEY UPDATE, on the caller's
// transaction (PR 556 round 3d). The raise apply mints loads (an INSERT whose stops counter draw the sync seq
// trigger makes and the transaction holds to commit) BEFORE the split writes the stops: without these locks first
// it would hold the counter while it waits for a stop row a single stop write holds between its own lock and its
// draw. Rows first, then the counter. The loads it mints later are the transaction's own rows.
func (r *Repository) LockLoadsForOrderTx(ctx context.Context, q runner, orderID string) error {
	if _, err := q.Exec(ctx,
		`SELECT 1 FROM delivery_stops WHERE order_id = $1 AND status NOT IN ('CANCELLED','FAILED') ORDER BY id FOR NO KEY UPDATE`,
		orderID); err != nil {
		return fmt.Errorf("lock the order's loads: %w", err)
	}
	return nil
}

// insertSplitLoadOn mints the Nth load on either connection, returning the new id. It is the same
// statement InsertSplitLoad has always run; only the executor varies.
func (r *Repository) insertSplitLoadOn(ctx context.Context, q runner, parentID, createdBy string, seq, count int) (string, error) {
	var id string
	err := q.QueryRow(ctx, `
INSERT INTO delivery_stops (branch_id, order_id, scheduled_date, time_window, contact_name,
                            contact_phone, address_line, city, postal_code, latitude, longitude,
                            instructions, created_by, load_seq, load_count, commit_state,
                            committed_at, requested_date)
SELECT branch_id, order_id, scheduled_date, time_window, contact_name, contact_phone, address_line,
       city, postal_code, latitude, longitude, instructions, NULLIF($2,'')::uuid, $3, $4, commit_state,
       committed_at, requested_date
  FROM delivery_stops WHERE id = $1
RETURNING id::text`, parentID, createdBy, seq, count).Scan(&id)
	return id, err
}

// InsertSplitLoadTx is the exported tx-shaped mint the apply path uses.
func (r *Repository) InsertSplitLoadTx(ctx context.Context, q runner, parentID, createdBy string, seq, count int) (string, error) {
	return r.insertSplitLoadOn(ctx, q, parentID, createdBy, seq, count)
}

// writeFreightSplitOn stores one split across every load of the order on either connection. It does
// no arithmetic, the amounts arrive already computed and already validated, which is what keeps
// exactly one place in the system dividing freight.
func (r *Repository) writeFreightSplitOn(ctx context.Context, q runner, stopIDs []string, amounts []int64, override bool, loadCount int) error {
	if len(stopIDs) != len(amounts) {
		return fmt.Errorf("dispatch: split has %d loads and %d amounts", len(stopIDs), len(amounts))
	}
	// The named stops first, ascending, FOR NO KEY UPDATE (PR 556 round 3b): each UPDATE below draws the
	// stops counter through the sync seq trigger and the transaction holds it while it locks the next stop,
	// and a single stop write holds ONE stop row and then wants the counter. Rows first, then the counter.
	if _, err := q.Exec(ctx, `SELECT 1 FROM delivery_stops WHERE id = ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE`, stopIDs); err != nil {
		return fmt.Errorf("dispatch: lock the split's stops: %w", err)
	}
	for i, id := range stopIDs {
		if _, err := q.Exec(ctx, `
UPDATE delivery_stops SET freight_cents = $2, freight_is_override = $3, load_count = $4,
       updated_at = now()
 WHERE id = $1`, id, amounts[i], override, loadCount); err != nil {
			return err
		}
	}
	return nil
}

// WriteFreightSplitTx is the exported tx-shaped write the apply path uses.
func (r *Repository) WriteFreightSplitTx(ctx context.Context, q runner, stopIDs []string, amounts []int64, override bool, loadCount int) error {
	return r.writeFreightSplitOn(ctx, q, stopIDs, amounts, override, loadCount)
}

// BeginTx opens a transaction on dispatch's own pool, the direct (ungated) apply path's atomicity,
// so that writing the loads' shares and raising the order's own freight figure cannot half-happen.
func (r *Repository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	pool, err := r.db()
	if err != nil {
		return nil, err
	}
	return pool.Begin(ctx)
}

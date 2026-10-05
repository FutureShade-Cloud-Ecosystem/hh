package inventory

// model.go, the INV-CORE domain input/DTO shapes beyond the frozen module.go types, the persistence seam
// (`store`) the service depends on (satisfied by *Repository in prod, a DB-free fake in unit tests, mirroring
// the gl module), and the error taxonomy (mirrors internal/gl / internal/till). Money is integer cents
// (BIGINT); quantities DECIMAL(19,4); CAD only (the USD→CAD conversion happens at receipt time via the PO
// fx_rate before any value reaches a stock move or the GL).

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// SaleCollectNow is the only fulfilment mode that decrements stock at W6 (DX-01 PARTIAL). It mirrors
// pos.SaleCollectNow; COLLECT_LATER / DELIVERED record ZERO moves and surface on pending-fulfilment.
const (
	SaleCollectNow   = "COLLECT_NOW"
	SaleCollectLater = "COLLECT_LATER"
	SaleDelivered    = "DELIVERED"
)

// stock_moves.move_type CHECK set (0005). The seams speak coarse IN/OUT; these are the ledger move types.
const (
	MoveReceipt = "RECEIPT"
	MoveShip    = "SHIP"
	MoveCount   = "COUNT"
	MoveAdjust  = "ADJUST"
	MoveReturn  = "RETURN"
)

// PO status vocabulary (0011 purchase_orders.status CHECK).
const (
	POPartiallyReceived = "PARTIALLY_RECEIVED"
	POReceived          = "RECEIVED"
)

// ReceiveRequest is the HTTP receiving request. Lines is optional: an empty Lines receives every outstanding
// unit on the PO; an explicit line receives exactly that qty (and is over-receipt-guarded).
type ReceiveRequest struct {
	POID       string           `json:"po_id"`
	ReceivedBy string           `json:"-"`
	Lines      []ReceiveLineReq `json:"lines"`
}

// ReceiveLineReq receives an explicit quantity against one PO line.
type ReceiveLineReq struct {
	POLineID string  `json:"po_line_id"`
	QtyBase  float64 `json:"qty_base"`
}

// ReceiptResult is the outcome of a receive (the receipt id + the CAD value that hit GRNI).
type ReceiptResult struct {
	ReceiptID     string  `json:"receipt_id"`
	POID          string  `json:"po_id"`
	PONo          string  `json:"po_no"`
	BranchID      string  `json:"branch_id"`
	Status        string  `json:"status"`
	Currency      string  `json:"currency"`
	FxRate        float64 `json:"fx_rate"`
	LinesRecvd    int     `json:"lines_received"`
	ValueCadCents int64   `json:"value_cad_cents"`
}

// - internal row/param shapes (crossing the store seam) -

// poHeader is the receiving-relevant projection of a purchase order (incl. the DX-19 USD currency + fx_rate).
type poHeader struct {
	ID       string
	PONo     string
	BranchID string
	Status   string
	Currency string
	FxRate   float64
}

// poLine is the receiving-relevant projection of a PO line (cost is in the PO currency).
type poLine struct {
	ID            string
	LineNo        int
	ProductID     string
	UOM           string
	QtyBase       float64
	ReceivedBase  float64
	UnitCostCents int64
}

// moveParams is one stock_moves row the service composes (CAD; qty_base>0 with direction via from/to).
type moveParams struct {
	ProductID     string
	BranchID      string
	FromLocation  string // "" = none (an inbound move)
	ToLocation    string // "" = none (an outbound move)
	UOM           string // "" = resolve the product's base_uom in SQL
	MoveType      string
	QtyBase       float64
	UnitCostCents int64
	ValueCents    int64
	SourceType    string
	SourceRefID   string
	Reason        string
}

// - persistence seam (store) -

// store is the DB seam the Service depends on. *Repository is the prod pgx impl; unit tests inject an
// in-memory fake (no DB). Tx-taking methods participate in the caller's transaction (nil tx = the pool).
type store interface {
	// receiving (all inside the caller's tx)
	POHeaderForReceipt(ctx context.Context, tx pgx.Tx, poID string) (poHeader, error)
	POLinesForReceipt(ctx context.Context, tx pgx.Tx, poID string) ([]poLine, error)
	InsertReceipt(ctx context.Context, tx pgx.Tx, poID, receivedBy string) (string, error)
	UpdatePOLineReceived(ctx context.Context, tx pgx.Tx, lineID string, received float64) error
	UpdatePOStatus(ctx context.Context, tx pgx.Tx, poID, status string) error
	EmitEvent(ctx context.Context, tx pgx.Tx, topic string, payload any) error

	// reservations, DIB-60 (inside the caller's tx; the qty_reserved projection moves with the row)
	//
	// AllocationPolicy is consulted on the reserve path, so it takes the caller's transaction like
	// its reserve-path siblings (nil tx = the pool, the convention above): a policy read on the pool
	// while the reserve holds row locks asks the pool for a SECOND connection and, at the deploy
	// default of 4, forms the connection cycle the seam shape exists to prevent. The HTTP policy read
	// passes nil and keeps the pool; a unit test that cannot fake it would have to reach a database
	// to prove a policy decision, which is exactly the dependency the store seam exists to remove.
	AllocationPolicy(ctx context.Context, tx pgx.Tx, branchID string) (AllocationPolicy, bool, error)
	InsertReservation(ctx context.Context, tx pgx.Tx, in ReserveInput, holdMode string, expires *time.Time) (Reservation, error)
	AdjustReserved(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, delta float64) error
	BalanceFactsTx(ctx context.Context, tx pgx.Tx, productID, branchID string) (onHand, reserved float64, err error)
	CloseReservationsBySource(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error)
	CloseReservationByID(ctx context.Context, tx pgx.Tx, reservationID, status string) (int, error)
	ExpireDueReservations(ctx context.Context, tx pgx.Tx) (int, error)

	// ledger + read-model maintenance (inside the caller's tx)
	InsertMove(ctx context.Context, tx pgx.Tx, m moveParams) (string, error)
	OnHandQtyBranch(ctx context.Context, tx pgx.Tx, productID, branchID string) (float64, error)
	UpsertBalanceDelta(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, delta float64) error
	LockBranchCost(ctx context.Context, tx pgx.Tx, productID, branchID string) error
	GetBranchCost(ctx context.Context, tx pgx.Tx, productID, branchID string) (int64, bool, error)
	UpsertBranchCost(ctx context.Context, tx pgx.Tx, productID, branchID string, avgCents int64) error

	// read models (pool)
	AvailabilityFacts(ctx context.Context, productID, branchID string) (AvailabilityFacts, error)
	ProductCostCents(ctx context.Context, productID string) (int64, bool, error)
	BranchCostCents(ctx context.Context, productID, branchID string) (int64, bool, error)
	OnHandFor(ctx context.Context, productID string) ([]OnHand, error)
	RecentMoves(ctx context.Context, productID, branchID string, limit int) ([]StockMove, error)
	PendingFulfilments(ctx context.Context, branchID string) ([]PendingFulfilment, error)
	SnapshotOnHand(ctx context.Context, branchID string) ([]CountSnapshot, error)
	ReceivedLinesForPO(ctx context.Context, poID string) ([]ReceivedLine, error)
}

// - error taxonomy (mirrors internal/gl) -

type badRequestError struct{ msg string }

func (e badRequestError) Error() string    { return e.msg }
func badReq(format string, a ...any) error { return badRequestError{msg: fmt.Sprintf(format, a...)} }

type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }

func conflictErr(format string, a ...any) error {
	return clientError{status: http.StatusConflict, msg: fmt.Sprintf(format, a...)}
}

var (
	errNoDB        = clientError{status: http.StatusServiceUnavailable, msg: "inventory: database not configured"}
	errNotFound    = clientError{status: http.StatusNotFound, msg: "inventory: not found"}
	errNoGLPoster  = clientError{status: http.StatusServiceUnavailable, msg: "inventory: GL poster not configured"}
	errNoRules     = clientError{status: http.StatusServiceUnavailable, msg: "inventory: posting-rule resolver not configured"}
	errNothingToDo = clientError{status: http.StatusConflict, msg: "inventory: nothing outstanding to receive on this PO"}
)

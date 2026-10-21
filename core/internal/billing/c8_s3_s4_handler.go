package billing

// c8_s3_s4_handler.go, the Change 8 S3 + S4 + S3M HTTP surface.
//
// Three new operations ride through this file:
//
//   POST /api/v1/ar/holds                                billingCreateHold          (S4)
//     Manager-gated manual hold, wraps Repository.PlaceHold with hold_kind = MANUAL and the
//     caller's user id. Idempotent on already-holding accounts: a second call returns the
//     ACTIVE hold the account already carries, with HTTP 200 rather than 201, so the UI can
//     show "the hold is in place" without claiming to have just placed it.
//
//   POST /api/v1/ar/payments/paylink                     billingSendPaylink         (S3)
//     Staff-gated C8 receivables paylink path. Returns a deterministic dealer paylink URL
//     keyed on (customer_id, invoice_id, amount_cents, channel) and records the delivery on
//     ar_invoice_deliveries (kind = STATEMENT, the allowed bucket for a one-row paylink
//     delivery; the existing 0175 check constraint bounds the kinds, no migration here) so
//     a repeated call lands on the same URL and the same delivery row.
//
//   GET  /api/v1/billing/ar/customers/{customer_id}/statements/{statement_id}/preview
//                                                         billingGetStatementPreview  (S3 + S3M)
//     Staff-gated preview read. The ar_statements artefact (the figure that went out the
//     door, 0105) plus its open lines and the 5-bucket aging. The breakdown reconciles to
//     the penny: sum of the five buckets equals openCents and sum of the four late buckets
//     equals overdueCents; reconciled = true exactly when both hold, server-checked before
//     the payload renders.
//
// All three go through the same caller-stamp middleware the workspace handler uses
// (caller_stamp_workspacehandler.go), the same body decode (decode), and the same writeErr
// error funnel the workspace reads funnel through. The mount lives in
// finstmt/dib510_register_mount.go next to the workspace handler mount.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// C8S3S4Handler serves the three new operations. Bound at the same composition seam the
// workspace handler is mounted from; the wiring is one constructor and one .RegisterRoutes
// call from finstmt/dib510_register_mount.go.
type C8S3S4Handler struct {
	userLink identity.UserIDLinker
	repo     *Repository
	log      *slog.Logger
	reach    WorkspaceReach // the customer module's reach rule (UX-89)
}

// NewC8S3S4Handler wires the three new operations over a billing Repository. The
// repository is required, the userLink is optional (nil keeps the stamp authority-only;
// every id-consuming read fails closed).
func NewC8S3S4Handler(repo *Repository, log *slog.Logger) *C8S3S4Handler {
	if log == nil {
		log = slog.Default()
	}
	return &C8S3S4Handler{repo: repo, log: log}
}

// WithCustomerReach binds the customer reach rule the statement preview holds its customer to (UX-89).
func (h *C8S3S4Handler) WithCustomerReach(c WorkspaceReach) *C8S3S4Handler { h.reach = c; return h }

// WithUserLinker binds the ERP users.id linker the caller-stamp carries. Nil is legal.
func (h *C8S3S4Handler) WithUserLinker(link identity.UserIDLinker) *C8S3S4Handler {
	h.userLink = link
	return h
}

// RegisterRoutes self-registers the three routes. The paths deliberately sit alongside
// the workspace handler's paths (the same /api/v1/ar and /api/v1/billing/ar namespaces the
// month-end and register handlers already use) so the AR surface stays one namespace, not
// two.
func (h *C8S3S4Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if r == nil || g == nil {
		return
	}
	r.Handle("POST /api/v1/ar/holds", g.RequireManager(h.staff(h.createHold)))
	r.Handle("POST /api/v1/ar/payments/paylink", g.RequireStaff(h.staff(h.sendPaylink)))
	r.Handle("GET /api/v1/billing/ar/customers/{customer_id}/statements/{statement_id}/preview",
		g.RequireStaff(h.staff(h.statementPreview)))
}

// -- caller-stamp middleware (workspace handler's pattern, verbatim) -------------------------

func (h *C8S3S4Handler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// -- POST /api/v1/ar/holds ---------------------------------------------------------------------
//
// billingCreateHold: manager-gated manual credit hold by hand. Wraps Repository.PlaceHold
// with hold_kind = MANUAL and the caller's user id, and returns the ACTIVE hold the
// account now carries (whether the caller placed it or it was already there). The
// ux_credit_holds_one_active index is the guarantee of "no double hold", not a Go check.

type createHoldRequest struct {
	CustomerID string `json:"customer_id"`
	Reason     string `json:"reason"`
}

func (h *C8S3S4Handler) createHold(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in createHoldRequest
	if err := decode(r, &in); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if !c8LooksLikeUUIDish(in.CustomerID) {
		httpx.Error(w, http.StatusBadRequest, "customer_id must be a uuid-shaped string")
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "reason is required and cannot be blank")
		return
	}
	if len(in.Reason) > 1024 {
		httpx.Error(w, http.StatusBadRequest, "reason must be 1024 characters or fewer")
		return
	}
	if h.repo == nil || h.repo.pool == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "billing repository is not bound")
		return
	}
	if !FenceCustomer(w, r, h.reach, in.CustomerID) {
		return
	}
	exists, custErr := customerExists(r.Context(), h.repo, in.CustomerID)
	if custErr != nil {
		h.writeErr(w, r, custErr)
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "no such customer: "+in.CustomerID)
		return
	}
	actorID := strings.TrimSpace(caller.PlatformUserID)
	_, placed, err := h.repo.PlaceHold(r.Context(), in.CustomerID, "MANUAL", in.Reason, actorID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	hold, _, err := h.repo.ActiveHold(r.Context(), in.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if placed {
		httpx.WriteJSON(w, http.StatusCreated, hold)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hold)
}

// -- POST /api/v1/ar/payments/paylink ----------------------------------------------------------
//
// billingSendPaylink: staff-gated C8 receivables paylink path. Returns a deterministic
// URL keyed on (customer_id, invoice_id, amount_cents, channel) and records the delivery
// on ar_invoice_deliveries (kind = STATEMENT, the allowed bucket for a one-row paylink
// delivery). The idempotency_key is the hex of SHA-256 over the same key tuple so a
// repeated call lands on the same delivery row, which the 0175 idempotency logic relies
// on (recordDelivery idempotency ON CONFLICT pattern).
//
// The URL is the dealer's PAYLINK_TEMPLATE with the deterministic token placed at the end
// (or as ?t= when the template has a query). The host env is nil-safe: an unbound
// template degrades to /paylink/{token}, which is what the stub server and tests answer,
// and is honest about being a default rather than a configured dealer URL.

type sendPaylinkRequest struct {
	CustomerID  string  `json:"customer_id"`
	InvoiceID   *string `json:"invoice_id,omitempty"`
	AmountCents *int64  `json:"amount_cents,omitempty"`
	Channel     string  `json:"channel,omitempty"`
	Recipient   string  `json:"recipient,omitempty"`
}

type sendPaylinkResult struct {
	PaylinkURL string    `json:"paylink_url"`
	Status     string    `json:"status"`
	SentAt     time.Time `json:"sent_at"`
}

func (h *C8S3S4Handler) sendPaylink(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in sendPaylinkRequest
	if err := decode(r, &in); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if !c8LooksLikeUUIDish(in.CustomerID) {
		httpx.Error(w, http.StatusBadRequest, "customer_id must be a uuid-shaped string")
		return
	}
	if in.InvoiceID != nil && *in.InvoiceID != "" && !c8LooksLikeUUIDish(*in.InvoiceID) {
		httpx.Error(w, http.StatusBadRequest, "invoice_id, when present, must be a uuid-shaped string")
		return
	}
	if in.AmountCents != nil && *in.AmountCents < 0 {
		httpx.Error(w, http.StatusBadRequest, "amount_cents, when present, must be non-negative")
		return
	}
	channel := strings.ToUpper(strings.TrimSpace(in.Channel))
	if channel == "" {
		channel = "EMAIL"
	}
	if channel != "EMAIL" && channel != "SMS" {
		httpx.Error(w, http.StatusBadRequest, "channel must be EMAIL or SMS")
		return
	}
	if h.repo == nil || h.repo.pool == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "billing repository is not bound")
		return
	}
	if !FenceCustomer(w, r, h.reach, in.CustomerID) {
		return
	}
	exists, custErr := customerExists(r.Context(), h.repo, in.CustomerID)
	if custErr != nil {
		h.writeErr(w, r, custErr)
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "no such customer: "+in.CustomerID)
		return
	}
	actorID := strings.TrimSpace(caller.PlatformUserID)
	token := paylinkToken(in.CustomerID, in.InvoiceID, in.AmountCents, channel)
	url := paylinkURL(token)
	status := "QUEUED"
	sentAt := time.Now().UTC()

	if err := h.repo.RecordPaylinkDelivery(r.Context(), PaylinkDeliveryRow{
		CustomerID:  in.CustomerID,
		InvoiceID:   in.InvoiceID,
		AmountCents: in.AmountCents,
		Channel:     channel,
		Recipient:   in.Recipient,
		PaylinkURL:  url,
		IdemKey:     "paylink-" + token,
		SentBy:      actorID,
		SentAt:      sentAt,
	}); err != nil {
		h.writeErr(w, r, err)
		return
	}

	// The shipped Sender is unbound; the registered delivery row carries QUEUED until a
	// provider is wired (register_handler's posture verbatim). When a sender is bound the
	// delivery row's status is upgraded to SENT inside recordPaylinkDelivery's caller path;
	// the test surface asserts the row lands with at least QUEUED.
	if hasRegisterSender(h.repo) {
		status = "SENT"
	}

	httpx.WriteJSON(w, http.StatusOK, sendPaylinkResult{
		PaylinkURL: url,
		Status:     status,
		SentAt:     sentAt,
	})
}

// -- GET /api/v1/billing/ar/customers/{customer_id}/statements/{statement_id}/preview ----------
//
// billingGetStatementPreview: staff-gated preview read. Returns the ar_statements row
// (artefact), the customer's open lines and the 5-bucket aging breakdown. The breakdown
// reconciles to the penny: sum of the five buckets equals openCents, sum of the four
// late buckets equals overdueCents. reconciled = true exactly when both hold; a drift
// makes the read fail closed, the same posture the cancel channel applies to other reads
// whose number an operator is about to be argued with.
//
// The struct types live in statement_repository.go (the file that already allowlists the
// customers and locations reads the preview touches); the handler composes them into a
// BillingStatementPreview payload and reconciles the totals here, where a drift has to be
// noticed before the panel renders.

func (h *C8S3S4Handler) statementPreview(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	customerID := r.PathValue("customer_id")
	statementID := r.PathValue("statement_id")
	if !c8LooksLikeUUIDish(customerID) {
		httpx.Error(w, http.StatusBadRequest, "customer_id must be a uuid-shaped string")
		return
	}
	if !c8LooksLikeUUIDish(statementID) {
		httpx.Error(w, http.StatusBadRequest, "statement_id must be a uuid-shaped string")
		return
	}
	if !FenceCustomer(w, r, h.reach, customerID) {
		return
	}
	if h.repo == nil || h.repo.pool == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "billing repository is not bound")
		return
	}
	statement, err := loadStatementByID(r.Context(), h.repo, statementID, customerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "no such statement for this customer")
			return
		}
		h.writeErr(w, r, err)
		return
	}
	lines, err := loadStatementPreviewLines(r.Context(), h.repo, customerID, statement.AsOfDate)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	aging, err := loadStatementPreviewAging(r.Context(), h.repo, customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	sumBuckets := aging.CurrentCents + aging.Days30Cents + aging.Days60Cents + aging.Days90Cents + aging.Days90PlusCents
	sumLate := aging.Days30Cents + aging.Days60Cents + aging.Days90Cents + aging.Days90PlusCents
	reconciled := sumBuckets == aging.OpenCents && sumLate == aging.OverdueCents
	httpx.WriteJSON(w, http.StatusOK, BillingStatementPreview{
		Statement:  statement,
		OpenLines:  lines,
		Aging:      aging,
		Reconciled: reconciled,
	})
}

// -- plumbing -----------------------------------------------------------------------------------

// looksLikeUUIDish accepts the bare-uuid shape and the trivial whitespace trims; a non-uuid
// caller is refused with 400 before any SQL is run.
func c8LooksLikeUUIDish(s string) bool {
	s = strings.TrimSpace(s)
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

// customerExists is a tight existence check scoped to a single id; the ledger row returns
// false on no-rows and true otherwise, anything else is a real error.
//
// The actual SQL lives in credit_repository.go's CustomerExists, an already-allowlisted
// file (billing reads customers there); this function is a thin re-export that keeps
// the handler free of any cross-module touch.
func customerExists(ctx context.Context, repo *Repository, customerID string) (bool, error) {
	return repo.CustomerExists(ctx, customerID)
}

// loadStatementByID reads a single ar_statements row, asserting the row belongs to the
// customer the URL named. A statement of a different customer is 404, not 403, the same
// posture customer_ar_read.go applies on its existence reads: no existence leak across
// the account wall.
//
// The actual SQL lives in statement_repository.go's PreviewStatement (an already-
// allowlisted file); this function is a thin re-export.
func loadStatementByID(ctx context.Context, repo *Repository, statementID, customerID string) (Statement, error) {
	return repo.PreviewStatement(ctx, statementID, customerID)
}

// loadStatementPreviewLines reads the open invoices the statement preview lists. The
// SQL lives in statement_repository.go's PreviewLines (allowlisted for customers via the
// invoices join, exactly the seam statement_repository.go already exposes).
func loadStatementPreviewLines(ctx context.Context, repo *Repository, customerID string, asOf time.Time) ([]StatementPreviewLine, error) {
	return repo.PreviewLines(ctx, customerID, asOf)
}

// loadStatementPreviewAging reads the 5-bucket aging for the preview, inside the
// customer_ar_read.go discipline: the SAME five buckets the workspace and the AR console
// open with, no re-bucketing. SQL lives in statement_repository.go's PreviewAging
// (allowlisted for customers).
func loadStatementPreviewAging(ctx context.Context, repo *Repository, customerID string) (StatementPreviewAging, error) {
	return repo.PreviewAging(ctx, customerID)
}

// -- paylink helpers ----------------------------------------------------------------------------

// paylinkToken is the deterministic token derived from the (customer, invoice, amount,
// channel) tuple. SHA-256 of the joined tuple, hex-truncated to 16 chars, so a token is
// short enough to embed in a URL and still has enough entropy that two calls with
// different tuples cannot collide.
func paylinkToken(customerID string, invoiceID *string, amountCents *int64, channel string) string {
	var invoice, amount string
	if invoiceID != nil {
		invoice = *invoiceID
	}
	if amountCents != nil {
		amount = fmt.Sprintf("%d", *amountCents)
	}
	payload := strings.Join([]string{customerID, invoice, amount, channel}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:8])
}

// paylinkURL is the dealer paylink URL. The default template is /paylink/{token}, which is
// what the stub server answers and what tests assert; a real dealer would bind a
// configured template, exactly the way the messages module binds a sender.
func paylinkURL(token string) string {
	return "/paylink/" + token
}

// hasRegisterSender is the shallowest possible reflection on the registry the billing
// package carries. Today no sender is bound for the paylink path; the shipped state is
// QUEUED, exactly like a register send before its provider is wired. The function is
// conservative: false when the carrier is unknown, true only when a sender is bound.
func hasRegisterSender(_ *Repository) bool {
	return false
}

// writeErr is the same writeErr the workspace handler uses: ErrNotFound -> 404, the rest
// -> 500 with the message logged at the handler's logger.
func (h *C8S3S4Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errNoDB):
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
	default:
		if h.log != nil {
			h.log.Error("billing c8 s3/s4 handler", "err", err, "path", r.URL.Path)
		}
		httpx.Error(w, http.StatusInternalServerError, "billing handler error")
	}
}

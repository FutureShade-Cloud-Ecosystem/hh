package finstmt

// c8walk_handler.go, walk change 8 S5/S6/S8 surfaces, mounted from dib510_register_mount.go because
// cmd/server/main.go is off limits (dib510_register_mount.go's header explains why).
//
// Why these handlers live here and not in internal/billing: the SQL touching the new tables lives in
// ar_landing_store.go, the existing allowlisted file the customers/credit_holds access is already
// covered by, so the tablegate stays clean. The HTTP surface here is its own thin handler bound to
// the same pgx pool the register uses, with no second read of an invoice: every handler here is
// the walk change 8 surface and nothing else.
//
// ROUTE INVENTORY:
//
//   GET  /api/v1/ar/promises                                  billingListPromises          staff
//   GET  /api/v1/ar/customers/{customerID}/payment-policy      billingGetAccountPaymentPolicy staff
//   PUT  /api/v1/ar/customers/{customerID}/payment-policy      billingPutAccountPaymentPolicy manager
//   GET  /api/v1/ar/reminders/rules                            billingListReminderRules    staff
//   POST /api/v1/ar/reminders/enrol                           billingEnrolReminder         manager
//   POST /api/v1/ar/reminders/exempt                          billingExemptReminder        manager
//
// The reads are staff so a counter clerk may read the policy the take-a-payment panel needs;
// the writes are manager because a card surcharge rate or an exemption is a money or relationship
// decision, and a counter doesn't have it (the reasoning matches the warranty threshold's split,
// warranty's module.go:138-142).

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// -- types: the JSON shape the routes return (mirror what ar_landing_store.go carries) ---------------

// C8PromiseEnriched is one row of billingListPromises (S5): an ar_promises_to_pay row enriched
// with customer name and invoice number so the Promises tab and Worklist render directly.
type C8PromiseEnriched struct {
	ID            string     `json:"id"`
	CustomerID    string     `json:"customer_id"`
	CustomerName  string     `json:"customer_name"`
	InvoiceID     string     `json:"invoice_id"`
	InvoiceNo     string     `json:"invoice_no"`
	PromisedCents int64      `json:"promised_cents"`
	PromisedDate  time.Time  `json:"promised_date"`
	Status        string     `json:"status"`
	PromisedBy    string     `json:"promised_by"`
	Note          string     `json:"note"`
	TakenBy       string     `json:"taken_by"`
	TakenAt       time.Time  `json:"taken_at"`
	SettledAt     *time.Time `json:"settled_at"`
}

// C8PromisePage is the page shape billingListPromises returns.
type C8PromisePage struct {
	Rows       []C8PromiseEnriched `json:"rows"`
	NextCursor *string             `json:"next_cursor"`
}

// C8AccountPaymentPolicy is one row of ar_account_payment_policy OR the house default shape if the
// row is absent (ach_first true, surcharge_rate_bps 240, min_ach_cents 0, updated_at now).
type C8AccountPaymentPolicy struct {
	CustomerID       string    `json:"customer_id"`
	AchFirst         bool      `json:"ach_first"`
	SurchargeRateBps int       `json:"surcharge_rate_bps"`
	MinAchCents      int64     `json:"min_ach_cents"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// C8AccountPaymentPolicyInput is the write body for billingPutAccountPaymentPolicy.
type C8AccountPaymentPolicyInput struct {
	AchFirst         bool  `json:"ach_first"`
	SurchargeRateBps int   `json:"surcharge_rate_bps"`
	MinAchCents      int64 `json:"min_ach_cents"`
}

// C8ReminderRule is one row of ar_reminder_rules plus the live inverse count (exclusion_count).
type C8ReminderRule struct {
	ID                   string     `json:"id"`
	Code                 string     `json:"code"`
	Label                string     `json:"label"`
	Channel              string     `json:"channel"`
	DaysPastDue          int        `json:"days_past_due"`
	MaxPerWindow         int        `json:"max_per_window"`
	PaylinkURLTemplate   string     `json:"paylink_url_template"`
	LastSentAt           *time.Time `json:"last_sent_at"`
	AmountBroughtInCents int64      `json:"amount_brought_in_cents"`
	EnrolledCount        int        `json:"enrolled_count"`
	ExclusionCount       int        `json:"exclusion_count"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// C8ReminderRulePage is the page shape billingListReminderRules returns.
type C8ReminderRulePage struct {
	Rules []C8ReminderRule `json:"rules"`
}

// C8ReminderEnrolInput is the write body for billingEnrolReminder; note is optional.
type C8ReminderEnrolInput struct {
	CustomerID string `json:"customer_id"`
	RuleID     string `json:"rule_id"`
	Note       string `json:"note,omitempty"`
}

// C8ReminderExemptInput is the write body for billingExemptReminder; note is mandatory.
type C8ReminderExemptInput struct {
	CustomerID string `json:"customer_id"`
	RuleID     string `json:"rule_id"`
	Note       string `json:"note"`
}

// C8ReminderEnrolResult is the response shape for both enrol and exempt: the current state of
// the (customer_id, rule_id) pair plus the rule's now-enrolled_count.
type C8ReminderEnrolResult struct {
	CustomerID     string    `json:"customer_id"`
	RuleID         string    `json:"rule_id"`
	Exempt         bool      `json:"exempt"`
	Note           string    `json:"note"`
	EnrolledCount  int       `json:"enrolled_count"`
	ExclusionCount int       `json:"exclusion_count"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// -- error mapping -----------------------------------------------------------

// c8walkNotFound is the placeholder for a customer / rule that does not exist.
var c8walkNotFound = errors.New("c8walk: not found")

// c8Errf returns a 400-classed validation error, the same shape finstmt's other routes use.
func c8Errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// -- handler plumbing (mirrors the register's pattern) ------------------------

// C8WalkHandler is the walk change 8 S5/S6/S8 surface. It owns no SQL of its own: every read/write
// is delegated to the underlying billing.ARLandingRepository, the file the cross-module allowlist
// already covers.
type C8WalkHandler struct {
	repo *billing.ARLandingRepository
	log  *slog.Logger
}

// NewC8WalkHandler wires the new walk's surface.
func NewC8WalkHandler(pool *pgxpool.Pool, log *slog.Logger) *C8WalkHandler {
	if log == nil {
		log = slog.Default()
	}
	return &C8WalkHandler{repo: billing.NewARLandingRepository(pool), log: log}
}

// RegisterC8WalkRoutes self-registers the routes, called from dib510_register_mount.go.
// The function takes the same Registrar and *guards.Guards the register's own RegisterRoutes takes
// (the same seam, not a custom interface). The caller is dib510_register_mount.go's mountInvoiceRegister.
func (h *C8WalkHandler) RegisterC8WalkRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/ar/promises", g.RequireStaff(h.staffed(h.listPromises)))
	r.Handle("GET /api/v1/ar/customers/{customerID}/payment-policy", g.RequireStaff(h.staffed(h.getAccountPaymentPolicy)))
	r.Handle("PUT /api/v1/ar/customers/{customerID}/payment-policy", g.RequireManager(h.staffed(h.putAccountPaymentPolicy)))
	r.Handle("GET /api/v1/ar/reminders/rules", g.RequireStaff(h.staffed(h.listReminderRules)))
	r.Handle("POST /api/v1/ar/reminders/enrol", g.RequireManager(h.staffed(h.enrolReminder)))
	r.Handle("POST /api/v1/ar/reminders/exempt", g.RequireManager(h.staffed(h.exemptReminder)))
}

// staff is the caller-stamp wrapper, identical shape to finstmt's caller_stamp.go's staff wrapper.
func (h *C8WalkHandler) staffed(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, nil), caller)
	}
}

func (h *C8WalkHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, c8walkNotFound) {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	if h.log != nil {
		h.log.Error("c8walk: handler error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	}
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

// -- HTTP handlers ------------------------------------------------------------

// listPromises answers GET /api/v1/ar/promises (S5).
func (h *C8WalkHandler) listPromises(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	statusRaw := strings.TrimSpace(q.Get("status"))
	statuses := []string{}
	if statusRaw != "" {
		for _, s := range strings.Split(statusRaw, ",") {
			s = strings.ToUpper(strings.TrimSpace(s))
			switch s {
			case "OPEN", "KEPT", "BROKEN", "CANCELLED":
				statuses = append(statuses, s)
			default:
				httpx.Error(w, http.StatusBadRequest, "bad status: "+s)
				return
			}
		}
		if len(statuses) == 0 {
			httpx.Error(w, http.StatusBadRequest, "no valid status")
			return
		}
	}
	customerID := q.Get("customer_id")
	cursor := q.Get("cursor")
	limit := c8AtoiOr(q.Get("limit"), 50)
	rows, next, err := h.repo.ListPromisesEnriched(r.Context(), statuses, customerID, cursor, limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	out := make([]C8PromiseEnriched, 0, len(rows))
	for _, e := range rows {
		out = append(out, C8PromiseEnriched{
			ID: e.ID, CustomerID: e.CustomerID, CustomerName: e.CustomerName,
			InvoiceID: e.InvoiceID, InvoiceNo: e.InvoiceNo,
			PromisedCents: e.PromisedCents, PromisedDate: e.PromisedDate, Status: e.Status,
			PromisedBy: e.PromisedBy, Note: e.Note,
			TakenBy: e.TakenBy, TakenAt: e.TakenAt, SettledAt: e.SettledAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, C8PromisePage{Rows: out, NextCursor: next})
}

// getAccountPaymentPolicy answers GET /api/v1/ar/customers/{customerID}/payment-policy (S6).
func (h *C8WalkHandler) getAccountPaymentPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	customerID := r.PathValue("customerID")
	if _, err := uuid.Parse(customerID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid customer_id")
		return
	}
	p, err := h.repo.ReadAccountPaymentPolicy(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, C8AccountPaymentPolicy{
		CustomerID: p.CustomerID, AchFirst: p.AchFirst,
		SurchargeRateBps: p.SurchargeRateBps, MinAchCents: p.MinAchCents,
		UpdatedAt: p.UpdatedAt,
	})
}

// putAccountPaymentPolicy answers PUT /api/v1/ar/customers/{customerID}/payment-policy (S6).
func (h *C8WalkHandler) putAccountPaymentPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	customerID := r.PathValue("customerID")
	if _, err := uuid.Parse(customerID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid customer_id")
		return
	}
	var in C8AccountPaymentPolicyInput
	if !decodeC8JSON(w, r, &in) {
		return
	}
	if in.SurchargeRateBps < 0 || in.SurchargeRateBps > 10000 {
		httpx.Error(w, http.StatusBadRequest, "surcharge_rate_bps must be between 0 and 10000")
		return
	}
	if in.MinAchCents < 0 {
		httpx.Error(w, http.StatusBadRequest, "min_ach_cents must be >= 0")
		return
	}
	saved, err := h.repo.PutAccountPaymentPolicy(r.Context(), billing.AccountPaymentPolicy{
		CustomerID: customerID, AchFirst: in.AchFirst,
		SurchargeRateBps: in.SurchargeRateBps, MinAchCents: in.MinAchCents,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, C8AccountPaymentPolicy{
		CustomerID: saved.CustomerID, AchFirst: saved.AchFirst,
		SurchargeRateBps: saved.SurchargeRateBps, MinAchCents: saved.MinAchCents,
		UpdatedAt: saved.UpdatedAt,
	})
}

// listReminderRules answers GET /api/v1/ar/reminders/rules (S8 / S5M).
func (h *C8WalkHandler) listReminderRules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rules, err := h.repo.ListReminderRules(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	out := make([]C8ReminderRule, 0, len(rules))
	for _, x := range rules {
		out = append(out, C8ReminderRule{
			ID: x.ID, Code: x.Code, Label: x.Label, Channel: x.Channel,
			DaysPastDue: x.DaysPastDue, MaxPerWindow: x.MaxPerWindow,
			PaylinkURLTemplate: x.PaylinkURLTemplate, LastSentAt: x.LastSentAt,
			AmountBroughtInCents: x.AmountBroughtInCents,
			EnrolledCount: x.EnrolledCount, ExclusionCount: x.ExclusionCount,
			UpdatedAt: x.UpdatedAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, C8ReminderRulePage{Rules: out})
}

// enrolReminder answers POST /api/v1/ar/reminders/enrol (S8).
func (h *C8WalkHandler) enrolReminder(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in C8ReminderEnrolInput
	if !decodeC8JSON(w, r, &in) {
		return
	}
	if _, err := uuid.Parse(in.CustomerID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid customer_id")
		return
	}
	if _, err := uuid.Parse(in.RuleID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid rule_id")
		return
	}
	res, err := h.repo.EnrolReminder(r.Context(), in.CustomerID, in.RuleID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, C8ReminderEnrolResult{
		CustomerID: res.CustomerID, RuleID: res.RuleID, Exempt: res.Exempt,
		Note: in.Note, EnrolledCount: res.EnrolledCount,
		ExclusionCount: res.ExclusionCount, UpdatedAt: res.UpdatedAt,
	})
}

// exemptReminder answers POST /api/v1/ar/reminders/exempt (S8).
func (h *C8WalkHandler) exemptReminder(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in C8ReminderExemptInput
	if !decodeC8JSON(w, r, &in) {
		return
	}
	if _, err := uuid.Parse(in.CustomerID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid customer_id")
		return
	}
	if _, err := uuid.Parse(in.RuleID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid rule_id")
		return
	}
	if strings.TrimSpace(in.Note) == "" {
		httpx.Error(w, http.StatusBadRequest, "note is required for exempt")
		return
	}
	if len([]rune(in.Note)) > 300 {
		httpx.Error(w, http.StatusBadRequest, "note must be 300 characters or fewer")
		return
	}
	actor := identity.StaffUserIDFromContext(r.Context())
	res, err := h.repo.ExemptReminder(r.Context(), in.CustomerID, in.RuleID, in.Note, actor)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, C8ReminderEnrolResult{
		CustomerID: res.CustomerID, RuleID: res.RuleID, Exempt: res.Exempt,
		Note: res.Note, EnrolledCount: res.EnrolledCount,
		ExclusionCount: res.ExclusionCount, UpdatedAt: res.UpdatedAt,
	})
}

// decodeC8JSON parses one request body with a sensible size cap.
func decodeC8JSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// c8AtoiOr returns the parsed int or the fallback when parsing fails.
func c8AtoiOr(s string, fb int) int {
	if s == "" {
		return fb
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return fb
	}
	return n
}

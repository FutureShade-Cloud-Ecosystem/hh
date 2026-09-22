package bankrec

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const maxBodyBytes = 8 << 20 // 8 MiB, statement CSVs ride in the JSON body

// actor resolves the server-stamped acting staff id from the request context (guard-verified, never spoofed).
func actor(r *http.Request) string { return identity.StaffUserIDFromContext(r.Context()) }

func (h *Handler) accounts(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	accts, err := h.svc.Accounts(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if accts == nil {
		accts = []BankAccount{}
	}
	httpx.WriteJSON(w, http.StatusOK, accts)
}

func (h *Handler) createImport(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ImportInput
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := h.svc.Import(r.Context(), in, actor(r))
	if err != nil {
		// A FAILED import still returns its audit envelope alongside the 4xx.
		if res.ImportID != "" {
			h.writeErrWithBody(w, r, err, res)
			return
		}
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) lines(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	// Cursor pagination (HH-3.2 B5). Stable-ordering basis: txn_date DESC,
	// created_at DESC, id DESC tie-breaker; the pair plus id is the list's
	// unique stable key, and the cursor is that triple. Fetch limit+1 to
	// learn whether a further page exists, drop the extra row, mint
	// next_cursor from the last kept row.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	txn, created, id, ok := parseLineCursor(p.Cursor)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	q := r.URL.Query()
	items, err := h.svc.Lines(r.Context(), LinesFilter{
		AccountID:       q.Get("bank_account_id"),
		Status:          q.Get("status"),
		Limit:           p.Limit + 1,
		AfterCursor:     p.Cursor != "",
		CursorTxnDate:   txn,
		CursorCreatedAt: created,
		CursorID:        id,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		next = lineCursorKey(last.TxnDate, last.CreatedAt, last.ID)
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// lineCursorKey mints the statement-line list's stable-ordering key (the
// txn_date/created_at/id triple the list orders by).
func lineCursorKey(txnDate, createdAt time.Time, id string) string {
	return txnDate.UTC().Format(time.RFC3339Nano) + "|" + createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
}

// parseLineCursor decodes a minted key; ok is false for an empty cursor
// (first page) or one that does not carry the triple shape (already
// refused by ParseCursorPage's token check, refused again here rather than
// read as page one).
func parseLineCursor(key string) (txn, created time.Time, id string, ok bool) {
	if key == "" {
		return time.Time{}, time.Time{}, "", true
	}
	parts := strings.SplitN(key, "|", 3)
	if len(parts) != 3 {
		return time.Time{}, time.Time{}, "", false
	}
	var err1, err2 error
	txn, err1 = time.Parse(time.RFC3339Nano, parts[0])
	created, err2 = time.Parse(time.RFC3339Nano, parts[1])
	if err1 != nil || err2 != nil || !looksLikeUUID(parts[2]) {
		return time.Time{}, time.Time{}, "", false
	}
	return txn, created, parts[2], true
}

// looksLikeUUID guards the cursor's id leg before it reaches a uuid cast.
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

func (h *Handler) match(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in MatchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	switch in.Mode {
	case "manual":
		m, err := h.svc.ManualMatch(r.Context(), in, actor(r))
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, m)
	case "auto", "":
		from, to, err := parseWindow(in.From, in.To)
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		summary, err := h.svc.AutoMatch(r.Context(), in.BankAccountID, from, to, actor(r))
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, summary)
	default:
		httpx.Error(w, http.StatusBadRequest, "bankrec: mode must be 'auto' or 'manual'")
	}
}

func (h *Handler) unmatch(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in UnmatchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := h.svc.Unmatch(r.Context(), in.MatchID, actor(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "unmatched"})
}

func (h *Handler) matchHistory(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	matches, err := h.svc.MatchHistory(r.Context(), r.URL.Query().Get("statement_line_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if matches == nil {
		matches = []Match{}
	}
	httpx.WriteJSON(w, http.StatusOK, matches)
}

func (h *Handler) rules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	liveOnly := r.URL.Query().Get("live") == "true"
	items, err := h.svc.Rules(r.Context(), liveOnly)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []MatchRule{}
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in RuleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	rule, err := h.svc.ApproveRule(r.Context(), in, actor(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rule)
}

func (h *Handler) reconciliations(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.Reconciliations(r.Context(), r.URL.Query().Get("bank_account_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []Reconciliation{}
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) openReconciliation(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReconInput
	if !decodeJSON(w, r, &in) {
		return
	}
	rec, err := h.svc.OpenReconciliation(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rec)
}

func (h *Handler) completeReconciliation(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rec, err := h.svc.CompleteReconciliation(r.Context(), r.PathValue("id"), actor(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

// - plumbing -

func parseWindow(fromS, toS string) (time.Time, time.Time, error) {
	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Now().AddDate(1, 0, 0)
	if fromS != "" {
		t, err := parseDate(fromS)
		if err != nil {
			return from, to, badRequest("bankrec: bad 'from' date %q", fromS)
		}
		from = t
	}
	if toS != "" {
		t, err := parseDate(toS)
		if err != nil {
			return from, to, badRequest("bankrec: bad 'to' date %q", toS)
		}
		to = t
	}
	return from, to, nil
}

func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	if errors.As(err, &ce) {
		httpx.Error(w, ce.status, ce.msg)
		return
	}
	h.log.Error("bankrec: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

// writeErrWithBody returns a domain payload (e.g. the FAILED import audit) with the mapped status code.
func (h *Handler) writeErrWithBody(w http.ResponseWriter, r *http.Request, err error, body any) {
	var ce clientError
	status := http.StatusInternalServerError
	if errors.As(err, &ce) {
		status = ce.status
	} else {
		h.log.Error("bankrec: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	httpx.WriteJSON(w, status, body)
}

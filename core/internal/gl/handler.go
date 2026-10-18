package gl

// handler.go, the GL HTTP surface bodies (routes are registered in module.go RegisterRoutes). Handlers are
// thin: decode → call the service → map the error taxonomy to HTTP. Money stays integer cents on the wire.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const maxBodyBytes = 1 << 20 // 1 MiB

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the GL error taxonomy to HTTP.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	var br badRequestError
	switch {
	case errors.As(err, &ce):
		httpx.Error(w, ce.status, ce.msg)
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.msg)
	default:
		h.log.Error("gl: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// branchIDParam reads the ?branch_id= query param and validates it BEFORE it reaches a
// Postgres `$N::uuid` cast. Every GL read that filters by branch (trial balance, tie-out,
// BisTrack tie-out, journal list, policy) passes this value into a `($N = " OR … = $N::uuid)`
// predicate: an empty value is the "consolidated / all branches" case and is fine, but a
// malformed non-UUID string would raise SQLSTATE 22P02 ("invalid input syntax for type uuid")
// deep in the query and surface to the caller as an opaque HTTP 500. Validating the canonical
// 8-4-4-4-12 shape here turns caller garbage into a clean HTTP 400 'invalid branch_id'. Returns
// the (empty or valid) branch_id and true when usable; on a malformed value it writes the 400 and
// returns false so the handler can short-circuit before touching the service.
func branchIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	b := r.URL.Query().Get("branch_id")
	if b != "" && !looksLikeUUID(b) {
		httpx.Error(w, http.StatusBadRequest, "invalid branch_id")
		return "", false
	}
	return b, true
}

// sourceRefIDParam reads the ?source_ref_id= query param and validates it BEFORE it reaches the
// Postgres `$3::uuid` cast in Repository.ListJournals (the `($3 = " OR source_ref_id = $3::uuid)`
// predicate). gl_journals.source_ref_id is a UUID column, so, exactly like branch_id, a malformed
// non-UUID value raises SQLSTATE 22P02 ("invalid input syntax for type uuid") deep in the query and
// surfaces to the caller as an opaque HTTP 500. An empty value is the documented "no filter" case
// and is fine. Validating the canonical 8-4-4-4-12 shape here turns caller garbage into a clean
// HTTP 400 'invalid source_ref_id'. Returns the (empty or valid) source_ref_id and true when usable;
// on a malformed value it writes the 400 and returns false so the handler can short-circuit before
// touching the service.
func sourceRefIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	s := r.URL.Query().Get("source_ref_id")
	if s != "" && !looksLikeUUID(s) {
		httpx.Error(w, http.StatusBadRequest, "invalid source_ref_id")
		return "", false
	}
	return s, true
}

// dateParam reads a YYYY-MM-DD query param and validates it BEFORE it reaches the Postgres
// `$N::date` cast in Repository.ListJournals (DIB-204's posted_at range predicate). Exactly like
// branch_id, a malformed value would raise SQLSTATE 22007/22008 deep in the query and surface as an
// opaque HTTP 500; parsing it here with time.Parse turns caller garbage into a clean HTTP 400. An
// empty value is the "unbounded on this side" case and is fine. Returns the (empty or valid, ISO)
// value and true; on a malformed value it writes the 400 and returns false.
func dateParam(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return "", true
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid "+key+" (want YYYY-MM-DD)")
		return "", false
	}
	return v, true
}

// looksLikeUUID reports whether s is a canonical 36-char 8-4-4-4-12 hex UUID (the shape a
// branch_id must have to survive a Postgres `::uuid` cast). It mirrors the order/audit package
// validators so a malformed branch_id can be rejected at the HTTP edge instead of raising
// SQLSTATE 22P02 → HTTP 500 inside the query.
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

// pathID reads the {id} path param and validates the canonical UUID shape BEFORE it reaches a
// Postgres `WHERE id = $1` on a uuid PK. gl_journals.id / gl_periods.id are UUID columns, so a
// malformed value would raise SQLSTATE 22P02 deep in the query and surface as an opaque HTTP 500 -
// and for the Exec-based writes (approve/void/close/reopen) it raises before any Get is reached, so
// a repo scan-map fix would miss them. Guarding here at the edge covers reads and writes alike.
// Mirrors branchIDParam/sourceRefIDParam: returns the valid id and true, or writes a 400 and returns
// false so the handler can short-circuit before touching the service.
func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !looksLikeUUID(id) {
		httpx.Error(w, http.StatusBadRequest, "invalid id")
		return "", false
	}
	// One canonical value for everything downstream: a uuid column compares case blind but a text column
	// (an approval request's subject_ref) does not, so the lower case form is what every read and write sees.
	return strings.ToLower(id), true
}

// - chart of accounts -

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	accts, err := h.svc.ListAccounts(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if accts == nil {
		accts = []Account{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": accts})
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in CreateAccountInput
	if !decodeJSON(w, r, &in) {
		return
	}
	a, err := h.svc.CreateAccount(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) deactivateAccount(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	a, err := h.svc.DeactivateAccount(r.Context(), r.PathValue("code"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

// - journals -

func (h *Handler) listJournals(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	branchID, ok := branchIDParam(w, r)
	if !ok {
		return
	}
	sourceRefID, ok := sourceRefIDParam(w, r)
	if !ok {
		return
	}
	// DIB-204: optional posted_at range. date_from/date_to are YYYY-MM-DD; the frontend's default
	// year/month picker sends the first and last day of the month, a custom range sends its bounds.
	dateFrom, ok := dateParam(w, r, "date_from")
	if !ok {
		return
	}
	dateTo, ok := dateParam(w, r, "date_to")
	if !ok {
		return
	}
	// Cursor pagination (HH-3.2 B4). Stable-ordering basis: fiscal_year
	// DESC, entry_no DESC; entry_no is unique within a fiscal year, so
	// (fiscal_year, entry_no) is the list's unique stable key, and the
	// cursor is that pair. Fetch limit+1 to learn whether a further page
	// exists, drop the extra row, mint next_cursor from the last kept row.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	fy, en, ok := parseJournalCursor(p.Cursor)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	f := JournalFilter{
		Status:           r.URL.Query().Get("status"),
		BranchID:         branchID,
		SourceRefID:      sourceRefID,
		DateFrom:         dateFrom,
		DateTo:           dateTo,
		Limit:            p.Limit + 1,
		AfterCursor:      ok && p.Cursor != "",
		CursorFiscalYear: fy,
		CursorEntryNo:    en,
	}
	js, err := h.svc.ListJournals(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	next := ""
	if len(js) > p.Limit {
		js = js[:p.Limit]
		next = journalCursorKey(js[len(js)-1].FiscalYear, js[len(js)-1].EntryNo)
	}
	httpx.WriteCursorPage(w, js, next, p.Limit)
}

// journalCursorKey mints the journal list's stable-ordering key (the
// fiscal_year/entry_no pair the list orders by).
func journalCursorKey(fiscalYear, entryNo int) string {
	return strconv.Itoa(fiscalYear) + "/" + strconv.Itoa(entryNo)
}

// parseJournalCursor decodes a minted key; ok is false for an empty cursor
// (first page) or one that does not carry the pair shape (already refused
// by ParseCursorPage's token check, refused again here rather than read as
// page one).
func parseJournalCursor(key string) (fy, en int, ok bool) {
	if key == "" {
		return 0, 0, true
	}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	var err1, err2 error
	fy, err1 = strconv.Atoi(parts[0])
	en, err2 = strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return fy, en, true
}

func (h *Handler) getJournal(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := h.svc.GetJournal(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, j)
}

func (h *Handler) createJournal(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in CreateJournalInput
	if !decodeJSON(w, r, &in) {
		return
	}
	j, err := h.svc.CreateJournal(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, j)
}

func (h *Handler) approveJournal(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := h.svc.ApproveJournal(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, j)
}

func (h *Handler) voidJournal(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := h.svc.VoidJournal(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, j)
}

func (h *Handler) reverseJournal(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := h.svc.ReverseJournal(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, j)
}

// - recurring -

func (h *Handler) listRecurring(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	ts, err := h.svc.ListRecurring(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if ts == nil {
		ts = []RecurringTemplate{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": ts})
}

func (h *Handler) runRecurring(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := h.svc.RunRecurring(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, j)
}

// - trial balance + tie-out -

func (h *Handler) trialBalance(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	branchID, ok := branchIDParam(w, r)
	if !ok {
		return
	}
	year := queryInt(r, "year", time.Now().Year())
	month := queryInt(r, "month", 0)
	res, err := h.svc.TrialBalance(r.Context(), year, month, branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) tieOut(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	branchID, ok := branchIDParam(w, r)
	if !ok {
		return
	}
	asOf := time.Now()
	if v := r.URL.Query().Get("as_of"); v != "" {
		// An explicit day is the dealer's day, cut at its last instant: the AR arm reads the house day
		// of this instant and the GL arm cuts posted_at at it, so both sides close the same day. A
		// bare time.Parse (midnight at Greenwich) is the previous evening to the AR arm.
		zone, zerr := houseday.Resolve(r.Context(), h.zone)
		if zerr != nil {
			h.writeErr(w, r, zerr)
			return
		}
		if t, err := zone.EndOfDay(v); err == nil {
			asOf = t
		}
	}
	states, err := h.svc.TieOut(r.Context(), asOf, branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if states == nil {
		states = []TieoutState{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": states})
}

// bistrackTieOut serves the M2-C1 static BisTrack GL reconciliation report for a fiscal period. Read-only,
// gated to Admin/Finance (same guard as the GP-parity export). Query: ?year=&month=&branch_id=&tolerance_cents=
// (tolerance defaults to 0 = exact). A closing balance is reconciled at ONE period end, so month is REQUIRED
// and must be 1-12; month=0 (whole year) is rejected by the service rather than silently reconciled against a
// single-period fixture. The fixture path is fixed (the committed static snapshot), not a caller input, so
// the endpoint can never be pointed at an arbitrary file. Returns the full tie-out report JSON.
func (h *Handler) bistrackTieOut(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	branchID, ok := branchIDParam(w, r)
	if !ok {
		return
	}
	// A dealer migration aid: on only where the dealer's configuration names a legacy import source.
	if !h.legacyImportEnabled(w, r) {
		return
	}
	year := queryInt(r, "year", time.Now().Year())
	month := queryInt(r, "month", 0) // 0 → rejected by the service (a closing balance needs an explicit month)
	tolerance := int64(queryInt(r, "tolerance_cents", 0))
	rep, err := h.svc.BisTrackTieOut(r.Context(), year, month, branchID, "", tolerance)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

// legacyImportEnabled answers whether this dealer names a legacy import source, writing the refusal
// itself when not (a plain 404 sentence) or when the read fails. No source wired is no source.
func (h *Handler) legacyImportEnabled(w http.ResponseWriter, r *http.Request) bool {
	src := ""
	if h.legacy != nil {
		var err error
		if src, err = h.legacy.LegacyImportSource(r.Context()); err != nil {
			h.writeErr(w, r, err)
			return false
		}
	}
	if src == "" {
		httpx.Error(w, http.StatusNotFound, "the legacy import check is not enabled for this dealer")
		return false
	}
	return true
}

// - periods -

func (h *Handler) listPeriods(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	ps, err := h.svc.ListPeriods(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if ps == nil {
		ps = []Period{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": ps})
}

type reasonBody struct {
	Reason string `json:"reason"`
}

func (h *Handler) closePeriod(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b reasonBody
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&b) // reason optional on close
	p, err := h.svc.ClosePeriod(r.Context(), id, b.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) reopenPeriod(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b reasonBody
	if !decodeJSON(w, r, &b) {
		return
	}
	p, err := h.svc.ReopenPeriod(r.Context(), id, b.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) closeYear(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var b struct {
		Year   int    `json:"year"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	if b.Year <= 0 {
		httpx.Error(w, http.StatusBadRequest, "gl: year is required")
		return
	}
	res, err := h.svc.CloseYear(r.Context(), b.Year, b.Reason)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// - policy + export -

func (h *Handler) policy(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	branchID, ok := branchIDParam(w, r)
	if !ok {
		return
	}
	p, err := h.svc.Policy(r.Context(), branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var b struct {
		Year     int    `json:"year"`
		Month    int    `json:"month"`
		BranchID string `json:"branch_id"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	// Same guard as branchIDParam, but branch_id here arrives in the JSON body rather than the
	// query string. Export feeds it into the same `($N = '' OR branch_id = $N::uuid)` predicate
	// (ExportRows), so a malformed value would raise SQLSTATE 22P02 → opaque HTTP 500. Reject it
	// at the edge; an empty branch_id stays the "all branches" case.
	if b.BranchID != "" && !looksLikeUUID(b.BranchID) {
		httpx.Error(w, http.StatusBadRequest, "invalid branch_id")
		return
	}
	res, err := h.svc.Export(r.Context(), b.Year, b.Month, b.BranchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

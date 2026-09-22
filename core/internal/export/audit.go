package export

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// audit.go, DIB-454 / DIB-405: the audit trail as a native .xlsx.
//
// IT READS THE SAME SERVICE THE SCREEN READS. `audit-log.ts` calls GET /api/v1/audit, which is
// audit.Service.List over audit.Filter. This consumer calls the same method with the same filter
// through the one-method interface below, exactly as the catalogue consumer does, so the file is the
// screen's rows, not a second query that will eventually disagree with them.
//
// THE COLUMN THAT NEEDED A DECISION IS THE PAYLOAD. An audit row's `changes` is free-form JSON
// written by a dozen modules. Three things follow from that:
//
//   - It is rendered as BEFORE and AFTER columns when the payload has that shape (the house
//     convention: {"before": …, "after": …}), and as one compact JSON column when it does not.
//     Flattening an arbitrary object into columns invents a schema the writers never agreed to.
//   - It carries control characters out of upstream systems. escapeXML drops the ones that are
//     illegal in XML 1.0 rather than escaping them, which is why a payload pasted out of a supplier
//     PDF cannot make the whole package unreadable (see the package doc, rule 3).
//   - It is CAPPED. A single 2 MB JSON blob in a cell is not evidence, it is a denial of service
//     against the reader, Excel's own hard limit is 32,767 characters per cell, and a file that
//     exceeds it does not open.

// auditCellLimit is the longest payload text one cell carries. Below Excel's 32,767-character
// maximum with room to spare, and truncation is MARKED (…) so a reader can tell a clipped payload
// from a short one.
const auditCellLimit = 4000

// AuditLister is the audit read, *audit.Service.
//
// One method, declared here in the consumer's terms. The export module does not decide how an audit
// row is read or who may read it; it asks the module that owns the trail.
type AuditLister interface {
	List(ctx context.Context, f audit.Filter) ([]audit.Entry, error)
}

// AuditQuery is the export's filter: the same four narrowing parameters GET /api/v1/audit takes, plus
// the human summary of them for the sheet's provenance line.
type AuditQuery struct {
	Filter audit.Filter
	// FilterSummary is the human rendering of the applied filters, stamped onto the sheet. An
	// extract with no provenance becomes "the audit log" in the hands of whoever opens it next.
	FilterSummary string
}

// AuditService builds the audit workbook.
type AuditService struct {
	audit AuditLister
	log   *slog.Logger
	now   func() time.Time
}

// NewAuditService builds the DIB-405 consumer over the audit read seam.
func NewAuditService(a AuditLister, log *slog.Logger) *AuditService {
	if log == nil {
		log = slog.Default()
	}
	return &AuditService{audit: a, log: log, now: time.Now}
}

// WithClock pins the clock (the generated-at line and the filename are otherwise untestable).
func (s *AuditService) WithClock(now func() time.Time) *AuditService {
	if now != nil {
		s.now = now
	}
	return s
}

// auditColumns is the sheet's column list, the ticket's "Date · Actor · Action · Entity · Before →
// After", with the two columns the screen also shows and an export needs more than the screen does.
//
// Entity TYPE and Entity ID are separate columns for the reason SKU got its own column in the
// catalogue: "order/9f3c…" in one cell is not filterable, and the id is the join key every
// downstream use of this file needs.
func auditColumns() []Column {
	return []Column{
		{Header: "Recorded at", Width: 20},
		{Header: "Action", Width: 26},
		{Header: "Entity type", Width: 18},
		{Header: "Entity ID", Width: 38},
		{Header: "Actor ID", Width: 38},
		{Header: "Actor role", Width: 16},
		{Header: "Branch ID", Width: 38},
		{Header: "Before", Width: 44},
		{Header: "After", Width: 44},
		{Header: "Request ID", Width: 26},
	}
}

// Workbook builds the audit export.
//
// A failed read is an ERROR, not an empty file. There is a real difference between "no audit rows
// matched your filter", which is an answer, and downloads as a workbook with headers and no rows -
// and "the audit trail could not be read", which must never arrive looking like a clean bill of
// health. The catalogue makes the same distinction for its product read.
func (s *AuditService) Workbook(ctx context.Context, q AuditQuery) (*Workbook, error) {
	rows, err := s.audit.List(ctx, q.Filter)
	if err != nil {
		return nil, fmt.Errorf("export: read audit trail: %w", err)
	}

	wb := New()
	sh := wb.AddSheet("Audit")
	sh.Title = "Audit trail"
	sh.Subtitle = auditSubtitle(len(rows), s.now(), q)
	sh.Columns = auditColumns()

	for _, e := range rows {
		before, after := auditPayload(e.Changes)
		sh.AddRow(
			Date(parseAuditTime(e.CreatedAt)),
			Str(e.Action),
			Str(e.EntityType),
			Str(e.EntityID),
			Str(e.ActorID),
			Str(e.ActorRole),
			Str(e.BranchID),
			Str(before),
			Str(after),
			Str(e.RequestID),
		)
	}
	return wb, nil
}

// auditSubtitle is the provenance line: how many rows, when it was pulled, and what filter produced
// it. The row COUNT is on the sheet because an audit extract's most dangerous misreading is "this is
// everything", the read is limited (audit.Filter.Limit clamps at 500), so a file that hit the
// ceiling says so.
func auditSubtitle(n int, at time.Time, q AuditQuery) string {
	parts := []string{plural(n, "entry", "entries")}
	if lim := clampedAuditLimit(q.Filter.Limit); n >= lim {
		parts = append(parts, "AT THE "+itoa(lim)+"-ROW LIMIT, narrow the filter to see more")
	}
	parts = append(parts, "generated "+at.Format("2006-01-02 15:04"))
	if s := strings.TrimSpace(q.FilterSummary); s != "" {
		parts = append(parts, "filter: "+s)
	}
	return strings.Join(parts, " · ")
}

// clampedAuditLimit mirrors audit's own clamp (1..500, default 200). Duplicated deliberately and
// narrowly: the alternative is exporting the clamp from the audit module so this file can render one
// sentence, and a mismatch here costs a slightly wrong warning, not wrong data.
func clampedAuditLimit(n int) int {
	switch {
	case n <= 0:
		return 200
	case n > 500:
		return 500
	default:
		return n
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

// auditPayload splits an audit row's `changes` into the Before and After cells.
//
// The house convention is {"before": …, "after": …}; anything else is rendered whole in the After
// column, with Before left BLANK rather than filled with "{}", an empty cell says "there was no
// prior state recorded", and a fabricated one says something that is not true.
func auditPayload(changes map[string]any) (before, after string) {
	if len(changes) == 0 {
		return "", ""
	}
	b, hasBefore := changes["before"]
	a, hasAfter := changes["after"]
	if hasBefore || hasAfter {
		return auditJSON(b), auditJSON(a)
	}
	return "", auditJSON(changes)
}

// auditJSON renders one payload side compactly and deterministically.
//
// Determinism matters here for the same reason the zip timestamps are fixed: two exports of the same
// audit rows must be byte-identical, and encoding/json already sorts map keys, so a diff between two
// files shows data changes rather than map-iteration order.
func auditJSON(v any) string {
	if v == nil {
		return ""
	}
	// A string payload is written as itself, not as a quoted JSON string: "approved" reads better in
	// a cell than "\"approved\"".
	if s, ok := v.(string); ok {
		return truncateCell(s)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		// Unmarshalable payloads are a fact about the row, not a reason to fail the export.
		return truncateCell(fmt.Sprintf("%v", v))
	}
	return truncateCell(string(raw))
}

// truncateCell caps a cell's text and MARKS that it did so.
func truncateCell(s string) string {
	if len(s) <= auditCellLimit {
		return s
	}
	// Cut on a rune boundary, half a multi-byte character is not text, and the writer would drop
	// the resulting invalid rune, silently changing the last visible character.
	cut := auditCellLimit
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// utf8RuneStart reports whether b begins a UTF-8 rune (i.e. is not a continuation byte).
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// parseAuditTime reads audit's RFC3339 timestamp string. An unparseable value yields the zero time,
// which Date() writes as a BLANK cell, never today's date, which would be a fabricated fact in an
// evidence file.
func parseAuditTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// AuditFilename names the audit download. Dated with the day it was pulled: an audit extract is a
// snapshot of a growing table, so two pulls a week apart are genuinely different files.
func AuditFilename(now time.Time) string {
	return "audit-" + now.Format("2006-01-02") + ".xlsx"
}

// ParseAuditFilter reads the audit export's query parameters, the same names, in the same
// spellings, that GET /api/v1/audit accepts.
//
// It VALIDATES actor_id and since rather than passing them through, mirroring audit's own handler:
// both flow into ::uuid / ::timestamptz casts, and a malformed one becomes a Postgres error that
// surfaces as a 500 for what is really bad client input.
func ParseAuditFilter(q map[string][]string) (audit.Filter, error) {
	get := func(k string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	actorID := get("actor_id")
	if actorID != "" && !looksLikeUUID(actorID) {
		return audit.Filter{}, errBadRequest("actor_id must be a valid UUID")
	}
	since := get("since")
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			return audit.Filter{}, errBadRequest("since must be an RFC3339 timestamp")
		}
	}
	limit := 0
	if raw := get("limit"); raw != "" {
		n, err := atoiStrict(raw)
		if err != nil || n < 0 {
			return audit.Filter{}, errBadRequest("limit must be a whole number, zero or more")
		}
		limit = n
	}
	return audit.Filter{
		EntityType: get("entity_type"),
		Action:     get("action"),
		ActorID:    actorID,
		Since:      since,
		Limit:      limit,
	}, nil
}

// AuditFilterSummary renders the applied filters for the sheet's provenance line, in a stable order
// so two exports of the same filter carry the same sentence.
func AuditFilterSummary(f audit.Filter) string {
	pairs := map[string]string{}
	if f.EntityType != "" {
		pairs["Entity"] = f.EntityType
	}
	if f.Action != "" {
		pairs["Action"] = f.Action
	}
	if f.ActorID != "" {
		pairs["Actor"] = f.ActorID
	}
	if f.Since != "" {
		pairs["Since"] = f.Since
	}
	if len(pairs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+": "+pairs[k])
	}
	return strings.Join(out, " · ")
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func atoiStrict(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			return 0, fmt.Errorf("too large")
		}
	}
	return n, nil
}

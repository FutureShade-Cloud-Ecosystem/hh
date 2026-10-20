package assist

// dataquery_run.go, data.query executed: compile over the asker's catalogue, check the plan, run the
// statement as the staff reader inside one read only transaction carrying the asker's signed claim, map
// every failure to a code, and turn the rows into a Result the service can cite.
//
// This file is the only place assist code runs a compiled query. It holds no connection of its own: the
// readtx.Reader it is given logs in as the assist login, which can reach nothing but the ai views.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// The fixed sentences. They are ours, they name no value and no estimate, and they are the only words
// a failed query puts in front of the model or the user.
const (
	TooBroadMessage     = "That question is too broad; narrow it by date, branch or customer."
	QueryUnavailable    = "I could not look that up just now."
	ClassRefusedMessage = "That is not shown for your role."
	maxResultBytes      = 24000
)

// QueryLogEntry is one row of assist_query_log: what was asked and what came back in outline, never
// result rows and never an error text.
type QueryLogEntry struct {
	ExchangeID string
	AskerID    string
	Audience   string
	Tool       string
	View       string
	Spec       json.RawMessage
	RowCount   int
	CitedIDs   []string
	Refusal    string
	TokensIn   int
	TokensOut  int
}

// QueryLog appends to assist_query_log.
type QueryLog interface {
	AppendQueryLog(ctx context.Context, e QueryLogEntry) error
}

// DataQuerier runs one data.query spec as the caller.
type DataQuerier interface {
	Query(ctx context.Context, c Caller, spec string) (Result, error)
}

// ReadDoer is the part of readtx.Reader the querier needs: one read only transaction under the
// asker's claim, handed to fn after the reader role is set. *readtx.Reader satisfies it.
type ReadDoer interface {
	Do(ctx context.Context, c readtx.Claims, fn func(tx pgx.Tx) error) error
}

// ReadQuerier is the DataQuerier over the assist read path.
type ReadQuerier struct {
	Reader   ReadDoer
	Zone     houseday.Source
	Now      func() time.Time
	Log      QueryLog
	Branches BranchReader
	Logger   *slog.Logger

	CostCeiling      float64
	SeqScanCostLimit float64
}

var _ DataQuerier = (*ReadQuerier)(nil)

func (q *ReadQuerier) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *ReadQuerier) ceiling() float64 {
	if q.CostCeiling > 0 {
		return q.CostCeiling
	}
	return DefaultCostCeiling
}

func (q *ReadQuerier) seqLimit() float64 {
	if q.SeqScanCostLimit > 0 {
		return q.SeqScanCostLimit
	}
	return DefaultSeqScanCostLimit
}

func (q *ReadQuerier) warn(msg string, args ...any) {
	if q.Logger != nil {
		q.Logger.Warn(msg, args...)
	}
}

// Query implements DataQuerier.
func (q *ReadQuerier) Query(ctx context.Context, c Caller, raw string) (Result, error) {
	entry := QueryLogEntry{ExchangeID: exchangeIDFrom(ctx), AskerID: c.UserID, Audience: readtx.AudienceStaff, Tool: "data.query"}
	if json.Valid([]byte(raw)) {
		entry.Spec = json.RawMessage(raw)
	} else {
		entry.Spec = json.RawMessage(`{"unparsed":true}`)
	}
	finish := func(res Result, err error) (Result, error) {
		q.append(ctx, entry)
		return res, err
	}
	refuse := func(code string, res Result) (Result, error) {
		entry.Refusal = code
		return finish(res, nil)
	}

	sp, err := ParseSpec(raw)
	if err != nil {
		entry.Refusal = "invalid"
		return finish(Result{}, specToBadArg(err))
	}
	entry.View = sp.View
	zone, err := houseday.Resolve(ctx, q.Zone)
	if err != nil {
		entry.Refusal = "unavailable"
		return finish(Result{Command: DataQueryCode, Headline: QueryUnavailable, Empty: true}, nil)
	}
	loc := zone.Location()
	now := q.now()
	comp, err := Compile(sp, builtinViews(), CatalogueFor(c), loc, now)
	if err != nil {
		var se *SpecError
		if errors.As(err, &se) && se.Kind == "class" {
			return refuse("class:"+se.Class, Result{
				Command: DataQueryCode, Headline: ClassRefusedMessage, Empty: true,
				Notice: &Notice{Code: NoticeRefused, Class: noticeClassFor(se.Class)},
			})
		}
		entry.Refusal = "invalid"
		return finish(Result{}, specToBadArg(err))
	}
	claims, err := c.ReadClaims()
	if err != nil {
		entry.Refusal = "claims"
		return finish(Result{}, ErrRefused)
	}

	type row map[string]string
	var rows []row
	var cites []Ref
	truncated := false
	var shape Shape
	runErr := q.Reader.Do(ctx, claims, func(tx pgx.Tx) error {
		// The plan first, in the same transaction and role: EXPLAIN does not execute the query.
		er, err := tx.Query(ctx, "EXPLAIN (FORMAT JSON) "+comp.SQL, comp.Args...)
		if err != nil {
			return readtx.MapError(err)
		}
		var plan []byte
		for er.Next() {
			if err := er.Scan(&plan); err != nil {
				er.Close()
				return readtx.MapError(err)
			}
		}
		er.Close()
		if err := er.Err(); err != nil {
			return readtx.MapError(err)
		}
		shape = CheckShape(plan, q.ceiling(), q.seqLimit())
		if shape.TooBroad {
			return errTooBroad
		}
		dr, err := tx.Query(ctx, comp.SQL, comp.Args...)
		if err != nil {
			return readtx.MapError(err)
		}
		defer dr.Close()
		size := 0
		for dr.Next() {
			vals, err := dr.Values()
			if err != nil {
				return readtx.MapError(err)
			}
			r := row{}
			for i, fd := range dr.FieldDescriptions() {
				s := ""
				if vals[i] != nil {
					s = fmt.Sprint(vals[i])
				}
				r[fd.Name] = s
				size += len(s)
			}
			if size > maxResultBytes {
				truncated = true
				break
			}
			rows = append(rows, r)
		}
		dr.Close()
		return readtx.MapError(dr.Err())
	})
	if runErr != nil {
		code := readtx.CodeUnavailable
		var re *readtx.Error
		switch {
		case errors.Is(runErr, errTooBroad):
			// The estimates are logged here, on the server, and nowhere else.
			q.warn("assist: data.query refused on plan shape", "reason", shape.Reason, "top_cost", shape.TopCost, "worst_scan", shape.WorstScan, "view", sp.View)
			return refuse("too_broad", tooBroadResult())
		case errors.As(runErr, &re):
			code = re.Code
		}
		q.warn("assist: data.query failed", "code", code, "view", sp.View)
		if code == readtx.CodeTimeout {
			return refuse("timeout", tooBroadResult())
		}
		return refuse(code, Result{Command: DataQueryCode, Headline: QueryUnavailable, Empty: true})
	}

	res := Result{Command: DataQueryCode, Items: []Item{}, Empty: len(rows) == 0}
	for _, r := range rows {
		it := Item{}
		for i, out := range comp.Outputs {
			v := formatValue(out, r[out.Name], loc)
			if i == 0 {
				it.Title = v
			}
			it.Fields = append(it.Fields, Field{Label: out.Label, Value: v})
		}
		if comp.Cites && r["__id"] != "" {
			label := strings.TrimSpace(comp.View.CiteLabelPrefix + r["__label"])
			ref := Ref{Kind: comp.View.CiteKind, ID: r["__id"], Label: label}
			it.Ref = &ref
			cites = append(cites, ref)
		}
		res.Items = append(res.Items, it)
	}
	res.Headline = headlineFor(comp, len(rows))
	if truncated {
		res.Note = "The result was cut to keep it short; narrow the question for the rest."
	} else if len(rows) >= comp.Limit && !comp.Aggregate {
		res.Note = "Showing the first " + strconv.Itoa(comp.Limit) + " rows; narrow the question for the rest."
	}
	res.Scope = q.scope(ctx, c, comp, loc, now)
	for _, ref := range cites {
		entry.CitedIDs = append(entry.CitedIDs, ref.ID)
	}
	entry.RowCount = len(rows)
	return finish(res, nil)
}

// DataQueryCode is the registry code of the tool.
const DataQueryCode = "data.query"

var errTooBroad = errors.New("assist: plan too broad")

func tooBroadResult() Result {
	return Result{Command: DataQueryCode, Headline: TooBroadMessage, Empty: true, Notice: &Notice{Code: NoticeTooBroad}}
}

func specToBadArg(err error) error {
	var se *SpecError
	if errors.As(err, &se) {
		return badArg("%s", se.Msg)
	}
	return badArg("The query spec was not usable.")
}

func (q *ReadQuerier) append(ctx context.Context, e QueryLogEntry) {
	if q.Log == nil {
		return
	}
	if err := q.Log.AppendQueryLog(context.WithoutCancel(ctx), e); err != nil {
		q.warn("assist: the query log row could not be written", "err_kind", "append")
	}
}

func headlineFor(c Compiled, n int) string {
	title := strings.ToLower(c.View.Title)
	if c.Aggregate {
		if n == 0 {
			return "No " + title + " matched."
		}
		return "Summary of " + title + " (" + strconv.Itoa(n) + " " + pluralWord(n, "group", "groups") + ")."
	}
	if n == 0 {
		return "No " + title + " matched."
	}
	return strconv.Itoa(n) + " " + pluralWord(n, strings.TrimSuffix(title, "s"), title) + "."
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// scope builds the answer's scope from the asker's claims and the query.
func (q *ReadQuerier) scope(ctx context.Context, c Caller, comp Compiled, loc *time.Location, now time.Time) *Scope {
	sc := &Scope{AsOf: now.In(loc).Format(time.RFC3339)}
	if c.Global {
		sc.AllBranches = true
	} else {
		labels := map[string]string{}
		if q.Branches != nil {
			if bs, err := q.Branches.Branches(ctx, c); err == nil {
				for _, b := range bs {
					labels[b.BranchID] = b.Name
				}
			}
		}
		for _, id := range c.BranchIDs {
			label := labels[id]
			if label == "" {
				label = unnamedBranch
			}
			sc.Branches = append(sc.Branches, ScopeBranch{ID: id, Label: label})
		}
	}
	if comp.PeriodFrom != "" {
		// An open ended period ("from the start of the month") runs to the day the query ran.
		to := comp.PeriodTo
		if to == "" {
			to = now.In(loc).Format("2006-01-02")
		}
		p := &ScopePeriod{From: comp.PeriodFrom, To: to}
		switch {
		case comp.PeriodTo != "" && comp.PeriodTo != comp.PeriodFrom:
			p.Label = comp.PeriodFrom + " to " + comp.PeriodTo
		case comp.PeriodTo == "":
			p.Label = "from " + comp.PeriodFrom
		default:
			p.Label = comp.PeriodFrom
		}
		sc.Period = p
	}
	return sc
}

// formatValue renders one cell by its column type, once, so the table and the sentence agree.
func formatValue(o Output, s string, loc *time.Location) string {
	if s == "" {
		return ""
	}
	switch o.Type {
	case typeCents:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return money(int64(f + sign(f)*0.5))
		}
	case typeNumeric:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return qty(f)
		}
	case typeTime:
		if t, ok := parsePGTime(s); ok {
			return t.In(loc).Format("2 Jan 2006 15:04")
		}
	case typeDate:
		if t, err := time.Parse("2006-01-02", s[:min(len(s), 10)]); err == nil {
			return t.Format("2 Jan 2006")
		}
	}
	return s
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

func parsePGTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999 -0700 MST", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func exchangeIDFrom(ctx context.Context) string {
	if st, ok := ctx.Value(exchangeKey{}).(*exchangeState); ok {
		return st.id
	}
	return ""
}

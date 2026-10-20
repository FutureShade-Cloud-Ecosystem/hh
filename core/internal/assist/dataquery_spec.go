package assist

// dataquery_spec.go, the closed grammar of data.query (plan r3 section 2) and the compiler that turns a
// spec into one parameterised statement over the asker's catalogue.
//
//   - The model never writes SQL. It sends a JSON spec; unknown fields are refused, so there is no
//     identity or scope field to smuggle (claims are never arguments).
//   - Identifiers come only from the catalogue (map keys, quoted with pgx.Identifier); every value is a bind
//     parameter with an explicit cast; limit is clamped.
//   - The class check runs on every referenced column: select, where, group by, order by and aggregates.
//   - Relative date words are resolved here, in the dealer's zone, before the statement exists.
//   - Error texts are ours and never echo what the model sent.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// OrderBy, Filter, AggSpec and Spec are the wire shape.
type OrderBy struct {
	Col string `json:"col"`
	Dir string `json:"dir"`
}

type Filter struct {
	Col   string          `json:"col"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

type AggSpec struct {
	Fn  string `json:"fn"`
	Col string `json:"col,omitempty"`
}

type Spec struct {
	View      string    `json:"view"`
	Select    []string  `json:"select,omitempty"`
	Where     []Filter  `json:"where,omitempty"`
	GroupBy   []string  `json:"group_by,omitempty"`
	Aggregate []AggSpec `json:"aggregate,omitempty"`
	OrderBy   []OrderBy `json:"order_by,omitempty"`
	Limit     int       `json:"limit,omitempty"`
}

// Limits of the grammar.
const (
	defaultLimit = 25
	maxLimit     = 100
	maxWhere     = 8
	maxInValues  = 50
	maxSelect    = 20
	maxAggs      = 4
	maxOrderBy   = 3
)

// SpecError is a refusal of a spec. Kind is "invalid" (malformed), "unknown" (a view or column the
// asker's catalogue does not carry) or "class" (a column the asker lacks the class for; Class names it).
type SpecError struct {
	Kind  string
	Class string
	Msg   string
}

func (e *SpecError) Error() string { return e.Msg }

func invalid(msg string) error { return &SpecError{Kind: "invalid", Msg: msg} }
func unknownRef() error {
	return &SpecError{Kind: "unknown", Msg: "That view or column is not in your catalogue."}
}

// ParseSpec decodes one spec strictly: one JSON object, no unknown field, nothing after it.
func ParseSpec(raw string) (Spec, error) {
	var s Spec
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Spec{}, invalid("The query spec was not a single valid JSON object of the documented shape.")
	}
	if dec.More() {
		return Spec{}, invalid("The query spec was not a single valid JSON object of the documented shape.")
	}
	return s, nil
}

// Output is one visible column of a result.
type Output struct {
	Name  string // key in the result row
	Label string
	Type  colType
	Agg   bool
	Class string // the column's class; a cost or margin cell the database masked is left out of the row
}

// Compiled is a spec turned into a statement.
type Compiled struct {
	SQL                  string
	Args                 []any
	Outputs              []Output
	View                 ViewDef
	Limit                int
	Aggregate            bool
	Cites                bool   // the statement also returns __id and __label, the row's citation
	PeriodFrom, PeriodTo string // the scope period read from a filter on the view's period column
}

// compiler holds one compilation.
type compiler struct {
	view       ViewDef // the asker's view
	full       ViewDef // the unfiltered view, to tell a class refusal from an unknown name
	loc        *time.Location
	now        time.Time
	args       []any
	where      []string
	pFrom, pTo string
}

// Compile turns a parsed spec into a statement over the asker's catalogue. full is the unfiltered
// catalogue (used only to name a withheld class); asker is what this asker may name.
func Compile(sp Spec, full, asker []ViewDef, loc *time.Location, now time.Time) (Compiled, error) {
	var v, fv ViewDef
	found := false
	for _, a := range asker {
		if a.Name == sp.View {
			v, found = a, true
		}
	}
	if !found {
		return Compiled{}, unknownRef()
	}
	for _, f := range full {
		if f.Name == sp.View {
			fv = f
		}
	}
	cp := &compiler{view: v, full: fv, loc: loc, now: now.In(loc)}
	return cp.compile(sp)
}

// ref resolves a column name: ok when the asker's view carries it; otherwise a class error if the
// unfiltered view carries it under a class the asker lacks, else unknown.
func (c *compiler) ref(name string, needVisible bool) (ColumnDef, error) {
	if col, ok := c.view.col(name); ok && (!needVisible || !col.Hidden) {
		return col, nil
	}
	if col, ok := c.full.col(name); ok && col.Class != ClassPlain && !col.Hidden {
		if _, held := c.view.col(name); !held {
			return ColumnDef{}, &SpecError{Kind: "class", Class: col.Class, Msg: "That column is not shown for your role."}
		}
	}
	return ColumnDef{}, unknownRef()
}

func (c *compiler) param(v any, cast string) string {
	c.args = append(c.args, v)
	return "$" + strconv.Itoa(len(c.args)) + "::" + cast
}

func qi(parts ...string) string { return pgx.Identifier(parts).Sanitize() }

func (c *compiler) compile(sp Spec) (Compiled, error) {
	if len(sp.Select) > maxSelect || len(sp.Where) > maxWhere || len(sp.Aggregate) > maxAggs || len(sp.OrderBy) > maxOrderBy || len(sp.GroupBy) > maxSelect {
		return Compiled{}, invalid("The query spec is larger than the grammar allows.")
	}
	aggregate := len(sp.Aggregate) > 0
	if len(sp.GroupBy) > 0 && !aggregate {
		return Compiled{}, invalid("group_by needs an aggregate.")
	}

	// -- the select list -------------------------------------------------------------------------------
	var outs []Output
	var selects []string
	seen := map[string]bool{}
	addPlain := func(name string) error {
		if seen[name] {
			return nil
		}
		col, err := c.ref(name, true)
		if err != nil {
			return err
		}
		if aggregate && !col.Group {
			return invalid("An aggregate query may only select the columns it groups by.")
		}
		seen[name] = true
		outs = append(outs, Output{Name: name, Label: col.Label, Type: col.Type, Class: col.Class})
		selects = append(selects, fmt.Sprintf("(%s)::text AS %s", qi(name), qi(name)))
		return nil
	}
	var groups []string
	for _, g := range sp.GroupBy {
		col, err := c.ref(g, true)
		if err != nil {
			return Compiled{}, err
		}
		if !col.Group {
			return Compiled{}, invalid("That column cannot be grouped by.")
		}
		groups = append(groups, qi(g))
		if err := addPlain(g); err != nil {
			return Compiled{}, err
		}
	}
	if aggregate {
		for _, s := range sp.Select {
			if !hasString(sp.GroupBy, s) {
				if _, err := c.ref(s, true); err != nil {
					return Compiled{}, err
				}
				return Compiled{}, invalid("An aggregate query may only select the columns it groups by.")
			}
		}
		for _, a := range sp.Aggregate {
			out, expr, err := c.aggregate(a)
			if err != nil {
				return Compiled{}, err
			}
			if seen[out.Name] {
				continue
			}
			seen[out.Name] = true
			outs = append(outs, out)
			selects = append(selects, fmt.Sprintf("(%s)::text AS %s", expr, qi(out.Name)))
		}
	} else {
		names := sp.Select
		if len(names) == 0 {
			names = c.view.Default
		}
		for _, n := range names {
			if err := addPlain(n); err != nil {
				return Compiled{}, err
			}
		}
	}

	// -- the filters -----------------------------------------------------------------------------------
	for _, f := range sp.Where {
		if err := c.filter(f); err != nil {
			return Compiled{}, err
		}
	}

	// -- order and limit -------------------------------------------------------------------------------
	var orders []string
	outNames := map[string]bool{}
	for _, o := range outs {
		outNames[o.Name] = true
	}
	for _, ob := range sp.OrderBy {
		if ob.Dir != "" && ob.Dir != "asc" && ob.Dir != "desc" {
			return Compiled{}, invalid("order_by dir must be asc or desc.")
		}
		if !outNames[ob.Col] {
			if aggregate {
				return Compiled{}, invalid("An aggregate query may only order by the columns it returns.")
			}
			if _, err := c.ref(ob.Col, true); err != nil {
				return Compiled{}, err
			}
		}
		dir := "ASC"
		if ob.Dir == "desc" {
			dir = "DESC"
		}
		orders = append(orders, qi(ob.Col)+" "+dir+" NULLS LAST")
	}
	if len(orders) == 0 && !aggregate {
		for _, d := range c.view.DefaultOrder {
			if _, ok := c.view.col(d.Col); ok {
				dir := "ASC"
				if d.Dir == "desc" {
					dir = "DESC"
				}
				orders = append(orders, qi(d.Col)+" "+dir+" NULLS LAST")
			}
		}
	}
	if len(orders) == 0 && aggregate && len(groups) > 0 {
		orders = append(orders, strings.Join(groups, ", "))
	}
	limit := sp.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	cites := !aggregate && c.view.CiteIDCol != ""
	if cites {
		selects = append(selects,
			fmt.Sprintf("(%s)::text AS %s", qi(c.view.CiteIDCol), qi("__id")),
			fmt.Sprintf("(%s)::text AS %s", qi(c.view.CiteLabelCol), qi("__label")))
	}

	var b strings.Builder
	b.WriteString("SELECT " + strings.Join(selects, ", ") + " FROM " + qi(c.view.Schema, c.view.Table))
	if len(c.where) > 0 {
		b.WriteString(" WHERE " + strings.Join(c.where, " AND "))
	}
	if len(groups) > 0 {
		b.WriteString(" GROUP BY " + strings.Join(groups, ", "))
	}
	if len(orders) > 0 {
		b.WriteString(" ORDER BY " + strings.Join(orders, ", "))
	}
	c.args = append(c.args, int64(limit))
	b.WriteString(" LIMIT $" + strconv.Itoa(len(c.args)))
	return Compiled{
		SQL: b.String(), Args: c.args, Outputs: outs, View: c.view, Limit: limit, Aggregate: aggregate,
		Cites: cites, PeriodFrom: c.pFrom, PeriodTo: c.pTo,
	}, nil
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// aggregate compiles one aggregate and names its output.
func (c *compiler) aggregate(a AggSpec) (Output, string, error) {
	switch a.Fn {
	case "count":
		if a.Col == "" {
			return Output{Name: "count", Label: "Count", Type: typeInt, Agg: true}, "count(*)", nil
		}
		col, err := c.ref(a.Col, true)
		if err != nil {
			return Output{}, "", err
		}
		return Output{Name: "count_" + a.Col, Label: "Count of " + strings.ToLower(col.Label), Type: typeInt, Agg: true, Class: col.Class}, "count(" + qi(a.Col) + ")", nil
	case "sum", "avg", "min", "max":
		if a.Col == "" {
			return Output{}, "", invalid("That aggregate needs a column.")
		}
		col, err := c.ref(a.Col, true)
		if err != nil {
			return Output{}, "", err
		}
		if !col.Agg {
			return Output{}, "", invalid("That column cannot be aggregated.")
		}
		numeric := col.Type == typeCents || col.Type == typeInt || col.Type == typeNumeric
		if (a.Fn == "sum" || a.Fn == "avg") && !numeric {
			return Output{}, "", invalid("That aggregate needs a numeric column.")
		}
		typ := col.Type
		if a.Fn == "avg" && typ == typeInt {
			typ = typeNumeric
		}
		label := map[string]string{"sum": "Total ", "avg": "Average ", "min": "Lowest ", "max": "Highest "}[a.Fn] + strings.ToLower(col.Label)
		return Output{Name: a.Fn + "_" + a.Col, Label: label, Type: typ, Agg: true, Class: col.Class}, a.Fn + "(" + qi(a.Col) + ")", nil
	}
	return Output{}, "", invalid("That aggregate function is not allowed.")
}

// filter compiles one where entry.
func (c *compiler) filter(f Filter) error {
	col, err := c.ref(f.Col, false)
	if err != nil {
		return err
	}
	if !col.Filter {
		return invalid("That column cannot be filtered.")
	}
	name := qi(f.Col)
	switch f.Op {
	case "is_null":
		var yes bool
		if json.Unmarshal(f.Value, &yes) != nil && len(bytes.TrimSpace(f.Value)) != 0 {
			return invalid("is_null takes true or false.")
		}
		if len(bytes.TrimSpace(f.Value)) == 0 {
			yes = true
		}
		if yes {
			c.where = append(c.where, name+" IS NULL")
		} else {
			c.where = append(c.where, name+" IS NOT NULL")
		}
		return nil
	case "contains":
		if !col.Search || col.Type != typeText {
			return invalid("contains is not available on that column.")
		}
		var s string
		if json.Unmarshal(f.Value, &s) != nil || strings.TrimSpace(s) == "" || len(s) > 100 {
			return invalid("contains takes a short piece of text.")
		}
		c.where = append(c.where, fmt.Sprintf("position(lower(%s) in lower(%s)) > 0", c.param(s, "text"), name))
		return nil
	case "in":
		return c.filterIn(col, name, f)
	case "between":
		var two []json.RawMessage
		if json.Unmarshal(f.Value, &two) != nil || len(two) != 2 {
			return invalid("between takes exactly two values.")
		}
		lo, err := c.value(col, two[0])
		if err != nil {
			return err
		}
		hi, err := c.value(col, two[1])
		if err != nil {
			return err
		}
		c.notePeriod(f.Col, &lo, &hi)
		c.where = append(c.where, c.cmp(col, name, ">=", lo)+" AND "+c.cmp(col, name, "<=", hi))
		return nil
	case "=", "!=", "<", "<=", ">", ">=":
		if col.Type == typeText && f.Op != "=" && f.Op != "!=" {
			return invalid("That operator is not available on text.")
		}
		if col.Type == typeUUID && f.Op != "=" && f.Op != "!=" {
			return invalid("That operator is not available on ids.")
		}
		v, err := c.value(col, f.Value)
		if err != nil {
			return err
		}
		c.notePeriod(f.Col, &v, nil)
		c.where = append(c.where, c.cmp(col, name, f.Op, v))
		return nil
	}
	return invalid("That operator is not allowed.")
}

// parsed is one value read for a column.
type parsed struct {
	s        string
	i        int64
	t        time.Time
	dateOnly bool
	f        string // numeric text
}

func (c *compiler) value(col ColumnDef, raw json.RawMessage) (parsed, error) {
	raw = bytes.TrimSpace(raw)
	switch col.Type {
	case typeText, typeUUID:
		var s string
		if json.Unmarshal(raw, &s) != nil || len(s) == 0 || len(s) > 200 {
			return parsed{}, invalid("That value must be short text.")
		}
		if col.Type == typeUUID && !uuidShape.MatchString(s) {
			return parsed{}, invalid("That value must be an id.")
		}
		return parsed{s: s}, nil
	case typeInt, typeCents:
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return parsed{}, invalid("That value must be a whole number.")
		}
		i, err := n.Int64()
		if err != nil {
			return parsed{}, invalid("That value must be a whole number.")
		}
		return parsed{i: i}, nil
	case typeNumeric:
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return parsed{}, invalid("That value must be a number.")
		}
		if _, err := strconv.ParseFloat(n.String(), 64); err != nil {
			return parsed{}, invalid("That value must be a number.")
		}
		return parsed{f: n.String()}, nil
	case typeDate, typeTime:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return parsed{}, invalid("That value must be a date.")
		}
		t, dateOnly, ok := c.day(strings.TrimSpace(s))
		if !ok {
			return parsed{}, invalid("That value is not a date, a date and time, or a known date word.")
		}
		return parsed{t: t, dateOnly: dateOnly}, nil
	}
	return parsed{}, invalid("That value is not usable.")
}

// day resolves a date word or a literal, in the dealer's zone.
func (c *compiler) day(s string) (t time.Time, dateOnly, ok bool) {
	today := time.Date(c.now.Year(), c.now.Month(), c.now.Day(), 0, 0, 0, 0, c.loc)
	switch s {
	case "today":
		return today, true, true
	case "yesterday":
		return today.AddDate(0, 0, -1), true, true
	case "tomorrow":
		return today.AddDate(0, 0, 1), true, true
	case "start_of_week":
		return weekStart(today), true, true
	case "start_of_last_week":
		return weekStart(today).AddDate(0, 0, -7), true, true
	case "start_of_month":
		return time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, c.loc), true, true
	case "start_of_last_month":
		return time.Date(today.Year(), today.Month()-1, 1, 0, 0, 0, 0, c.loc), true, true
	case "start_of_year":
		return time.Date(today.Year(), 1, 1, 0, 0, 0, 0, c.loc), true, true
	}
	if strings.HasSuffix(s, "_days_ago") {
		if n, err := strconv.Atoi(strings.TrimSuffix(s, "_days_ago")); err == nil && n >= 0 && n <= 3660 {
			return today.AddDate(0, 0, -n), true, true
		}
		return time.Time{}, false, false
	}
	if d, err := time.ParseInLocation("2006-01-02", s, c.loc); err == nil {
		return d, true, true
	}
	if d, err := time.Parse(time.RFC3339, s); err == nil {
		return d, false, true
	}
	return time.Time{}, false, false
}

// cmp renders one comparison with its bind parameters.
func (c *compiler) cmp(col ColumnDef, name, op string, v parsed) string {
	sqlOp := op
	if op == "!=" {
		sqlOp = "<>"
	}
	switch col.Type {
	case typeText:
		return fmt.Sprintf("lower(%s) %s lower(%s)", name, sqlOp, c.param(v.s, "text"))
	case typeUUID:
		return fmt.Sprintf("%s %s %s", name, sqlOp, c.param(v.s, "uuid"))
	case typeInt, typeCents:
		return fmt.Sprintf("%s %s %s", name, sqlOp, c.param(v.i, "bigint"))
	case typeNumeric:
		return fmt.Sprintf("%s %s %s", name, sqlOp, c.param(v.f, "numeric"))
	case typeDate:
		return fmt.Sprintf("%s %s %s", name, sqlOp, c.param(v.t.Format("2006-01-02"), "date"))
	}
	// typeTime. A date alone means the whole of that day in the dealer's zone.
	if !v.dateOnly {
		return fmt.Sprintf("%s %s %s", name, sqlOp, c.param(v.t, "timestamptz"))
	}
	start, next := v.t, v.t.AddDate(0, 0, 1)
	switch op {
	case "=":
		return fmt.Sprintf("(%s >= %s AND %s < %s)", name, c.param(start, "timestamptz"), name, c.param(next, "timestamptz"))
	case "!=":
		return fmt.Sprintf("(%s < %s OR %s >= %s)", name, c.param(start, "timestamptz"), name, c.param(next, "timestamptz"))
	case ">=":
		return fmt.Sprintf("%s >= %s", name, c.param(start, "timestamptz"))
	case ">":
		return fmt.Sprintf("%s >= %s", name, c.param(next, "timestamptz"))
	case "<":
		return fmt.Sprintf("%s < %s", name, c.param(start, "timestamptz"))
	default: // <=
		return fmt.Sprintf("%s < %s", name, c.param(next, "timestamptz"))
	}
}

func (c *compiler) filterIn(col ColumnDef, name string, f Filter) error {
	var vals []json.RawMessage
	if json.Unmarshal(f.Value, &vals) != nil || len(vals) == 0 || len(vals) > maxInValues {
		return invalid("in takes a short list of values.")
	}
	switch col.Type {
	case typeText:
		list := make([]string, 0, len(vals))
		for _, r := range vals {
			v, err := c.value(col, r)
			if err != nil {
				return err
			}
			list = append(list, strings.ToLower(v.s))
		}
		c.where = append(c.where, fmt.Sprintf("lower(%s) = ANY(%s)", name, c.param(list, "text[]")))
	case typeUUID:
		list := make([]string, 0, len(vals))
		for _, r := range vals {
			v, err := c.value(col, r)
			if err != nil {
				return err
			}
			list = append(list, v.s)
		}
		c.where = append(c.where, fmt.Sprintf("%s = ANY(%s)", name, c.param(list, "uuid[]")))
	case typeInt, typeCents:
		list := make([]int64, 0, len(vals))
		for _, r := range vals {
			v, err := c.value(col, r)
			if err != nil {
				return err
			}
			list = append(list, v.i)
		}
		c.where = append(c.where, fmt.Sprintf("%s = ANY(%s)", name, c.param(list, "bigint[]")))
	default:
		return invalid("in is not available on that column.")
	}
	return nil
}

// notePeriod records the scope period from a filter on the view's period column.
func (c *compiler) notePeriod(col string, a, b *parsed) {
	if col != c.view.Period || a == nil {
		return
	}
	if a.t.IsZero() {
		return
	}
	day := func(p *parsed) string { return p.t.In(c.loc).Format("2006-01-02") }
	if b != nil && !b.t.IsZero() {
		c.pFrom, c.pTo = day(a), day(b)
		return
	}
	if c.pFrom == "" {
		c.pFrom = day(a)
	}
}

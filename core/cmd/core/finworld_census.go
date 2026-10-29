//go:build finworld

package main

// finworld_census.go, the world's books census (W2-1d item 1). On every screen
// a tester sees, the world must read like a real dealer's books: names a
// person would give a business, payables paid on their terms with only a few
// intended late ones, and memos in a bookkeeper's words, never engine words.
//
// The census builds the plan alone (no database: the plan carries the names and
// the bookkeeper text the world posts) and fails on:
//
//   - any customer, vendor, carrier or broker name that ends in a number or
//     carries an id shape (lc-, pay-, -s1-, a UUID, or a run of digits);
//   - any plan-carried journal memo, bank line description or document note in
//     engine words (bankrec, finworld, pay-s1, lc-s1, an idempotency key, a
//     UUID). The memos the product code composes around the plan's keys ride
//     the source scan below, which walks the memo positions of the packages the
//     world's journals come from and fails on an engine-word literal or a key
//     interpolated into a memo, note, description or reason;
//   - any carrier or broker payable still open more than 90 days past its due
//     date that the plan did not name in IntendedLatePayables with a reason.
//     At most a handful are intended.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// finworldVendorTermsDays is the payment terms every world vendor trades on,
// the same figure the driver writes into vendors.payment_terms_days.
const finworldVendorTermsDays = 30

// finworldIntendedLatePayableCap is the most payables the plan may mark as
// intended late: a handful, each with its reason. More than this and the aging
// stops reading like a real dealer's books.
const finworldIntendedLatePayableCap = 8

var (
	finworldEngineWord    = regexp.MustCompile(`(?i)(bankrec|finworld|pay-s1|lc-s1)`)
	finworldKeyShape      = regexp.MustCompile(`-s[0-9]+-[0-9]+`)
	finworldUUIDShape     = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	finworldNameIDPrefix  = regexp.MustCompile(`(?i)(^|[^a-z])(lc|pay)-`)
	finworldAnyDigit      = regexp.MustCompile(`[0-9]`)
	finworldNameDigitTail = func(name string) bool {
		t := strings.TrimRight(name, " .")
		return len(t) > 0 && t[len(t)-1] >= '0' && t[len(t)-1] <= '9'
	}
)

// finworldNameFinding explains one name the census refuses.
type finworldNameFinding struct {
	Kind string
	Key  string
	Name string
	Why  string
}

func (f finworldNameFinding) String() string {
	return fmt.Sprintf("%s %s: name %q %s", f.Kind, f.Key, f.Name, f.Why)
}

// finworldNameProblems answers why a name does not read like a business name,
// or "" when it does. A name ends in a number or carries an id shape (lc-,
// pay-, -s1-, a UUID, or a run of digits) is engine data, not a name.
func finworldNameProblems(name string) string {
	switch {
	case finworldNameDigitTail(name):
		return "ends in a number"
	case finworldAnyDigit.MatchString(name):
		return "carries a run of digits"
	case finworldUUIDShape.MatchString(name):
		return "carries a UUID"
	case finworldKeyShape.MatchString(name):
		return "carries an id key shape (-s1-)"
	case finworldNameIDPrefix.MatchString(name):
		return "carries an id prefix (lc- or pay-)"
	}
	return ""
}

// finworldCensusNames walks every name the plan gives a person-shaped thing:
// the customers, and every vendor (goods supplier, carrier or broker). The
// dealer's own display name is an input from the deploy setting, not invented
// data, so the census does not judge it.
func finworldCensusNames(plan *FinWorldPlanT) []finworldNameFinding {
	var out []finworldNameFinding
	check := func(kind, key, name string) {
		if why := finworldNameProblems(name); why != "" {
			out = append(out, finworldNameFinding{Kind: kind, Key: key, Name: name, Why: why})
		}
	}
	for i := range plan.Contractors {
		c := &plan.Contractors[i]
		check("customer", c.Key, c.Name)
	}
	for i := range plan.Homeowners {
		c := &plan.Homeowners[i]
		check("customer", c.Key, c.Name)
	}
	for i := range plan.Vendors {
		v := &plan.Vendors[i]
		check("vendor", v.VendorNo, v.Name)
	}
	return out
}

// finworldMemoFinding explains one bookkeeper text that carries engine words.
type finworldMemoFinding struct {
	Where string
	Text  string
	Why   string
}

func (f finworldMemoFinding) String() string {
	return fmt.Sprintf("%s: %q %s", f.Where, f.Text, f.Why)
}

// finworldMemoProblems answers why a memo, bank line description or document
// note is in engine words, or "" when it reads like a bookkeeper wrote it.
func finworldMemoProblems(text string) string {
	switch {
	case finworldEngineWord.MatchString(text):
		return "names an engine (bankrec, finworld, pay-s1, lc-s1)"
	case finworldUUIDShape.MatchString(text):
		return "carries a UUID"
	case finworldKeyShape.MatchString(text):
		return "carries an idempotency key shape (-s1-)"
	}
	return ""
}

// finworldCensusMemos walks every plan-carried journal memo, bank line
// description and document note: the manual journal's description and its line
// memos, the landed cost's description and void reason, the credit memo's
// description, the till movement's reason, and every bank statement line's
// description. The keys and references ride their own columns, where they
// belong; only the bookkeeper words are censused.
func finworldCensusMemos(plan *FinWorldPlanT) []finworldMemoFinding {
	var out []finworldMemoFinding
	add := func(where, text string) {
		if why := finworldMemoProblems(text); why != "" {
			out = append(out, finworldMemoFinding{Where: where, Text: text, Why: why})
		}
	}
	for mIdx := range plan.Months {
		for dIdx := range plan.Months[mIdx].TradingDays {
			for _, ev := range plan.Months[mIdx].TradingDays[dIdx].Events {
				where := fmt.Sprintf("%s %s", ev.Date, ev.EventKey)
				if mj := ev.ManualJournal; mj != nil {
					add(where+" manual journal", mj.Description)
					for _, ln := range mj.Lines {
						add(fmt.Sprintf("%s manual journal line %d", where, ln.LineNo), ln.Memo)
					}
				}
				if lc := ev.LandedCost; lc != nil {
					add(where+" landed cost note", lc.Description)
					add(where+" landed cost void reason", lc.VoidReason)
				}
				if cm := ev.CreditMemo; cm != nil {
					add(where+" credit memo note", cm.Description)
				}
				if pio := ev.TillPaidInOut; pio != nil {
					add(where+" till movement reason", pio.Reason)
				}
				if bs := ev.BankStatement; bs != nil {
					for _, ln := range bs.Lines {
						add(fmt.Sprintf("%s bank line %d", where, ln.LineNo), ln.Description)
					}
				}
			}
		}
	}
	return out
}

// finworldLatePayableFinding explains one carrier or broker payable the world
// leaves open more than 90 days past its due date without the plan naming it
// as intended.
type finworldLatePayableFinding struct {
	LandedCostKey  string
	AppliedDate    string
	DueDate        string
	ChargeCadCents int64
	Why            string
}

func (f finworldLatePayableFinding) String() string {
	return fmt.Sprintf("landed cost %s (applied %s, due %s, %d CAD cents): %s",
		f.LandedCostKey, f.AppliedDate, f.DueDate, f.ChargeCadCents, f.Why)
}

// finworldCensusLatePayables fails on any carrier or broker payable left open
// more than 90 days past its terms that the plan did not mark as an intended
// late payable. A handful are allowed, each named with its reason; the census
// also refuses an intended-late entry that names no real late payable, and a
// plan that marks more than the cap.
func finworldCensusLatePayables(plan *FinWorldPlanT) []finworldLatePayableFinding {
	anchorEnd, err := time.ParseInLocation("2006-01-02", plan.Months[11].LastTradingDay, time.UTC)
	if err != nil {
		return []finworldLatePayableFinding{{Why: fmt.Sprintf("the anchor month's last trading day does not parse: %v", err)}}
	}

	intended := map[string]string{}
	for _, ip := range plan.IntendedLatePayables {
		intended[ip.LandedCostKey] = ip.Reason
	}

	var late []finworldLatePayableFinding
	lateKeys := map[string]bool{}
	for mIdx := range plan.Months {
		for dIdx := range plan.Months[mIdx].TradingDays {
			for _, ev := range plan.Months[mIdx].TradingDays[dIdx].Events {
				lc := ev.LandedCost
				if lc == nil || lc.Status != "POSTED" {
					continue
				}
				applied, perr := time.ParseInLocation("2006-01-02", lc.AppliedDate, time.UTC)
				if perr != nil {
					late = append(late, finworldLatePayableFinding{LandedCostKey: lc.LandedCostKey, Why: fmt.Sprintf("the applied date does not parse: %v", perr)})
					lateKeys[lc.LandedCostKey] = true
					continue
				}
				due := applied.AddDate(0, 0, finworldVendorTermsDays)
				if anchorEnd.Sub(due) <= 90*24*time.Hour {
					continue // not yet 90 days past its terms
				}
				if lc.SettledOnDate != "" {
					if settled, serr := time.ParseInLocation("2006-01-02", lc.SettledOnDate, time.UTC); serr == nil && !settled.After(due.AddDate(0, 0, 90)) {
						continue // paid within the 90 days the Controller chases
					}
				}
				lateKeys[lc.LandedCostKey] = true
				if reason, ok := intended[lc.LandedCostKey]; ok && strings.TrimSpace(reason) != "" {
					continue // named as intended, with the bookkeeper's reason
				}
				late = append(late, finworldLatePayableFinding{
					LandedCostKey:  lc.LandedCostKey,
					AppliedDate:    lc.AppliedDate,
					DueDate:        due.Format("2006-01-02"),
					ChargeCadCents: lc.ChargeCadCents,
					Why:            "open more than 90 days past its terms and the plan does not mark it as an intended late payable",
				})
			}
		}
	}

	for key, reason := range intended {
		if !lateKeys[key] {
			late = append(late, finworldLatePayableFinding{LandedCostKey: key, Why: fmt.Sprintf("the plan marks it as an intended late payable but it is not one (reason %q)", reason)})
		}
	}
	if len(plan.IntendedLatePayables) > finworldIntendedLatePayableCap {
		late = append(late, finworldLatePayableFinding{Why: fmt.Sprintf("the plan marks %d payables as intended late; at most %d are intended", len(plan.IntendedLatePayables), finworldIntendedLatePayableCap)})
	}

	sort.Slice(late, func(i, j int) bool { return late[i].LandedCostKey < late[j].LandedCostKey })
	return late
}

// finworldSourceMemoFinding is one memo position in product source that
// carries engine words or interpolates a key.
type finworldSourceMemoFinding struct {
	File string
	Line int
	Text string
	Why  string
}

func (f finworldSourceMemoFinding) String() string {
	return fmt.Sprintf("%s:%d: %q %s", f.File, f.Line, f.Text, f.Why)
}

// finworldMemoFieldNames are the composite-literal and map-literal keys whose
// value is bookkeeper-visible words: journal memos, document notes,
// descriptions, reasons, labels. Keys and references (reference, notes,
// bill_no, idempotency_key, po_ref) ride their own columns and stay keys.
var finworldMemoFieldNames = map[string]bool{
	"memo":          true,
	"description":   true,
	"reason":        true,
	"note":          true,
	"payeelabel":    true,
	"voidreason":    true,
	"variancenotes": true,
}

// finworldCensusSourceScan walks the product source the world's journals come
// from (bankrec, landedcost, purchasing, and the finworld driver and plan
// files) and fails on a memo-position literal in engine words, or a key
// interpolated into a memo position: an idempotency key or event key riding
// into bookkeeper words.
func finworldCensusSourceScan(coreRoot string) ([]finworldSourceMemoFinding, error) {
	dirs := []string{
		filepath.Join(coreRoot, "internal", "bankrec"),
		filepath.Join(coreRoot, "internal", "landedcost"),
		filepath.Join(coreRoot, "internal", "purchasing"),
	}
	cmdCore := filepath.Join(coreRoot, "cmd", "core")
	if entries, err := os.ReadDir(cmdCore); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), "finworld_") && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				dirs = append(dirs, filepath.Join(cmdCore, e.Name()))
			}
		}
	}

	var out []finworldSourceMemoFinding
	seen := map[string]bool{}
	for _, dir := range dirs {
		stat, err := os.Stat(dir)
		if err != nil || !stat.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var files []string
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			files = append(files, filepath.Join(dir, e.Name()))
		}
		sort.Strings(files)
		for _, path := range files {
			found, err := finworldScanFileMemoPositions(path)
			if err != nil {
				return nil, err
			}
			for _, f := range found {
				if seen[f.String()] {
					continue
				}
				seen[f.String()] = true
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// finworldScanFileMemoPositions parses one Go file and walks every composite
// literal and map literal, collecting the values of memo-position keys. A
// value fails when a string literal inside it (a plain literal, a part of a
// concatenation, or a format string) is in engine words, or when the value
// interpolates a key-named expression (a selector or identifier whose name
// ends in Key, an event or idempotency key riding into bookkeeper words).
func finworldScanFileMemoPositions(path string) ([]finworldSourceMemoFinding, error) {
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	base := filepath.Base(path)
	var out []finworldSourceMemoFinding

	checkValue := func(keyName string, expr ast.Expr) {
		lower := strings.ToLower(strings.ReplaceAll(keyName, "_", ""))
		if !finworldMemoFieldNames[lower] {
			return
		}
		lit := ""
		keyExpr := ""
		engine := false
		ast.Inspect(expr, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				if v.Kind == token.STRING {
					text := strings.Trim(v.Value, "`\"")
					if finworldMemoProblems(text) != "" {
						engine = true
						if lit == "" {
							lit = text
						}
					} else if lit == "" {
						lit = text
					}
				}
			case *ast.SelectorExpr:
				if strings.HasSuffix(v.Sel.Name, "Key") && keyExpr == "" {
					keyExpr = v.Sel.Name
				}
			case *ast.Ident:
				if strings.HasSuffix(v.Name, "Key") && keyExpr == "" {
					keyExpr = v.Name
				}
			}
			return true
		})
		why := ""
		if engine {
			why = "carries engine words in a memo position"
		} else if keyExpr != "" {
			why = fmt.Sprintf("interpolates the key %s into a memo position", keyExpr)
		}
		if why != "" {
			out = append(out, finworldSourceMemoFinding{
				File: base,
				Line: fset.Position(expr.Pos()).Line,
				Text: lit,
				Why:  why,
			})
		}
	}

	for _, decl := range fileAST.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, val := range vs.Values {
						ast.Inspect(val, func(n ast.Node) bool {
							if comp, ok := n.(*ast.CompositeLit); ok {
								finworldScanComposite(fset, base, comp, checkValue)
							}
							return true
						})
					}
				}
			}
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			ast.Inspect(d.Body, func(n ast.Node) bool {
				if comp, ok := n.(*ast.CompositeLit); ok {
					finworldScanComposite(fset, base, comp, checkValue)
				}
				return true
			})
		}
	}
	return out, nil
}

// finworldScanComposite checks one composite literal's keyed elements (a
// struct literal's fields or a map literal's keys) against the memo-position
// names.
func finworldScanComposite(fset *token.FileSet, base string, comp *ast.CompositeLit, check func(keyName string, expr ast.Expr)) {
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		var keyName string
		switch k := kv.Key.(type) {
		case *ast.Ident:
			keyName = k.Name
		case *ast.BasicLit:
			keyName = strings.Trim(k.Value, "\"`")
		default:
			continue
		}
		check(keyName, kv.Value)
	}
}

// finworldCensusResult is the whole census's answer for one plan.
type finworldCensusResult struct {
	Names        []finworldNameFinding
	Memos        []finworldMemoFinding
	LatePayables []finworldLatePayableFinding
	SourceMemos  []finworldSourceMemoFinding
}

// Clean answers whether every section came back empty.
func (r finworldCensusResult) Clean() bool {
	return len(r.Names) == 0 && len(r.Memos) == 0 && len(r.LatePayables) == 0 && len(r.SourceMemos) == 0
}

// finworldCensusPlan runs the plan census: names, memos and late payables, no
// database. The source scan runs beside it when a source root is at hand.
func finworldCensusPlan(plan *FinWorldPlanT, coreRoot string) (finworldCensusResult, error) {
	res := finworldCensusResult{
		Names:        finworldCensusNames(plan),
		Memos:        finworldCensusMemos(plan),
		LatePayables: finworldCensusLatePayables(plan),
	}
	if coreRoot != "" {
		src, err := finworldCensusSourceScan(coreRoot)
		if err != nil {
			return res, err
		}
		res.SourceMemos = src
	}
	return res, nil
}

// finworldFindCoreRoot walks up from dir until it finds the core tree (the
// directory that holds cmd/core/finworld_plan.go), or answers "" when none is
// above.
func finworldFindCoreRoot(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "core", "finworld_plan.go")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

package tablegate

// engine_test.go is the extraction engine of the cross-module table access
// gate (AGENTS.md's zero cross-module table access rule, checked). It turns
// Go source into SQL corpora and corpora into table reads and writes; the
// census (census_test.go) walks the real tree with it, the unit suite
// (unit_test.go) pins its edges on planted fixtures.
//
// HOW EXTRACTION WORKS, stated plainly:
//
//   - SQL is gathered from the positions where it reaches the database:
//     the call sites of the pgx surface (Query, QueryRow, Exec and
//     friends), the call sites of the module's SQL sinks, and composite
//     literal fields whose value tokenizes as a statement. A SQL sink is
//     any function, method or function literal whose STRING parameter
//     flows into a pgx call, or into a call of an already known sink (a
//     wrapper over a wrapper: execVia forwarding to exec resolves, one
//     level per fixpoint round, bounded at four; a cycle cannot grow the
//     set). Sinks match by BARE NAME within the module, so a same-named
//     call on any receiver is visited; at such a call site a raw argument
//     contributes only when it tokenizes as a statement (a leading
//     SELECT, WITH, INSERT, UPDATE or DELETE) and fragments arriving
//     through the string idiom chain pass as already vetted, so plain
//     English at a same-named call never enters a corpus. The failure
//     directions of that rule, stated: a statement split so its tail
//     argument lacks a leading verb loses the tail at a sink call site
//     (fail open), and a prose local passed through the idiom chain still
//     contributes (fail closed, as an unmapped word). Composite fields
//     (reporting's catalogEntry.sql, the sync skeleton's
//     readcache.Query.Page and ByID) are read from the literal itself,
//     because the runner executes them later; package consts resolve in
//     the field's expression beside the function's locals (pos's
//     parkedFrom join lives in a const). Names resolve through the string
//     idioms the tree actually uses: package level const and var
//     declarations of pure string expressions (backtick or interpreted
//     literals joined by +), local names assigned the same inside their
//     function, and package functions (or methods) whose body returns
//     pure string expressions, resolved with memoization and a cycle
//     guard (the query helper pattern: convertInsertSQL building the
//     conversion INSERT beside its caller).
//   - Nothing else is read as SQL. Prompt text, message templates and
//     error prose live in strings that never reach a pgx call, a sink
//     call site's statement-shaped argument, or a statement-shaped
//     composite field, so they are outside every corpus by construction;
//     a gate that vacuumed every literal would fail closed on the English
//     words from and the.
//   - A corpus is tokenized (SQL line and block comments and quoted value
//     literals are blanked so words inside them are never read as SQL),
//     split into statements at depth zero verbs, and each statement yields
//     the targets of FROM, JOIN, INSERT INTO, UPDATE, DELETE FROM and USING.
//
// SPELLINGS COVERED, taken from a read of the tree's real repositories
// (pricing, tax, order, billing, inventory, gl, identity, dispatch,
// reporting, finstmt, and the sync scope adapters beside them):
//
//   - SELECT ... FROM t; every JOIN spelling the tree uses (LEFT, RIGHT,
//     INNER, FULL, CROSS, with and without OUTER, plus JOIN LATERAL, where
//     the relation may also be a subquery or a set returning function);
//     INSERT INTO t; UPDATE t; DELETE FROM t. PostgreSQL's UPDATE t SET ...
//     FROM u and DELETE FROM t USING u are covered: FROM is a read wherever
//     it appears, so both spell their second relation as a read.
//   - INSERT INTO t (col, ...) SELECT: the statement keeps its verb across
//     the column list, so the SELECT's relations land in the same
//     statement (and under the same CTE names) as the INSERT's target.
//   - Plain and schema-qualified names: a.b (and a.b.c) resolve by their
//     table part.
//   - CTE names (WITH x AS (...), the comma chained , y AS (...), and the
//     data modifying WITH that prefixes INSERT, UPDATE or DELETE) are never
//     resolved as tables, scoped to their statement. The sync scope pattern
//     (a const holding WITH seq AS (...) concatenated, or fed through
//     Sprintf, at the call site with the statement body) keeps the WITH and
//     its name in one corpus, so the allocator name is never read as a
//     table.
//   - Function-call parens are not table lists: a captured name directly
//     followed by ( is dropped (FROM generate_series(1, 10), JOIN
//     unnest($1) u), and a FROM inside a call's argument list is skipped
//     when no SELECT runs in that paren group, which is how
//     EXTRACT(YEAR FROM created_at), SUBSTRING(x FROM 1 FOR 3) and
//     TRIM(BOTH ' ' FROM y) keep their column arguments out of the table
//     set while EXISTS(SELECT 1 FROM t) keeps t.
//   - INSERT INTO t (col, col) keeps t: after the INSERT INTO position a
//     following parenthesis is a column list, not a call. ON CONFLICT (...)
//     DO UPDATE SET is read as part of the same INSERT (the target is
//     already captured; the upsert arm names no new table).
//
// BLIND SPOTS, stated honestly rather than pretended away:
//
//   - Values interpolated by fmt.Sprintf are invisible when they carry a
//     table name. Today every Sprintf in the tree injects column lists,
//     WHERE fragments and scope values only; the table itself stays in the
//     literal. If a module ever Sprintfs a table name, this gate does not
//     see it. There is no query builder dependency in core/go.mod.
//   - SQL assembled through a strings.Builder or bytes.Buffer inside a
//     helper is invisible (b.String() is not a string expression). The
//     tree's builders build documents, letters and exports, not queries;
//     if one ever builds a query, the gate does not see it.
//   - Statement text that never appears as a source literal is invisible:
//     SQL assembled at run time from values the gate cannot trace (rows
//     read back, configuration strings, identifiers built from data) has
//     no literal for the engine to read. Statement positions reached
//     without a string literal, like CopyFrom's pgx.Identifier, are
//     likewise unseen. The tree uses none of these shapes for SQL today;
//     that is an audited fact of this revision, not a property of the
//     engine, and the day one lands the gate does not see it.
//   - The SQL sink rule keys on bare function and method names: two
//     same-named sinks in one module collapse to one. The argument
//     statement filter keeps prose out of the resulting corpora; what
//     remains of the keying is over-capture (visiting same-named calls
//     that carry no SQL), which fails closed.
//   - Two composite literal shapes are not read and do not occur in the
//     tree today: positional (unkeyed) struct literals (only named fields
//     are collected), and a statement field built inside a composite
//     literal nested in a function LITERAL assigned outside the function
//     whose consts resolve (the collected shapes are package level
//     declarations and function bodies). If either lands, the gate does
//     not see it.
//   - A closure sink is recognized only where the literal is assigned to
//     a local name in the same function that calls it (customer's scan
//     shape). A statement-carrying function literal stored in a struct
//     field or passed across functions is not recognized as a sink.
//   - Comma separated FROM lists (FROM a, b) capture only a. The tree uses
//     none today; this engine does not cover the spelling.
//   - Sequence values fetched through nextval('name') are string arguments,
//     not relation positions, so plain nextval use is outside this gate's
//     view; a sequence read as a relation (FROM some_seq) WOULD be captured
//     and would fail closed as unmapped, which is the fail closed posture.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// sqlCallMethods is the pgx surface whose call sites carry the SQL text as
// string arguments.
var sqlCallMethods = map[string]bool{
	"Query": true, "QueryRow": true, "Exec": true,
	"QueryContext": true, "QueryRowContext": true, "ExecContext": true,
	"QueryFunc": true, "ExecFunc": true, "CopyFrom": true,
	"Prepare": true, "PrepareStmt": true, "SendBatch": true,
}

// tableRef is one relation named by one statement: the table part after any
// schema qualification, and whether the statement writes it.
type tableRef struct {
	table string
	write bool
}

// sqlUnit is one extracted SQL corpus with its provenance, so a violation
// names the declaration it came from.
type sqlUnit struct {
	decl   string
	corpus string
}

// pkgIndex is the resolution index of one module: the package level const
// and var string values, the function return string parts, and the SQL
// sinks (functions and methods whose string parameter flows into a pgx
// call), merged across the module's directory tree. Merging is FIRST WINS
// per name: a name collision across subpackages (or two methods named
// alike on different types, since function parts and sinks are keyed by
// bare name) leaves the SECOND value unresolved, which NARROWS coverage.
// That is the honest weakness of the keying: a collision would make the
// second fragment's SQL invisible, a fail-open direction. No const
// collision across subpackages exists in the tree today, and no sink name
// collision was found across the 51 scanned modules; both facts are of
// this revision, and a reviewer re-checking them should re-run the dump.
type pkgIndex struct {
	consts map[string][]string
	funcs  map[string][]string
	sinks  map[string]bool
}

func newPkgIndex(consts, funcs map[string][]string) *pkgIndex {
	return &pkgIndex{consts: consts, funcs: funcs, sinks: map[string]bool{}}
}

// goFileUnits parses one non-test Go file and returns its SQL corpora: one
// per SQL-carrying call site and one per composite literal field whose
// value tokenizes as a statement. A call site carries SQL when the callee
// is the pgx surface, or a SQL sink of the module: any function, method or
// function literal whose string parameter flows into a pgx call (portal's
// r.exec and guardedAdminUpdate, quote's invoicesForTx, messaging's
// scanMessagePage, order's guardedChangeUpdate, customer's scan closure).
// Locals resolve per function, package names through the module index.
func goFileUnits(src []byte, filename string, pkg *pkgIndex) ([]sqlUnit, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var units []sqlUnit
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			resolve := localStringValues(d, pkg)
			localSinks := localSinkNames(d, pkg.sinks)
			name := declName(d)
			ast.Inspect(d.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel := callSelName(call)
				isPGX := sqlCallMethods[sel]
				if !isPGX && !pkg.sinks[sel] && !localSinks[sel] {
					return true
				}
				w := &corpusWalker{resolve: resolve, pkg: pkg, requireStatement: !isPGX}
				for _, arg := range call.Args {
					ast.Inspect(arg, func(child ast.Node) bool {
						w.collect(child)
						return true
					})
				}
				if c := strings.Join(w.parts, " "); strings.TrimSpace(c) != "" {
					units = append(units, sqlUnit{decl: name, corpus: c})
				}
				return true
			})
			// package consts resolve inside function bodies too: the sync
			// adapters build their readcache statements from shared consts
			// (pos's parkedColumns/parkedFrom), and the field's value is
			// only pure when those consts resolve beside the locals.
			units = append(units, compositeSQLUnits(d.Body, mergeInto(pkg.consts, resolve), pkg, name)...)
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				continue
			}
			units = append(units, compositeSQLUnits(d, pkg.consts, pkg, genDeclName(d))...)
		}
	}
	return units, nil
}

// packageSinks answers the names of the file's functions and methods whose
// string parameter flows into a pgx call, or into a call of an already
// known sink (a wrapper over a wrapper, resolved one level per fixpoint
// round by the caller): the body holds such a call with an argument (at
// any depth) that is one of the function's own parameters. Such a
// function is a SQL sink; the statements it runs arrive at its call
// sites, so the gate collects them there.
func packageSinks(file *ast.File, known map[string]bool) []string {
	var names []string
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Body == nil || d.Type.Params == nil {
			continue
		}
		params := stringParamNames(d.Type.Params)
		if len(params) == 0 {
			continue
		}
		if paramFlowsIntoPGX(d.Body, params, known) {
			names = append(names, d.Name.Name)
		}
	}
	return names
}

// localSinkNames answers the function's own local names assigned a function
// literal whose string parameter flows into a pgx call (customer's
// scan := func(sql string) closure). The closure's call sites sit in the
// same function, so the names resolve locally; the module's known sinks
// feed the flow check so a closure over a wrapper resolves too.
func localSinkNames(fn *ast.FuncDecl, sinks map[string]bool) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || (as.Tok != token.ASSIGN && as.Tok != token.DEFINE) ||
			len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		name, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		fl, ok := as.Rhs[0].(*ast.FuncLit)
		if !ok || fl.Type.Params == nil {
			return true
		}
		params := stringParamNames(fl.Type.Params)
		if len(params) > 0 && paramFlowsIntoPGX(fl.Body, params, sinks) {
			out[name.Name] = true
		}
		return true
	})
	return out
}

// stringParamNames answers the function's parameters whose type is string,
// the only parameters that can carry a statement. A context, a count or an
// options parameter flowing into a pgx call does not make a function a SQL
// sink.
func stringParamNames(fields *ast.FieldList) map[string]bool {
	out := map[string]bool{}
	if fields == nil {
		return out
	}
	for _, f := range fields.List {
		if id, ok := f.Type.(*ast.Ident); !ok || id.Name != "string" {
			continue
		}
		for _, n := range f.Names {
			out[n.Name] = true
		}
	}
	return out
}

// paramFlowsIntoPGX answers whether any pgx call (or call of an already
// known SQL sink, so a wrapper over a wrapper resolves, one level per
// fixpoint round under the caller's bound) in the body takes one of the
// named parameters as an argument, at any depth.
func paramFlowsIntoPGX(body ast.Node, params map[string]bool, sinks map[string]bool) bool {
	flows := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel := callSelName(call)
		if !sqlCallMethods[sel] && !sinks[sel] {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(c ast.Node) bool {
				if id, ok := c.(*ast.Ident); ok && params[id.Name] {
					flows = true
				}
				return true
			})
		}
		return true
	})
	return flows
}

// sqlStatementVerbs is the leading-token test for a composite literal field
// that carries a statement rather than a value.
var sqlStatementVerbs = map[string]bool{
	"SELECT": true, "WITH": true, "INSERT": true, "UPDATE": true, "DELETE": true,
}

// leadingSQLVerb answers whether the text's first identifier token is a
// statement verb, the shape of SQL parked in a struct field
// (catalogEntry{sql: ...}, readcache.Query{Page: ..., ByID: ...}).
func leadingSQLVerb(text string) bool {
	for _, tok := range tokenizeSQL(text) {
		if tok.kind == identKind {
			return sqlStatementVerbs[tok.upper]
		}
		if tok.kind == punctKind {
			return false
		}
	}
	return false
}

// compositeSQLUnits gathers one corpus per composite literal field whose
// pure string value tokenizes as a statement. The runner executes these
// later (the sync readcache skeleton runs Query.Page and Query.ByID; the
// reporting runner runs catalogEntry.sql), so the literal is the SQL's
// only appearance in source.
func compositeSQLUnits(node ast.Node, resolve map[string][]string, pkg *pkgIndex, decl string) []sqlUnit {
	r := &partsResolver{consts: resolve, funcs: pkg.funcs}
	var units []sqlUnit
	ast.Inspect(node, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			parts := r.parts(kv.Value, 0)
			if parts == nil {
				continue
			}
			text := strings.Join(parts, " ")
			if !leadingSQLVerb(text) {
				continue
			}
			units = append(units, sqlUnit{decl: decl + " field " + key.Name, corpus: text})
		}
		return true
	})
	return units
}

func declName(d *ast.FuncDecl) string {
	if d.Recv != nil && len(d.Recv.List) > 0 {
		if recv, ok := typeName(d.Recv.List[0].Type); ok {
			return recv + "." + d.Name.Name
		}
	}
	return d.Name.Name
}

func genDeclName(d *ast.GenDecl) string {
	kind := "var"
	if d.Tok == token.CONST {
		kind = "const"
	}
	// name the declaration the composite sits in, so a fact names its site
	for _, spec := range d.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
			return kind + " " + vs.Names[0].Name
		}
	}
	return kind
}

func typeName(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name, true
	}
	return "", false
}

func callSelName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}

// stringParts collects the string literal parts of a pure string expression
// (literals joined by +, parentheses, resolvable const idents and resolvable
// function calls), and nil when anything else appears.
type partsResolver struct {
	consts map[string][]string
	funcs  map[string][]string
}

func (r *partsResolver) parts(expr ast.Expr, depth int) []string {
	if depth > 24 {
		return nil
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return nil
		}
		s, err := strconv.Unquote(e.Value)
		if err != nil {
			return nil
		}
		return []string{s}
	case *ast.ParenExpr:
		return r.parts(e.X, depth+1)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return nil
		}
		left := r.parts(e.X, depth+1)
		right := r.parts(e.Y, depth+1)
		// an operand that resolves to nothing is a runtime string (a where
		// clause, a limit fragment): it carries no extractable literal, so
		// it contributes nothing but must not poison the literal operands
		// beside it (const query + where + const order must keep both
		// consts).
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		return append(left, right...)
	case *ast.Ident:
		if parts, ok := r.consts[e.Name]; ok {
			return parts
		}
		return nil
	case *ast.CallExpr:
		if parts, ok := r.funcs[callSelName(e)]; ok {
			return parts
		}
		// the two builder calls the tree formats queries with (fmt.Sprintf
		// for the statement, strings.Join for the column lists fed to it):
		// their format string and literal arguments are the SQL text. Every
		// other unknown call (fmt.Errorf and friends) stays prose, never
		// SQL. Non string arguments are values and are skipped.
		if !isQueryBuilderCall(e) {
			return nil
		}
		var parts []string
		for _, arg := range e.Args {
			if p := r.parts(arg, depth+1); p != nil {
				parts = append(parts, p...)
			}
		}
		if len(parts) > 0 {
			return parts
		}
		return nil
	}
	return nil
}

// isQueryBuilderCall answers whether the call is one of the string builders
// query text is assembled with in this tree: fmt.Sprintf and strings.Join.
func isQueryBuilderCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return (pkg.Name == "fmt" && sel.Sel.Name == "Sprintf") ||
		(pkg.Name == "strings" && sel.Sel.Name == "Join")
}

// packageStringValues records one file's top level const and var names whose
// values are pure string expressions. Values already recorded resolve inside
// later ones, and the rounds let a const built from a const declared beside
// it resolve regardless of order.
func packageStringValues(file *ast.File, funcs map[string][]string) map[string][]string {
	out := make(map[string][]string)
	r := &partsResolver{consts: out, funcs: funcs}
	var specs []*ast.ValueSpec
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok == token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				specs = append(specs, vs)
			}
		}
	}
	for round := 0; round < 4; round++ {
		changed := false
		for _, vs := range specs {
			if len(vs.Names) != 1 || len(vs.Values) == 0 {
				continue
			}
			if _, done := out[vs.Names[0].Name]; done {
				continue
			}
			var parts []string
			pure := true
			for _, val := range vs.Values {
				p := r.parts(val, 0)
				if p == nil {
					pure = false
					break
				}
				parts = append(parts, p...)
			}
			if pure && len(parts) > 0 {
				out[vs.Names[0].Name] = parts
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return out
}

// packageFuncParts records, for every function and method declared in the
// file, the string parts its return statements answer when every return is
// a pure string expression. Memoized across the package by the caller
// building the index; a function whose returns are not all pure string
// expressions (builders, prose formatters) records nothing, so its text
// never enters a corpus.
func packageFuncParts(file *ast.File, idx *pkgIndex, building map[string]bool) map[string][]string {
	out := make(map[string][]string)
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Body == nil || building[d.Name.Name] {
			continue
		}
		if _, done := idx.funcs[d.Name.Name]; done {
			continue
		}
		building[d.Name.Name] = true
		r := &partsResolver{consts: idx.consts, funcs: idx.funcs}
		var parts []string
		pure := true
		ast.Inspect(d.Body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, res := range ret.Results {
				p := r.parts(res, 0)
				if p == nil {
					pure = false
					return false
				}
				parts = append(parts, p...)
			}
			return true
		})
		delete(building, d.Name.Name)
		if pure && len(parts) > 0 {
			idx.funcs[d.Name.Name] = parts
			out[d.Name.Name] = parts
		}
	}
	return out
}

// localStringValues records the function's own local names assigned pure
// string expressions (const x = ..., var x = ..., x := ...), resolving
// through the module index.
func localStringValues(fn *ast.FuncDecl, pkg *pkgIndex) map[string][]string {
	out := make(map[string][]string)
	r := &partsResolver{consts: mergeInto(pkg.consts, out), funcs: pkg.funcs}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GenDecl:
			if node.Tok != token.CONST && node.Tok != token.VAR {
				return true
			}
			for _, spec := range node.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Values) == 0 || len(vs.Names) != 1 {
					continue
				}
				if _, done := out[vs.Names[0].Name]; done {
					continue
				}
				var parts []string
				pure := true
				for _, val := range vs.Values {
					p := r.parts(val, 0)
					if p == nil {
						pure = false
						break
					}
					parts = append(parts, p...)
				}
				if pure && len(parts) > 0 {
					out[vs.Names[0].Name] = parts
				}
			}
		case *ast.AssignStmt:
			if node.Tok != token.ASSIGN && node.Tok != token.DEFINE {
				return true
			}
			if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			name, ok := node.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			if parts := r.parts(node.Rhs[0], 0); parts != nil {
				out[name.Name] = parts
			}
		}
		return true
	})
	return out
}

func mergeInto(base, extra map[string][]string) map[string][]string {
	out := make(map[string][]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// corpusWalker collects string literal parts at a node, resolving const,
// local and function-return idents. requireStatement marks sink call site
// collection: there, a raw string literal contributes only when it
// tokenizes as a statement (a leading SELECT, WITH, INSERT, UPDATE or
// DELETE), so prose arguments at a same-named call never enter a corpus;
// fragments that arrive through the string idiom chain (consts, locals,
// known helpers) are already vetted and pass as they are.
type corpusWalker struct {
	resolve          map[string][]string
	pkg              *pkgIndex
	parts            []string
	requireStatement bool
}

func (w *corpusWalker) collect(n ast.Node) {
	switch node := n.(type) {
	case *ast.BasicLit:
		if node.Kind == token.STRING {
			if s, err := strconv.Unquote(node.Value); err == nil {
				if w.requireStatement && !leadingSQLVerb(s) {
					return
				}
				w.parts = append(w.parts, s)
			}
		}
	case *ast.Ident:
		if parts, ok := w.resolve[node.Name]; ok {
			w.parts = append(w.parts, parts...)
			return
		}
		if parts, ok := w.pkg.consts[node.Name]; ok {
			w.parts = append(w.parts, parts...)
		}
	case *ast.CallExpr:
		if parts, ok := w.pkg.funcs[callSelName(node)]; ok {
			w.parts = append(w.parts, parts...)
		}
	}
}

// extractTables runs the SQL side over one corpus and returns the table
// references it names, in first appearance order, deduplicated.
func extractTables(corpus string) []tableRef {
	var out []tableRef
	seen := map[tableRef]bool{}
	for _, stmt := range sqlStatements(tokenizeSQL(corpus)) {
		for _, ref := range statementTables(stmt) {
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	return out
}

// sqlToken is one token of the SQL side: an identifier (or keyword), a
// blanked value literal, a number, or single character punctuation.
type sqlToken struct {
	kind  int
	text  string
	upper string
}

const (
	identKind = iota
	stringKind
	numberKind
	punctKind
)

func isIdentRune(r byte) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isIdentStart(r byte) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// tokenizeSQL turns a corpus into tokens. SQL comments (line and block) and
// quoted value literals are blanked: words inside them are never read as
// SQL. Double quoted SQL identifiers keep their text.
func tokenizeSQL(corpus string) []sqlToken {
	var toks []sqlToken
	i := 0
	n := len(corpus)
	for i < n {
		r := corpus[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			i++
		case r == '-' && i+1 < n && corpus[i+1] == '-':
			for i < n && corpus[i] != '\n' {
				i++
			}
		case r == '/' && i+1 < n && corpus[i+1] == '*':
			i += 2
			for i+1 < n && !(corpus[i] == '*' && corpus[i+1] == '/') {
				i++
			}
			i = endOf(i+2, n)
		case r == '\'':
			i++
			for i < n {
				if corpus[i] == '\'' {
					if i+1 < n && corpus[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			toks = append(toks, sqlToken{kind: stringKind})
		case r == '"':
			i++
			start := i
			for i < n && corpus[i] != '"' {
				i++
			}
			text := corpus[start:endOf(i, n)]
			i = endOf(i+1, n)
			toks = append(toks, sqlToken{kind: identKind, text: text, upper: strings.ToUpper(text)})
		case isIdentStart(r):
			start := i
			for i < n && isIdentRune(corpus[i]) {
				i++
			}
			text := corpus[start:i]
			toks = append(toks, sqlToken{kind: identKind, text: text, upper: strings.ToUpper(text)})
		case r >= '0' && r <= '9':
			for i < n && (corpus[i] == '.' || (corpus[i] >= '0' && corpus[i] <= '9')) {
				i++
			}
			toks = append(toks, sqlToken{kind: numberKind})
		default:
			toks = append(toks, sqlToken{kind: punctKind, text: string(r)})
			i++
		}
	}
	return toks
}

func endOf(i, n int) int {
	if i > n {
		return n
	}
	return i
}

// sqlStatements splits a token stream into statements. A WITH at depth zero
// always opens a statement. A SELECT, INSERT, UPDATE or DELETE at depth zero
// is the statement's verb when it does not have one yet (a data modifying
// WITH prefixing its INSERT, UPDATE or DELETE; an INSERT ... SELECT keeps
// the INSERT as its verb across the column list). A verb on a statement
// whose verb is already a SELECT closes it and opens the next, except after
// UNION or ALL, where the arm belongs to the same statement; a verb on a
// statement whose verb is INSERT, UPDATE or DELETE closes it too, except
// the UPDATE of an upsert arm (DO UPDATE SET), which stays in its INSERT.
func sqlStatements(toks []sqlToken) [][]sqlToken {
	var statements [][]sqlToken
	var cur []sqlToken
	depth := 0
	haveVerb := false
	verb := ""
	flush := func() {
		if len(cur) > 0 {
			statements = append(statements, cur)
		}
		cur = nil
		haveVerb = false
		verb = ""
	}
	prevIdent := ""
	for _, tok := range toks {
		if tok.kind == punctKind {
			if tok.text == "(" {
				depth++
			} else if tok.text == ")" {
				depth--
				if depth < 0 {
					depth = 0
				}
			} else if tok.text == ";" && depth == 0 {
				flush()
				continue
			}
		}
		if depth == 0 && tok.kind == identKind {
			switch tok.upper {
			case "WITH":
				flush()
			case "SELECT", "INSERT", "UPDATE", "DELETE":
				isUpsertArm := tok.upper == "UPDATE" && prevIdent == "DO"
				isRowLock := tok.upper == "UPDATE" && prevIdent == "FOR"
				isUnionArm := tok.upper == "SELECT" && (prevIdent == "UNION" || prevIdent == "ALL")
				switch {
				case isUpsertArm, isRowLock:
					// stays in its statement (the upsert arm, the row lock)
				case !haveVerb:
					haveVerb = true
					verb = tok.upper
				case isUnionArm:
					// stays in its SELECT
				case verb == "SELECT" && tok.upper == "SELECT":
					flush()
					haveVerb = true
					verb = tok.upper
				case verb == "INSERT" && tok.upper == "SELECT":
					// INSERT INTO t (cols) SELECT: one statement
				default:
					flush()
					haveVerb = true
					verb = tok.upper
				}
			}
		}
		if tok.kind == identKind {
			prevIdent = tok.upper
		} else if tok.kind != punctKind || tok.text != "(" {
			prevIdent = ""
		}
		cur = append(cur, tok)
	}
	flush()
	return statements
}

// parenGroup records one open parenthesis: whether an identifier directly
// preceded it (a call's argument list) and whether a SELECT has been seen
// inside it so far.
type parenGroup struct {
	call      bool
	sawSelect bool
}

// statementTables extracts the table references of one statement's tokens:
// first the CTE names defined in it, then the relation positions.
func statementTables(toks []sqlToken) []tableRef {
	cteNames := cteDefinitions(toks)
	var refs []tableRef
	var groups []parenGroup
	depth := 0
	prevKind := 0
	prevIdent := ""      // upper of the identifier token before this one
	nextIsWrite := false // DELETE met: its FROM names the write target
	intoWrite := false   // INSERT met: its INTO names the write target
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if tok.kind == punctKind {
			switch tok.text {
			case "(":
				groups = append(groups, parenGroup{call: prevKind == identKind})
				depth++
			case ")":
				if len(groups) > 0 {
					groups = groups[:len(groups)-1]
				}
				depth--
				if depth < 0 {
					depth = 0
				}
			}
			prevKind = punctKind
			prevIdent = ""
			continue
		}
		if tok.kind == stringKind || tok.kind == numberKind {
			prevKind = tok.kind
			prevIdent = ""
			continue
		}
		// identKind
		if tok.upper == "SELECT" {
			for j := range groups {
				groups[j].sawSelect = true
			}
		}
		switch tok.upper {
		case "DELETE":
			nextIsWrite = true
		case "INSERT":
			intoWrite = true
		case "FROM":
			// a FROM inside a call's argument list with no SELECT of its own
			// is a function argument (EXTRACT, SUBSTRING, TRIM), not a table
			// source.
			if depth > 0 && len(groups) > 0 {
				g := groups[len(groups)-1]
				if g.call && !g.sawSelect {
					nextIsWrite = false
					prevKind = identKind
					prevIdent = tok.upper
					continue
				}
			}
			// IS [NOT] DISTINCT FROM is the set-comparison operator, not a
			// relation source.
			if prevIdent == "DISTINCT" {
				nextIsWrite = false
				prevKind = identKind
				prevIdent = tok.upper
				continue
			}
			name, next := captureName(toks, i+1, false)
			if name != "" && !cteNames[name] {
				refs = append(refs, tableRef{table: name, write: nextIsWrite})
			}
			i = next
			nextIsWrite = false
		case "JOIN":
			name, next := captureName(toks, i+1, false)
			if name != "" && !cteNames[name] {
				refs = append(refs, tableRef{table: name})
			}
			i = next
		case "INTO":
			if intoWrite {
				name, next := captureName(toks, i+1, true)
				if name != "" && !cteNames[name] {
					refs = append(refs, tableRef{table: name, write: true})
				}
				i = next
				intoWrite = false
			}
		case "UPDATE":
			// FOR UPDATE is the row lock clause, not a statement verb; and
			// the upsert arm (ON CONFLICT ... DO UPDATE SET) names no table.
			if prevIdent != "FOR" {
				name, next := captureName(toks, i+1, false)
				if name != "" && !cteNames[name] {
					refs = append(refs, tableRef{table: name, write: true})
				}
				i = next
			}
		case "USING":
			name, next := captureName(toks, i+1, false)
			if name != "" && !cteNames[name] {
				refs = append(refs, tableRef{table: name})
			}
			i = next
		}
		prevKind = identKind
		prevIdent = tok.upper
	}
	return refs
}

// cteDefinitions collects the names defined by WITH in one statement: the
// name after WITH and the names after commas that chain further CTEs, each
// followed by AS ( or by an optional column list then AS ( (the
// WITH x (a, b) AS spelling), with the MATERIALIZED and NOT MATERIALIZED
// variants too).
func cteDefinitions(toks []sqlToken) map[string]bool {
	names := map[string]bool{}
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if tok.kind != identKind || i == 0 || i+2 >= len(toks) {
			continue
		}
		prev := toks[i-1]
		prevOK := (prev.kind == identKind && prev.upper == "WITH") ||
			(prev.kind == punctKind && prev.text == ",")
		if !prevOK {
			continue
		}
		j := i + 1
		if toks[j].kind == punctKind && toks[j].text == "(" {
			// the column list of the WITH x (a, b) AS spelling: skip to the
			// matching close
			depth := 0
			for ; j < len(toks); j++ {
				if toks[j].kind == punctKind {
					if toks[j].text == "(" {
						depth++
					} else if toks[j].text == ")" {
						depth--
						if depth == 0 {
							j++
							break
						}
					}
				}
			}
		}
		if j+1 >= len(toks) || toks[j].kind != identKind || toks[j].upper != "AS" {
			continue
		}
		j++
		if toks[j].kind == identKind && (toks[j].upper == "MATERIALIZED" || toks[j].upper == "NOT") {
			j++
			if j < len(toks) && toks[j].kind == identKind && toks[j].upper == "MATERIALIZED" {
				j++
			}
		}
		if j < len(toks) && toks[j].kind == punctKind && toks[j].text == "(" {
			names[strings.ToLower(tok.text)] = true
		}
	}
	return names
}

// captureName reads the relation name at position i: the next identifier
// token, skipping the LATERAL and ONLY modifiers. It returns the table part
// after schema qualification, lower cased, and the position to resume from.
// It answers empty when the position holds no relation: a paren, a keyword,
// or a function call (a name directly followed by an open parenthesis),
// unless keepParens marks the INSERT INTO position, where the parenthesis is
// a column list, not a call.
func captureName(toks []sqlToken, i int, keepParens bool) (string, int) {
	for i < len(toks) {
		tok := toks[i]
		if tok.kind == identKind && (tok.upper == "LATERAL" || tok.upper == "ONLY") {
			i++
			continue
		}
		break
	}
	if i >= len(toks) {
		return "", i
	}
	tok := toks[i]
	if tok.kind != identKind || sqlNoise[tok.upper] {
		return "", i
	}
	if i+1 < len(toks) && toks[i+1].kind == punctKind && toks[i+1].text == "(" && !keepParens {
		// a name followed by an open parenthesis is a function call, not a
		// table (FROM generate_series(1, 10), JOIN unnest($1) u)
		return "", i
	}
	// schema qualification: a.b (and a.b.c) tokenize as separate tokens, so
	// walk the dots and answer the table part, the last segment. The
	// PostgreSQL system namespaces are their own objects (the catalog, not
	// one of its views): information_schema.tables resolves to
	// information_schema, which the ownership map carries as the system
	// owner.
	first := strings.ToLower(tok.text)
	if first == "information_schema" || first == "pg_catalog" {
		return first, i
	}
	last := tok
	j := i
	for j+2 < len(toks) &&
		toks[j+1].kind == punctKind && toks[j+1].text == "." &&
		toks[j+2].kind == identKind {
		j += 2
		last = toks[j]
	}
	return strings.ToLower(last.text), i
}

// sqlNoise is the keyword stop list a captured name must clear.
var sqlNoise = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "AND": true, "OR": true,
	"NOT": true, "IN": true, "EXISTS": true, "VALUES": true, "ON": true,
	"AS": true, "BY": true, "WHEN": true, "THEN": true, "ELSE": true,
	"CASE": true, "END": true, "UNION": true, "ALL": true, "DISTINCT": true,
	"LIMIT": true, "OFFSET": true, "HAVING": true, "GROUP": true,
	"ORDER": true, "RETURNING": true, "SET": true, "INTO": true,
	"DEFAULT": true, "CONFLICT": true, "DO": true, "NOTHING": true,
	"LATERAL": true, "ONLY": true, "NULL": true, "IS": true, "NULLS": true,
	"FIRST": true, "LAST": true, "TRUE": true, "FALSE": true, "ANY": true,
	"SOME": true, "BETWEEN": true, "LIKE": true, "ILIKE": true, "FOR": true,
	"LOCK": true, "SHARE": true, "NOWAIT": true, "SKIP": true, "OF": true,
	"CURRENT": true, "RECURSIVE": true, "MATERIALIZED": true, "CASCADE": true,
	"RESTRICT": true, "ACTION": true, "KEY": true, "REFERENCES": true,
}

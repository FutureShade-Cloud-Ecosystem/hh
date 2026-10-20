package assist

// citations.go, the server side of citations and the figure check (plan r3 sections 4 and 6.5, PANEL-API 8.2).
//
// A row that data.query returns can be cited: the service numbers the rows of the exchange's results, the
// model sees them as "[n]", and the model writes [n] after a figure it takes from row n. The server keeps a
// citation only if its number is in this exchange's own results, and builds the label and the ref itself
// from those results, so nothing a model writes can name a record the exchange did not read.
//
// The check that decides whether a reply stands: every [n] must exist; every number in the text must be a
// number the results contain (so a figure cannot be invented or computed from nothing); and when the
// results carry citable rows, a reply that states a figure must cite at least one. A reply that fails goes
// through slice 1's escalation: one retry with OUR text, the next alias, then the results tables with no
// model prose.

import (
	"context"
	"regexp"
	"strings"
)

var (
	reMarker = regexp.MustCompile(`\[(\d{1,4})\]`)
	reNumber = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)
)

// absorb takes what a successful command result contributes to the exchange: numbered citations for its
// rows, and the first notice and scope.
func (r *askRun) absorb(command string, out *Result) {
	if command != DataQueryCode {
		return
	}
	r.queried++
	for i := range out.Items {
		if out.Items[i].Ref == nil {
			continue
		}
		r.citeN++
		out.Items[i].Cite = r.citeN
		r.cites = append(r.cites, Citation{N: r.citeN, Label: out.Items[i].Ref.Label, Ref: *out.Items[i].Ref})
	}
	if r.notice == nil && out.Notice != nil {
		n := *out.Notice
		r.notice = &n
	}
	if r.scope == nil && out.Scope != nil {
		s := *out.Scope
		r.scope = &s
	}
}

// numbersIn returns the numeric tokens of s with thousands separators removed.
func numbersIn(s string) []string {
	var out []string
	for _, m := range reNumber.FindAllString(s, -1) {
		m = strings.ReplaceAll(m, ",", "")
		m = strings.TrimRight(m, ".")
		out = append(out, m)
	}
	return out
}

// groundedNumbers is every number the exchange's data.query results contain, plus the row counts and the
// citation numbers themselves.
func groundedNumbers(run *askRun) map[string]bool {
	set := map[string]bool{}
	for _, res := range run.results {
		if res.Command != DataQueryCode {
			continue
		}
		set[itoa(len(res.Items))] = true
		for _, n := range numbersIn(renderResult(res)) {
			set[n] = true
			// 1,234.50 is also 1234.5: a model may drop a trailing zero.
			if strings.Contains(n, ".") {
				set[strings.TrimRight(strings.TrimRight(n, "0"), ".")] = true
			}
		}
	}
	for _, c := range run.cites {
		set[itoa(c.N)] = true
	}
	return set
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}

// checkAnswerFigures reports whether a reply to an exchange that ran data.query may stand.
func checkAnswerFigures(run *askRun, text string) bool {
	markers := reMarker.FindAllStringSubmatch(text, -1)
	valid := map[string]bool{}
	for _, c := range run.cites {
		valid[itoa(c.N)] = true
	}
	for _, m := range markers {
		if !valid[m[1]] {
			return false
		}
	}
	body := reMarker.ReplaceAllString(text, "")
	figures := numbersIn(body)
	if len(figures) == 0 {
		return true
	}
	grounded := groundedNumbers(run)
	for _, f := range figures {
		if !grounded[f] {
			return false
		}
	}
	if len(run.cites) > 0 && len(markers) == 0 {
		return false
	}
	return true
}

// citationsIn keeps, in order and once each, the citations the text actually uses.
func citationsIn(run *askRun, text string) []Citation {
	byN := map[string]Citation{}
	for _, c := range run.cites {
		byN[itoa(c.N)] = c
	}
	var out []Citation
	seen := map[string]bool{}
	for _, m := range reMarker.FindAllStringSubmatch(text, -1) {
		if c, ok := byN[m[1]]; ok && !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, c)
		}
	}
	return out
}

// WithQueryLog attaches the writer of the per exchange summary row of the query log (the tokens an
// exchange that ran data.query cost). The per query rows are written by the querier.
func (s *Service) WithQueryLog(l QueryLog) *Service { s.qlog = l; return s }

// flushExchangeLog writes the one summary row of an exchange that ran data.query, with its tokens.
func (s *Service) flushExchangeLog(ctx context.Context, c Caller, run *askRun) {
	if s.qlog == nil || run == nil || run.queried == 0 {
		return
	}
	err := s.qlog.AppendQueryLog(context.WithoutCancel(ctx), QueryLogEntry{
		ExchangeID: exchangeIDFrom(ctx), AskerID: c.UserID, Audience: "staff", Tool: "exchange",
		RowCount: len(run.cites), TokensIn: run.in, TokensOut: run.out,
	})
	if err != nil && s.log != nil {
		s.log.Warn("assist: the exchange summary row of the query log was not written")
	}
}

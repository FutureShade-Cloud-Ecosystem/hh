package assist

// export.go, slice 2 sub item 6: the billing export. A job, not a route.
//
// FutureBuild supplies the model and the key and charges the dealer for inference, so the dealer's
// month has to come out of the ledger in a shape an invoice can be made from: per user and role,
// per audience, per alias and model, with requests, tokens and cost, and one dealer summary line
// carrying the org slug. There is no HTTP route and no Dealer Admin surface for it (the operator
// guard does not exist yet, review r2 P2-4); the `core assist-billing-export` command is run by the
// operator on the box and writes to its standard output.
//
// SETTLEMENTS ONLY. A reservation is an estimate, a void is nothing, and neither is billed. The
// export therefore equals the settlement rows of the spend table summed by group, which is what its
// test proves. Cost is the ledger's own: provider price times usage, with no markup. FutureBuild's
// markup is applied to this output, not stored beside it.

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"sort"
	"strconv"
)

// ExportLine is one group of settled calls.
type ExportLine struct {
	Audience       Audience `json:"audience"`
	UserID         string   `json:"user_id"`
	RoleCode       string   `json:"role_code"`
	Alias          string   `json:"alias"`
	Model          string   `json:"model"`
	Requests       int      `json:"requests"`
	TokensIn       int64    `json:"tokens_in"`
	TokensOut      int64    `json:"tokens_out"`
	TokensCached   int64    `json:"tokens_cached"`
	CostMicroCents int64    `json:"cost_micro_cents"`
}

// ExportSummary is the dealer's month in one line.
type ExportSummary struct {
	Org            string `json:"org"`
	Month          string `json:"month"`
	Requests       int    `json:"requests"`
	TokensIn       int64  `json:"tokens_in"`
	TokensOut      int64  `json:"tokens_out"`
	TokensCached   int64  `json:"tokens_cached"`
	CostMicroCents int64  `json:"cost_micro_cents"`
	// SweptRequests and SweptCostMicroCents are the part of the totals that the sweep settled at an
	// estimate (the call may not have been billed by the provider); the operator reviews them.
	SweptRequests       int   `json:"swept_requests"`
	SweptCostMicroCents int64 `json:"swept_cost_micro_cents"`
}

// BillingExport is one month.
type BillingExport struct {
	Summary ExportSummary `json:"summary"`
	Lines   []ExportLine  `json:"lines"`
}

// BuildBillingExport groups settlement rows. Rows of any other kind are ignored, so a caller that
// hands over the whole month still gets the billable part. Order is stable: audience, user, role,
// alias, model.
func BuildBillingExport(org, month string, rows []SpendRow) BillingExport {
	type key struct {
		aud                   Audience
		user, role, alias, mo string
	}
	groups := map[key]*ExportLine{}
	var order []key
	sum := ExportSummary{Org: org, Month: month}
	for _, r := range rows {
		if r.Kind != SpendSettlement {
			continue
		}
		user := r.StaffUserID
		if user == "" {
			user = r.PortalUserID
		}
		k := key{r.Audience, user, r.RoleCode, r.Alias, r.Model}
		g, ok := groups[k]
		if !ok {
			g = &ExportLine{Audience: r.Audience, UserID: user, RoleCode: r.RoleCode, Alias: r.Alias, Model: r.Model}
			groups[k] = g
			order = append(order, k)
		}
		g.Requests++
		g.TokensIn += int64(r.TokensIn)
		g.TokensOut += int64(r.TokensOut)
		g.TokensCached += int64(r.TokensCached)
		g.CostMicroCents += r.CostMicroCents
		sum.Requests++
		if r.SettledBy == SettledBySweep {
			sum.SweptRequests++
			sum.SweptCostMicroCents += r.CostMicroCents
		}
		sum.TokensIn += int64(r.TokensIn)
		sum.TokensOut += int64(r.TokensOut)
		sum.TokensCached += int64(r.TokensCached)
		sum.CostMicroCents += r.CostMicroCents
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		switch {
		case a.aud != b.aud:
			return a.aud < b.aud
		case a.user != b.user:
			return a.user < b.user
		case a.role != b.role:
			return a.role < b.role
		case a.alias != b.alias:
			return a.alias < b.alias
		}
		return a.mo < b.mo
	})
	out := BillingExport{Summary: sum, Lines: []ExportLine{}}
	for _, k := range order {
		out.Lines = append(out.Lines, *groups[k])
	}
	return out
}

// WriteJSON writes the export as one indented JSON document.
func (e BillingExport) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}

// CSVHeader is the column order of WriteCSV. The dealer summary is the last row, with row_type
// "summary" and the org slug in its org column; line rows carry an empty org.
var CSVHeader = []string{"row_type", "org", "month", "audience", "user_id", "role_code", "alias", "model",
	"requests", "tokens_in", "tokens_out", "tokens_cached", "cost_micro_cents"}

// WriteCSV writes the lines and then the dealer summary row.
func (e BillingExport) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(CSVHeader); err != nil {
		return err
	}
	n := strconv.FormatInt
	for _, l := range e.Lines {
		if err := cw.Write([]string{"line", "", e.Summary.Month, string(l.Audience), l.UserID, l.RoleCode, l.Alias, l.Model,
			strconv.Itoa(l.Requests), n(l.TokensIn, 10), n(l.TokensOut, 10), n(l.TokensCached, 10), n(l.CostMicroCents, 10)}); err != nil {
			return err
		}
	}
	s := e.Summary
	if err := cw.Write([]string{"summary", s.Org, s.Month, "", "", "", "", "",
		strconv.Itoa(s.Requests), n(s.TokensIn, 10), n(s.TokensOut, 10), n(s.TokensCached, 10), n(s.CostMicroCents, 10)}); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

package assist

// commands_data.go, data.query as a registry command. It goes through Registry.Run like the other
// commands: the tier floor (managers and above), administrator narrowing, existence before authority, and
// the caller's own Caller all apply unchanged. What it adds is the catalogue the model is shown, built per
// asker, and a spec argument the compiler (dataquery_spec.go) reads.

import (
	"context"
	"fmt"
	"strings"
)

// DataCommands is the data tool, appended after the typed commands.
func DataCommands() []Command {
	return []Command{{
		Code:    DataQueryCode,
		Title:   "Ask the data",
		Summary: "Query the orders data with a structured spec.",
		MaxTier: 2,
		Args: []ArgSpec{{
			Name:     "spec",
			Label:    "Query spec (one JSON object)",
			Example:  `{"view":"ai.orders","select":["order_no","customer_name","total_cents"],"where":[{"col":"status","op":"in","value":["CONFIRMED"]}],"limit":10}`,
			Required: true,
		}},
		describe: describeDataQuery,
		run: func(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
			if deps.Query == nil {
				return Result{}, errUnwired("data.query")
			}
			return deps.Query.Query(ctx, c, a.get("spec"))
		},
	}}
}

// describeDataQuery is the tool description for THIS asker: the closed grammar and the catalogue they
// may name. A view or column that is not listed here is absent for them and refused as unknown.
func describeDataQuery(c Caller) string {
	var b strings.Builder
	b.WriteString("Answer questions about orders by sending ONE JSON object in the spec argument. You never write SQL. ")
	b.WriteString("Shape: {\"view\":..., \"select\":[columns], \"where\":[{\"col\":..., \"op\":..., \"value\":...}], ")
	b.WriteString("\"group_by\":[columns], \"aggregate\":[{\"fn\":\"count|sum|avg|min|max\", \"col\":...}], ")
	b.WriteString("\"order_by\":[{\"col\":..., \"dir\":\"asc|desc\"}], \"limit\":n}. ")
	b.WriteString("Operators: =, !=, <, <=, >, >=, in (list), between ([low, high]), is_null. ")
	b.WriteString("Date values may be YYYY-MM-DD or one of: today, yesterday, tomorrow, start_of_week, start_of_last_week, start_of_month, start_of_last_month, start_of_year, N_days_ago. ")
	b.WriteString("Money columns are integer cents. An aggregate result column is named fn_col, for example sum_total_cents, and count for count(*). ")
	b.WriteString("Rows you get back are numbered [n]: cite a row's number in brackets after every figure you state from it; a figure with no citation is rejected.\n")
	for _, v := range CatalogueFor(c) {
		fmt.Fprintf(&b, "View %s: %s Columns:", v.Name, v.About)
		for _, col := range v.Columns {
			if col.Hidden {
				continue
			}
			fmt.Fprintf(&b, " %s (%s)", col.Name, typeWord(col.Type))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func typeWord(t colType) string {
	switch t {
	case typeInt:
		return "number"
	case typeCents:
		return "cents"
	case typeDate:
		return "date"
	case typeTime:
		return "time"
	case typeNumeric:
		return "quantity"
	case typeUUID:
		return "id"
	}
	return "text"
}

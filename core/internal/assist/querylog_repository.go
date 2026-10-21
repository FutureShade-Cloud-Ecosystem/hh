package assist

// querylog_repository.go, the writer of assist_query_log (migration 0419). INSERT only: the table's
// triggers refuse anything else and this file has no other statement.

import (
	"context"
	"encoding/json"
	"errors"
)

// AppendQueryLog writes one row. It writes the outline of a query, never a result row and never an
// error text; an error returned here names nothing of the row.
func (r *Repository) AppendQueryLog(ctx context.Context, e QueryLogEntry) error {
	if r == nil || r.pool == nil {
		return errors.New("assist: no database for the query log")
	}
	spec := e.Spec
	if len(spec) == 0 {
		spec = json.RawMessage(`{}`)
	}
	cited := e.CitedIDs
	if cited == nil {
		cited = []string{}
	}
	citedJSON, err := json.Marshal(cited)
	if err != nil {
		return errors.New("assist: query log row did not marshal")
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO assist_query_log (exchange_id, asker_id, audience, tool, view, spec, row_count, cited_ids, refusal_code, tokens_in, tokens_out)
		VALUES ($1, $2::uuid, $3, $4, $5, $6::jsonb, $7, $8::jsonb, $9, $10, $11)`,
		e.ExchangeID, e.AskerID, e.Audience, e.Tool, e.View, string(spec), e.RowCount, string(citedJSON), e.Refusal, e.TokensIn, e.TokensOut); err != nil {
		return errors.New("assist: query log row was not written")
	}
	return nil
}

var _ QueryLog = (*Repository)(nil)

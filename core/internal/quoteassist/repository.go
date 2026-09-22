package quoteassist

// repository.go, the Postgres side of migration 0159, and nothing else.
//
// ONE TABLE. quote_scope_rules, which this package owns outright. There is no read of products, of
// quotes, of quote_lines or of anything else in this file: both of those questions are asked through
// consumer interfaces (model.go) and answered by adapters, because this package's claim to import no
// domain has to be true of its SQL as well as of its imports. A repository that reached into
// products for an application would make the module boundary a comment.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the DB-backed RuleStore.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository builds the store over an existing pool.
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Compile-time proof, cheaper than finding a missing method in the composition root.
var _ RuleStore = (*Repository)(nil)

// ruleColumns is the projection, written once so the two queries below cannot drift.
const ruleColumns = `code, title, advice, when_apps, expect_apps, suggest_query, enabled, sort_order`

// Rules reads EVERY rule, enabled and disabled, ordered.
//
// The disabled ones come back on purpose: the admin listing has to show a rule that is switched off
// (that is the whole point of a switch), and Evaluate is the single place `enabled` is applied. A
// `WHERE enabled` here would be a second implementation of the same rule, and it is always the
// second one that gets forgotten when the rule changes.
//
// It returns an EMPTY SLICE for an empty table, never nil-with-no-error. A table with no rows is a
// legitimate configuration, every rule deleted, and it means "suggest nothing", which the caller
// renders as silence rather than as a failure.
func (r *Repository) Rules(ctx context.Context) ([]ScopeRule, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM quote_scope_rules ORDER BY sort_order, code`)
	if err != nil {
		return nil, fmt.Errorf("quoteassist: read scope rules: %w", err)
	}
	defer rows.Close()
	out := make([]ScopeRule, 0, 8)
	for rows.Next() {
		var s ScopeRule
		if err := rows.Scan(&s.Code, &s.Title, &s.Advice, &s.WhenApps, &s.ExpectApps,
			&s.SuggestQuery, &s.Enabled, &s.SortOrder); err != nil {
			return nil, fmt.Errorf("quoteassist: scan scope rule: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("quoteassist: read scope rules: %w", err)
	}
	return out, nil
}

// SetRuleEnabled flips one rule and returns it as it now stands.
//
// RETURNING rather than a second SELECT: the surface re-renders from the row it just wrote, so a
// concurrent edit by another administrator cannot leave the screen showing a state that was never in
// the table. updated_by is stamped with the ACTOR, this is a config change somebody made, and the
// admin listing says who and when.
//
// A missing code is ErrRuleNotFound, not a silent no-op. A PATCH to a rule that does not exist is a
// stale client or a typo, and answering 200 to it would let a screen show a switch it did not throw.
func (r *Repository) SetRuleEnabled(ctx context.Context, code string, enabled bool, actorID string) (ScopeRule, error) {
	// An empty actor is stored as NULL rather than as an empty uuid: the column is a users(id) FK,
	// and a fabricated actor in an audit column is worse than an honest absence.
	var actor any
	if actorID != "" {
		actor = actorID
	}
	var s ScopeRule
	err := r.pool.QueryRow(ctx,
		`UPDATE quote_scope_rules
		    SET enabled = $2, updated_at = now(), updated_by = $3
		  WHERE code = $1
		RETURNING `+ruleColumns, code, enabled, actor).
		Scan(&s.Code, &s.Title, &s.Advice, &s.WhenApps, &s.ExpectApps, &s.SuggestQuery, &s.Enabled, &s.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScopeRule{}, ErrRuleNotFound
	}
	if err != nil {
		return ScopeRule{}, fmt.Errorf("quoteassist: set scope rule enabled: %w", err)
	}
	return s, nil
}

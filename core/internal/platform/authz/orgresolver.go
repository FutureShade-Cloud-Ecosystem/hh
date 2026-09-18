package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NormalizeOrgSlug folds a slug to the lowercase form the platform
// guarantees for org team names (ADR-0020 addendum). The anchor slug
// column is CITEXT (core/db/migrations/0213_org_tenant_seq_deleted_idem.sql),
// so a mixed-case value would match anyway; the resolver normalizes ahead
// of the read so the lowercase form is also the form that travels onward
// in the stamped scope, and a nonconforming mixed-case slug cannot smuggle
// a second spelling of one org into the system.
func NormalizeOrgSlug(slug string) string {
	return strings.ToLower(slug)
}

// PgxOrgResolver is the concrete OrgResolver: the org anchor read (the org
// table, migration 0213, owned by this module) through a pgx pool. It is
// the ONLY core-database touch in the middleware and is reached only on
// the success path, after Team membership passed (ADR-0020), so a refused
// request executes no core-database statement. The pool arrives by
// constructor so wiring and the zero-statement proof build the same shape:
// both go through NewPgxPoolWithTracer, with the tracer attached in the
// proof (QA finding F5, B2 review-notes item 6).
type PgxOrgResolver struct {
	pool *pgxpool.Pool
}

// NewPgxOrgResolver validates the collaborator and returns the resolver.
func NewPgxOrgResolver(pool *pgxpool.Pool) (*PgxOrgResolver, error) {
	if pool == nil {
		return nil, errors.New("authz: pgx org resolver requires a pool")
	}
	return &PgxOrgResolver{pool: pool}, nil
}

// ResolveSlug resolves the normalized slug against the org anchor. A slug
// that fails the pinned shape after normalization is an error, not a miss:
// such a value cannot have come from the header leg (which answers 400
// earlier), so it names a platform-side defect and fails closed loudly.
// ok false with a nil error is the provisioning gap: membership passed,
// the anchor row is missing.
func (r *PgxOrgResolver) ResolveSlug(ctx context.Context, orgSlug string) (string, bool, error) {
	slug := NormalizeOrgSlug(orgSlug)
	if !orgSlugPattern.MatchString(slug) {
		return "", false, errors.New("authz: org slug does not match the pinned shape")
	}
	var orgID string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, slug).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("authz: org anchor read failed: %w", err)
	}
	return orgID, true, nil
}

// NewPgxPoolWithTracer builds the pool the middleware's database seams
// share, with an optional pgx QueryTracer attached. The tracer exists so
// the zero-statement property is provable in the pgx form: the proof and
// the serve wiring construct the identical pool, differing only in
// whether a tracer observes it. A nil tracer is the production shape.
func NewPgxPoolWithTracer(ctx context.Context, databaseURL string, tracer pgx.QueryTracer) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("authz: database URL is not parseable: %w", err)
	}
	if tracer != nil {
		// pgx v5 carries one QueryTracer per connection config; one is
		// all the proof needs.
		cfg.ConnConfig.Tracer = tracer
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

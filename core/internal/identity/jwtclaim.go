package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The single use claim on a FB Console JWT (state note section 3, gate 3b).
// The table console_jwt_claim (migration 0387) belongs to this module and
// is reached only through ClaimJWT, so no other module touches it.

const (
	// MinJWTClaimBound is the probed Console JWT life (900 seconds). The
	// configured bound on a JWT's exp, now plus the bound, may not be
	// lower, or a live JWT could be refused for its own exp.
	MinJWTClaimBound = 900 * time.Second
	// JWTClaimPurgeBatch is the most expired rows one claim removes, so a
	// purge is bounded work on the sign in path.
	JWTClaimPurgeBatch = 100
)

// ErrJWTExpOutOfBound: the JWT's exp is missing or lies beyond now plus the
// configured bound, so retention could not cover its life. The caller
// answers 401.
var ErrJWTExpOutOfBound = errors.New("identity: the JWT exp is missing or beyond the configured bound")

// CheckJWTClaimBound is the boot rule for CORE_EXCHANGE_JWT_BOUND.
func CheckJWTClaimBound(bound time.Duration) error {
	if bound < MinJWTClaimBound {
		return fmt.Errorf("identity: the JWT claim bound is below the console JWT life of %s", MinJWTClaimBound)
	}
	return nil
}

// HashJWTSignature is the claim key: SHA-256 over the decoded signature
// bytes (the canonical form; the Console JWT carries no jti).
func HashJWTSignature(signature []byte) [32]byte { return sha256.Sum256(signature) }

// JWTClaims claims Console JWTs.
type JWTClaims struct {
	pool  *pgxpool.Pool
	bound time.Duration
	now   func() time.Time
}

// NewJWTClaims validates the collaborators and the bound.
func NewJWTClaims(pool *pgxpool.Pool, bound time.Duration) (*JWTClaims, error) {
	if pool == nil {
		return nil, errors.New("identity: the JWT claim store requires a pool")
	}
	if err := CheckJWTClaimBound(bound); err != nil {
		return nil, err
	}
	return &JWTClaims{pool: pool, bound: bound, now: time.Now}, nil
}

// ClaimJWT records the JWT as spent with one atomic INSERT ... ON CONFLICT
// DO NOTHING: claimed is true for exactly one of any number of concurrent
// presentations of one signature, across replicas and restarts. An exp that
// is the zero time or beyond now plus the bound is ErrJWTExpOutOfBound and
// writes nothing. After the decision, up to JWTClaimPurgeBatch rows past
// their exp are removed in the same call; a purge failure never changes
// the claim's answer (the next claim purges again).
func (c *JWTClaims) ClaimJWT(ctx context.Context, sigHash [32]byte, exp time.Time) (bool, error) {
	now := c.now()
	if exp.IsZero() || exp.After(now.Add(c.bound)) {
		return false, ErrJWTExpOutOfBound
	}
	tag, err := c.pool.Exec(ctx,
		`INSERT INTO console_jwt_claim (sig_hash, exp, claimed_at) VALUES ($1, $2, $3)
		 ON CONFLICT (sig_hash) DO NOTHING`, sigHash[:], exp, now)
	if err != nil {
		return false, errors.New("identity: the JWT claim could not be written")
	}
	claimed := tag.RowsAffected() == 1
	_, _ = c.pool.Exec(ctx,
		`DELETE FROM console_jwt_claim WHERE sig_hash IN (
		   SELECT sig_hash FROM console_jwt_claim WHERE exp < $1 LIMIT $2)`, now, JWTClaimPurgeBatch)
	return claimed, nil
}

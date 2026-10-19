package identity

// Revocable core sessions (HH-678). A revocation kept only in memory is
// lost on a restart and is not shared between instances, so a signed out
// session would come back. The table revoked_sessions (migration 0413)
// belongs to this module and is reached only through SessionRevocations:
// it stores the revoked jti with its expiry, rows past expiry are swept
// opportunistically on write, and a short in memory cache of known
// revoked ids sits in front of the read so a repeated verification costs
// no query. A revocation on this instance takes effect immediately: the
// in memory map is written before the row is.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RevocationPurgeBatch is the most expired rows one revocation removes,
// mirroring the single use JWT claim's purge bound.
const RevocationPurgeBatch = 100

// RevocationChecker is the verifier's narrow read: whether the token id
// stands revoked. The verifier holds it through Config.Revocations or
// SetRevocations.
type RevocationChecker interface {
	IsRevoked(ctx context.Context, jti string) (bool, error)
}

// SessionRevocations stores and reads session token revocations over the
// pool, with a short in memory cache of known revoked ids in front. It is
// safe for concurrent use.
type SessionRevocations struct {
	pool *pgxpool.Pool
	now  func() time.Time
	mu   sync.Mutex
	// revoked is the in memory cache of known revoked jti -> exp. An entry
	// past its exp is dead weight and is swept on the next write.
	revoked map[string]time.Time
	// clean is the negative cache: jti -> when its "not revoked" answer
	// expires. Its TTL is short, so a revocation written by another
	// instance (or before the in memory path sees it) is picked up
	// quickly, while repeated verifications of one live token cost no
	// query.
	clean map[string]time.Time
	// unpersisted names the jtis revoked in memory whose Postgres row
	// could not be written (HH-678 P2-2): IsRevoked keeps answering true
	// for them on this instance, while EnsurePersisted knows the row is
	// still owed to the database.
	unpersisted map[string]time.Time
	cleanTTL    time.Duration
	// writeRow, when set, replaces the pool's INSERT (tests fail the row
	// write deterministically while reads still succeed).
	writeRow func(ctx context.Context, jti string, exp, now time.Time) error
}

// NewSessionRevocations builds the store over the pool. A nil pool is the
// in memory only shape (tests, the composition before the database
// exists): revocations are remembered and reads answer from memory alone.
func NewSessionRevocations(pool *pgxpool.Pool) *SessionRevocations {
	return &SessionRevocations{
		pool:        pool,
		now:         time.Now,
		revoked:     map[string]time.Time{},
		clean:       map[string]time.Time{},
		unpersisted: map[string]time.Time{},
		cleanTTL:    2 * time.Second,
	}
}

// WithClock sets the clock (tests drive their own).
func (s *SessionRevocations) WithClock(now func() time.Time) *SessionRevocations {
	if now != nil {
		s.now = now
	}
	return s
}

// WithWriteRow replaces the pool's row write (tests fail the write
// deterministically while reads still succeed).
func (s *SessionRevocations) WithWriteRow(f func(ctx context.Context, jti string, exp, now time.Time) error) *SessionRevocations {
	s.writeRow = f
	return s
}

// Revoke records the jti as revoked until exp. An empty jti or one whose
// exp has passed writes nothing (the token is dead by itself). The in
// memory cache is written first, so a revocation on this instance holds
// even when the row cannot be written; a failed row write is remembered in
// unpersisted (so EnsurePersisted retries it on a later sign out, P2-2),
// and with a pool the row makes the revocation hold across restarts and
// instances, with up to RevocationPurgeBatch expired rows swept in the
// same call.
func (s *SessionRevocations) Revoke(ctx context.Context, jti string, exp time.Time) error {
	now := s.now()
	jti = strings.TrimSpace(jti)
	if jti == "" || !exp.After(now) {
		return nil
	}
	s.mu.Lock()
	s.revoked[jti] = exp
	delete(s.clean, jti)
	for id, e := range s.revoked {
		if !e.After(now) {
			delete(s.revoked, id)
			delete(s.unpersisted, id)
		}
	}
	s.mu.Unlock()
	if s.pool == nil && s.writeRow == nil {
		return nil
	}
	var writeErr error
	if s.writeRow != nil {
		writeErr = s.writeRow(ctx, jti, exp, now)
	} else {
		_, writeErr = s.pool.Exec(ctx,
			`INSERT INTO revoked_sessions (jti, exp, revoked_at) VALUES ($1, $2, $3)
			 ON CONFLICT (jti) DO NOTHING`, jti, exp, now)
	}
	if writeErr != nil {
		s.mu.Lock()
		s.unpersisted[jti] = exp
		s.mu.Unlock()
		return errors.New("identity: the revocation could not be written")
	}
	s.mu.Lock()
	delete(s.unpersisted, jti)
	s.mu.Unlock()
	if s.pool != nil {
		_, _ = s.pool.Exec(ctx,
			`DELETE FROM revoked_sessions WHERE jti IN (
			   SELECT jti FROM revoked_sessions WHERE exp < $1 LIMIT $2)`, now, RevocationPurgeBatch)
	}
	return nil
}

// EnsurePersisted retries the row write when jti stands revoked in memory on this
// instance but its Postgres insert failed on an earlier call (HH-678 P2-2). When the
// row is already persisted (or has no pending write), it is a no-op and returns nil.
func (s *SessionRevocations) EnsurePersisted(ctx context.Context, jti string, exp time.Time) error {
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	pendingExp, pending := s.unpersisted[jti]
	if pending && !pendingExp.After(now) {
		delete(s.unpersisted, jti)
		pending = false
	}
	s.mu.Unlock()
	if !pending {
		return nil
	}
	if exp.IsZero() {
		exp = pendingExp
	}
	return s.Revoke(ctx, jti, exp)
}

// IsRevoked reports whether the jti stands revoked. The in memory cache of
// revoked ids answers first; a cached negative answer inside its TTL
// answers second; otherwise the table is read and a revoked row is cached
// in memory. A jti past its exp is not revoked (the token is dead by
// itself). Without a pool only the in memory cache answers.
func (s *SessionRevocations) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	now := s.now()
	s.mu.Lock()
	exp, revokedKnown := s.revoked[jti]
	cleanUntil, cleanKnown := s.clean[jti]
	s.mu.Unlock()
	if revokedKnown && exp.After(now) {
		return true, nil
	}
	if cleanKnown && now.Before(cleanUntil) {
		return false, nil
	}
	if s.pool == nil {
		return false, nil
	}
	var rowExp time.Time
	err := s.pool.QueryRow(ctx, `SELECT exp FROM revoked_sessions WHERE jti = $1`, jti).Scan(&rowExp)
	if errors.Is(err, pgx.ErrNoRows) {
		s.mu.Lock()
		s.clean[jti] = now.Add(s.cleanTTL)
		s.mu.Unlock()
		return false, nil
	}
	if err != nil {
		return false, errors.New("identity: the revocation source could not be read")
	}
	if !rowExp.After(now) {
		return false, nil
	}
	// The row is in the database, so any earlier failed write of it is
	// no longer owed.
	s.mu.Lock()
	s.revoked[jti] = rowExp
	delete(s.unpersisted, jti)
	s.mu.Unlock()
	return true, nil
}

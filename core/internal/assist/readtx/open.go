package readtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Readers are the assist read path's two audiences. Pro is nil when no
// contractor URL is configured.
type Readers struct {
	Staff *Reader
	Pro   *Reader

	pools []*pgxpool.Pool
}

// Close closes both pools.
func (r *Readers) Close() {
	for _, p := range r.pools {
		p.Close()
	}
}

// Open opens the assist pools. It returns an error when the staff pool or the
// key is missing or unusable; the caller then leaves the assist data path off
// and the rest of the app unaffected. No error carries a URL, a key or a driver
// message: a database URL holds its password.
func Open(ctx context.Context, cfg Config) (*Readers, error) {
	signer, err := NewSigner(cfg.ClaimsKey)
	if err != nil {
		return nil, err
	}
	if cfg.ReaderURL.IsEmpty() {
		return nil, errors.New("readtx: " + EnvReaderURL + " is not set")
	}
	out := &Readers{}
	staff, err := openOne(ctx, cfg.ReaderURL, AudienceStaff, signer, "staff")
	if err != nil {
		return nil, err
	}
	out.Staff, out.pools = staff.reader, append(out.pools, staff.pool)
	if !cfg.ProReaderURL.IsEmpty() {
		pro, err := openOne(ctx, cfg.ProReaderURL, AudiencePro, signer, "contractor")
		if err != nil {
			out.Close()
			return nil, err
		}
		out.Pro, out.pools = pro.reader, append(out.pools, pro.pool)
	}
	return out, nil
}

type opened struct {
	reader *Reader
	pool   *pgxpool.Pool
}

func openOne(ctx context.Context, url Secret, audience string, signer *Signer, label string) (opened, error) {
	pc, err := pgxpool.ParseConfig(url.Reveal())
	if err != nil {
		return opened{}, fmt.Errorf("readtx: the %s reader URL does not parse", label)
	}
	pc.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return opened{}, fmt.Errorf("readtx: the %s reader pool did not start", label)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return opened{}, fmt.Errorf("readtx: the %s reader pool cannot connect%s", label, sqlState(err))
	}
	var db, user string
	if err := pool.QueryRow(ctx, `SELECT current_database(), session_user`).Scan(&db, &user); err != nil {
		pool.Close()
		return opened{}, fmt.Errorf("readtx: the %s reader pool cannot read its own session%s", label, sqlState(err))
	}
	roles := RolesFor(db)
	role, login := roles.Reader, roles.Login
	if audience == AudiencePro {
		role, login = roles.ProReader, roles.ProLogin
	}
	_ = login
	return opened{
		reader: &Reader{
			audience: audience, role: role, signer: signer, limits: DefaultLimits,
			begin: func(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error) { return pool.BeginTx(ctx, o) },
		},
		pool: pool,
	}, nil
}

// sqlState renders only a Postgres error's five character code, which is safe to
// show; any other error contributes nothing.
func sqlState(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return " (sqlstate " + pg.Code + ")"
	}
	return ""
}

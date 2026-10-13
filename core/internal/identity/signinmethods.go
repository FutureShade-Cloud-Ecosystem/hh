package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The dealer's allowed sign in methods (state note section 5b, row 1524).
// The setting is a closed vocabulary of method codes per org in
// org_signin_method (migration 0388), owned by this module and reached only
// through SignInMethodStore, so no other module touches the table. There is
// no write route in this item: Dealer Admin writes it later and the populate
// step seeds it.

// The method codes. Microsoft and magic link are designed in and built after
// the demo; the vocabulary names them so a dealer's row can already hold
// them.
const (
	MethodEmailPassword = "email_password"
	MethodGoogle        = "google"
	MethodMicrosoft     = "microsoft"
	MethodMagicLink     = "magic_link"
)

// SignInMethodCodes is the closed vocabulary, in canonical order. Each call
// returns a fresh slice.
func SignInMethodCodes() []string {
	return []string{MethodEmailPassword, MethodGoogle, MethodMicrosoft, MethodMagicLink}
}

// DefaultSignInMethods is the set of an org with no row: the house sign in
// and Google, the demo's two methods.
func DefaultSignInMethods() []string {
	return []string{MethodEmailPassword, MethodGoogle}
}

// SignInMethodStore reads the setting.
type SignInMethodStore struct {
	pool *pgxpool.Pool
}

// NewSignInMethodStore validates the pool.
func NewSignInMethodStore(pool *pgxpool.Pool) (*SignInMethodStore, error) {
	if pool == nil {
		return nil, errors.New("identity: the sign in method store requires a pool")
	}
	return &SignInMethodStore{pool: pool}, nil
}

// AllowedSignInMethods answers the org's allowed codes in vocabulary order:
// the default set when the org has no row. A code outside the vocabulary in
// the table is a data defect and the answer is an error, never a smaller
// set. A malformed org id is an error too, never the default.
func (s *SignInMethodStore) AllowedSignInMethods(ctx context.Context, orgID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT code FROM org_signin_method WHERE org_id = $1::uuid`, orgID)
	if err != nil {
		return nil, errors.New("identity: the sign in methods could not be read")
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, errors.New("identity: the sign in methods could not be read")
		}
		codes = append(codes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("identity: the sign in methods could not be read")
	}
	return signInMethodSet(codes)
}

// signInMethodSet turns the stored codes into the answer: the default for no
// rows, the codes in vocabulary order, and a refusal for any code outside
// the vocabulary (compared exactly: no case folding, no trimming).
func signInMethodSet(codes []string) ([]string, error) {
	if len(codes) == 0 {
		return DefaultSignInMethods(), nil
	}
	have := make(map[string]bool, len(codes))
	for _, c := range codes {
		have[c] = true
	}
	var out []string
	for _, c := range SignInMethodCodes() {
		if have[c] {
			out = append(out, c)
			delete(have, c)
		}
	}
	if len(have) != 0 {
		return nil, fmt.Errorf("identity: the org's sign in methods hold %d code(s) outside the vocabulary", len(have))
	}
	return out, nil
}

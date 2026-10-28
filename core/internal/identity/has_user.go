package identity

// has_user.go, the existence read on the users table. Identity owns users,
// so a caller that wants to know whether a login is present asks here
// rather than spelling the query itself: the seed guards key on fixed
// logins (a database that holds the login has the seed applied), and
// cmd/core carries no SQL of its own.

import (
	"context"
	"errors"
	"fmt"
)

// HasUser reports whether a user row with this email exists.
func HasUser(ctx context.Context, db DBTX, email string) (bool, error) {
	if db == nil {
		return false, errors.New("identity: reading a user requires a database handle")
	}
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, email).Scan(&exists); err != nil {
		return false, fmt.Errorf("identity: reading the user by login: %w", err)
	}
	return exists, nil
}

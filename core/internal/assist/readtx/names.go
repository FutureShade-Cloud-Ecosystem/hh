// Package readtx is the assistant's read path to the database: two pools that
// log in as the assist logins (never the app role), the signed claims that tell
// the database who is asking, and the one function that runs one tool call as
// one read only transaction. It holds no view, no row policy and no query
// compiler; the data slices add those on top of this boundary.
package readtx

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
)

// Roles are the seven database role names of one database. Role names are
// cluster wide and dealer databases may share a cluster, so every name carries
// a suffix derived from the database name. Migration 0418 derives the same
// suffix in SQL; the integration test holds the two together.
type Roles struct {
	ViewOwner   string
	ClaimsOwner string
	Reader      string
	ProReader   string
	Login       string
	ProLogin    string
	// FunctionUsers is not an assist role: it receives EXECUTE on every function
	// migration 0418 closes to PUBLIC, and a serving role that is separate from the
	// migrating role must be made a member. No assist role may ever be.
	FunctionUsers string
}

const maxSuffix = 30

// Suffix derives a role name suffix from a database name: the name in ASCII
// lower case with every other character turned into an underscore. A name that
// the derivation changed, or one longer than the budget, gets its stem cut to
// 21 characters and the first eight hex characters of the md5 of the original
// name, so two databases whose names normalise alike never share a role.
func Suffix(database string) string {
	norm := normalise(database)
	if norm == database && len(norm) <= maxSuffix {
		return norm
	}
	stem := norm
	if len(stem) > 21 {
		stem = stem[:21]
	}
	return stem + "_" + md5Prefix(database)
}

// RolesFor names the roles of the database called database.
func RolesFor(database string) Roles {
	s := Suffix(database)
	return Roles{
		ViewOwner:     "assist_view_owner_" + s,
		ClaimsOwner:   "assist_claims_owner_" + s,
		Reader:        "assist_reader_" + s,
		ProReader:     "assist_pro_reader_" + s,
		Login:         "assist_login_" + s,
		ProLogin:      "assist_pro_login_" + s,
		FunctionUsers: "app_function_users_" + s,
	}
}

func normalise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func md5Prefix(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

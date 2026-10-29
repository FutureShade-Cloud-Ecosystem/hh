package main

// finance_reset_roles.go carries the reset's role mapping (W2 reset fix r1,
// finding 3): the world's dump is built on one cluster, under one database
// name, by one migrating role, and the box it lands on is another: another
// database name, another suffix on the assist role names, another role doing
// the restoring. The dump must not depend on the build cluster's role names,
// so the reset maps them onto the box's own before the restore.
//
// Three shapes are mapped:
//
//   - the eight assist role names, the ones migration 0418 and the migrations
//     after it derive from the database's own name: the build database's
//     suffix becomes the box database's own suffix, derived the way
//     readtx.Suffix derives it;
//   - the default privileges statements, which name the role that made them:
//     the box's own restoring role takes their place, because it is the role
//     that owns and creates what the box holds from now on, and because it
//     is the only role the box can always change the default privileges of;
//   - the owner statements, which the world's dump is taken without: the
//     restoring role owns what it restores, the way the box's own migrations
//     leave their objects owned by the migrating role.
//
// Only statement lines are touched, and never a COPY block: the world's own
// data is copied verbatim, whatever it says.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

// financeResetAssistRolePrefixes are the eight role names migration 0418 and
// the migrations after it derive from the database's own name. The world's
// dump carries the build database's suffix on each of them; the box's
// database has its own.
var financeResetAssistRolePrefixes = []string{
	"assist_view_owner_",
	"assist_claims_owner_",
	"assist_reader_",
	"assist_pro_reader_",
	"assist_zone_reader_",
	"assist_login_",
	"assist_pro_login_",
	"app_function_users_",
}

// financeResetAssistRoleShape matches one of those names with the suffix the
// derivation gave it. The suffix is the database's name in ASCII lower case
// with every other character an underscore, or a cut stem joined to the
// first eight hex characters of the name's md5.
var financeResetAssistRoleShape = regexp.MustCompile(`\b(` + strings.Join(financeResetAssistRolePrefixes, "|") + `)([a-z0-9_]+)\b`)

// financeResetDefaultPrivilegesShape matches the default privileges
// statements, the ones that name the role the defaults belong to.
var financeResetDefaultPrivilegesShape = regexp.MustCompile(`(?i)^(ALTER DEFAULT PRIVILEGES FOR (?:ROLE|USER) )("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)

// financeResetOwnerShape matches the owner statements: the role that owns the
// object.
var financeResetOwnerShape = regexp.MustCompile(`(?i)^(ALTER [A-Z ]+ [^;]* OWNER TO )("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)

// financeResetAssistRoleNameShape matches one of the assist role names whole,
// with the suffix the derivation gave it.
var financeResetAssistRoleNameShape = regexp.MustCompile(`^(` + strings.Join(financeResetAssistRolePrefixes, "|") + `)([a-z0-9_]+)$`)

// financeResetQuote quotes an identifier the way Postgres does.
func financeResetQuote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// financeResetStatementPrefixes are the statements that carry a role name.
// The dump's data never begins with one of these words: a COPY block's rows
// are copied verbatim and are skipped whole.
var financeResetStatementPrefixes = []string{
	"ALTER ", "GRANT ", "REVOKE ", "CREATE ROLE ", "DROP ROLE ", "SET ROLE ", "SET SESSION AUTHORIZATION ",
}

// financeResetBuildSuffix answers the suffix the world's dump carries on the
// assist role names: the build database's own suffix. Every assist role name
// in the dump must carry the same one: a dump that carries two is a dump the
// reset cannot map onto the box's own roles, and the reset refuses before it
// drops anything.
func financeResetBuildSuffix(dump []byte) (string, error) {
	found := map[string]bool{}
	for _, line := range financeResetNonDataLines(dump) {
		for _, m := range financeResetAssistRoleShape.FindAllStringSubmatch(line, -1) {
			found[m[2]] = true
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		for suffix := range found {
			return suffix, nil
		}
	}
	suffixes := make([]string, 0, len(found))
	for suffix := range found {
		suffixes = append(suffixes, suffix)
	}
	return "", fmt.Errorf("finance-reset: the world's dump carries more than one assist role suffix (%s): the reset cannot map them onto the box's own roles, so it refuses before it drops anything",
		strings.Join(suffixes, ", "))
}

// financeResetNonDataLines answers the dump's lines that are not the world's
// own data: the statements, the comments and the bodies of the world's own
// functions. A COPY block's rows are the world's business data and are never
// touched.
//
// The world's functions belong here: a function body may carry a role name
// or a statement that may not run inside a transaction, and both are part of
// the world's shape, not part of its data. The claims function compares
// session_user against the login role names, and those names are part of the
// world's shape too.
func financeResetNonDataLines(dump []byte) []string {
	var lines []string
	inCopy := false
	for _, line := range strings.Split(string(dump), "\n") {
		trimmed := strings.TrimSpace(line)
		if inCopy {
			if trimmed == `\.` {
				inCopy = false
			}
			continue
		}
		if financeResetCopyStart(trimmed) {
			inCopy = true
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// financeResetStatementLines answers the dump's statement lines: every line
// that carries SQL the reset may rewrite in a statement position, with the
// COPY blocks left out.
func financeResetStatementLines(dump []byte) []string {
	var lines []string
	for _, line := range financeResetNonDataLines(dump) {
		if !financeResetIsStatement(strings.TrimSpace(line)) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// financeResetCopyStart answers whether a line opens a COPY block.
//
// The line is upper cased first and the spelling is the upper cased one: a
// mixed case spelling here never matches, and a copy block that is never
// recognised is the world's own data read as script.
func financeResetCopyStart(trimmed string) bool {
	upper := strings.ToUpper(trimmed)
	return strings.HasPrefix(upper, "COPY ") && strings.HasSuffix(upper, "FROM STDIN;")
}

// financeResetIsStatement answers whether a line carries SQL that names a
// role. The dump's data lives in COPY blocks, which are skipped whole, so a
// line that begins with one of the statement words is a statement.
func financeResetIsStatement(trimmed string) bool {
	upper := strings.ToUpper(trimmed)
	for _, prefix := range financeResetStatementPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// financeResetMapRoles maps the world's role names onto the box's own, and
// answers the dump to restore. The box's suffix comes from the box's own
// database name, the way migration 0418 derives it, and the restoring role
// is the role that restores: the box's counterpart of the build's migrating
// role.
//
// The mapping never touches the world's data: only statement lines are
// rewritten, and a COPY block is copied verbatim.
func financeResetMapRoles(dump []byte, buildSuffix, boxSuffix, restoringRole string) ([]byte, error) {
	if restoringRole == "" {
		return nil, fmt.Errorf("finance-reset: the restoring role is not known: the dump cannot be mapped onto the box's own roles")
	}
	lines := strings.Split(string(dump), "\n")
	inCopy := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inCopy {
			if trimmed == `\.` {
				inCopy = false
			}
			continue
		}
		if financeResetCopyStart(trimmed) {
			inCopy = true
			continue
		}
		mapped := line
		// The role names are mapped on every line that is not the world's
		// own data: a grant, a row level security policy, a default
		// privileges statement, an owner statement, and the world's own
		// functions, whose claims function compares session_user against the
		// login role names.
		if buildSuffix != "" && buildSuffix != boxSuffix {
			mapped = financeResetAssistRoleShape.ReplaceAllStringFunc(mapped, func(name string) string {
				m := financeResetAssistRoleShape.FindStringSubmatch(name)
				if m == nil || m[2] != buildSuffix {
					return name
				}
				return m[1] + boxSuffix
			})
		}
		// The statement positions the role names sit in are mapped on their
		// own: the default privileges statements and the owner statements.
		if financeResetIsStatement(trimmed) {
			mapped = financeResetMapRolePositions(mapped, buildSuffix, boxSuffix, restoringRole)
		}
		lines[i] = mapped
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// financeResetMapRolePositions maps the role names of one statement line.
func financeResetMapRolePositions(line, buildSuffix, boxSuffix, restoringRole string) string {
	// The default privileges statements name the role the defaults belong to.
	// The box's own restoring role takes their place: it is the role that
	// will create what the box holds from now on, and the only role whose
	// default privileges the box's own role can always change. A role that
	// holds the assist roles with SET but not INHERIT (the shape migration
	// 0418 gives them) cannot change another role's defaults, so a statement
	// left on the build's role would refuse the restore.
	line = financeResetDefaultPrivilegesShape.ReplaceAllString(line, "${1}"+restoringRole)
	// The owner statements: the assist roles keep their kind and take the
	// box's own suffix, and every other owner becomes the restoring role,
	// which is the role that owns what the box holds from now on.
	line = financeResetOwnerShape.ReplaceAllStringFunc(line, func(statement string) string {
		m := financeResetOwnerShape.FindStringSubmatch(statement)
		if m == nil {
			return statement
		}
		owner := strings.Trim(m[2], `"`)
		if assist := financeResetAssistRoleNameShape.FindStringSubmatch(owner); assist != nil {
			return m[1] + financeResetQuote(assist[1]+boxSuffix)
		}
		return m[1] + financeResetQuote(restoringRole)
	})
	return line
}

// financeResetOwnerRoles answers the roles the world's dump gives its objects
// to: the assist roles only. The box's own role can always own what it
// restores, so the ones that need a check are the assist roles: a role may
// only hand an object to a role it can set.
func financeResetOwnerRoles(dump []byte) []string {
	seen := map[string]bool{}
	var roles []string
	for _, line := range financeResetStatementLines(dump) {
		m := financeResetOwnerShape.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		owner := strings.Trim(m[2], `"`)
		if !financeResetAssistRoleNameShape.MatchString(owner) {
			continue
		}
		if seen[owner] {
			continue
		}
		seen[owner] = true
		roles = append(roles, owner)
	}
	return roles
}

// financeResetBoxRoles answers the box's own role shape: the suffix the
// assist role names carry on this database, derived the way migration 0418
// derives it, and the role that is restoring.
func financeResetBoxRoles(ctx context.Context, pool *pgxpool.Pool) (string, string, error) {
	var database, role string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return "", "", fmt.Errorf("finance-reset: the box's database name could not be read: %w", err)
	}
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&role); err != nil {
		return "", "", fmt.Errorf("finance-reset: the restoring role could not be read: %w", err)
	}
	return readtx.Suffix(database), role, nil
}

// financeResetMappedRoles answers the box's own assist role names: the eight
// names the box's database's suffix gives them. They are the names the
// world's grants land on.
func financeResetMappedRoles(boxSuffix string) []string {
	roles := readtx.RolesFor(boxSuffix)
	return []string{
		roles.ViewOwner,
		roles.ClaimsOwner,
		roles.Reader,
		roles.ProReader,
		roles.ZoneReader,
		roles.Login,
		roles.ProLogin,
		roles.FunctionUsers,
	}
}

// financeResetOwnershipPossible answers whether the box's own role may hand
// the world's objects over to the assist roles: a role may only give an
// object to a role it can set. The check is made on a throwaway role switch
// inside a transaction that is rolled back, so nothing is left behind.
//
// It runs before anything is dropped: a box whose role cannot carry the
// world's own owners is refused with its tables still in place, never with
// them gone and the restore half done.
func financeResetOwnershipPossible(ctx context.Context, pool *pgxpool.Pool, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-reset: the world's object owners could not be checked: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, role := range roles {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+financeResetQuote(role)); err != nil {
			return fmt.Errorf("finance-reset: this role cannot hand the world's objects to %s, so the reset refuses before it drops anything: %w", role, err)
		}
		if _, err := tx.Exec(ctx, "RESET ROLE"); err != nil {
			return fmt.Errorf("finance-reset: the world's object owners could not be checked: %w", err)
		}
	}
	return nil
}

// financeResetDumpAssistRoles answers the assist role names the dump names,
// once they are mapped onto the box's own. They are the roles the world's
// grants land on.
func financeResetDumpAssistRoles(dump []byte) []string {
	seen := map[string]bool{}
	var names []string
	for _, line := range financeResetStatementLines(dump) {
		for _, m := range financeResetAssistRoleShape.FindAllStringSubmatch(line, -1) {
			if seen[m[0]] {
				continue
			}
			seen[m[0]] = true
			names = append(names, m[0])
		}
	}
	return names
}

// financeResetAssistRolesPresent answers that the roles the world's grants
// name are there: a grant to a role that does not exist refuses the restore.
// The box's own migrate creates them, so a migrated box has them; a box that
// has not is refused with its tables still in place, never with them gone
// and the restore half done.
func financeResetAssistRolesPresent(ctx context.Context, pool *pgxpool.Pool, dump []byte) error {
	for _, name := range financeResetDumpAssistRoles(dump) {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).Scan(&exists); err != nil {
			return fmt.Errorf("finance-reset: the box's role %s could not be checked: %w", name, err)
		}
		if !exists {
			return fmt.Errorf("finance-reset: the box's role %s does not exist: the world's grants land on the box's own roles, so the reset refuses before it drops anything", name)
		}
	}
	return nil
}

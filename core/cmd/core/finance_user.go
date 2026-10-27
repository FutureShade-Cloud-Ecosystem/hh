// finance_user.go carries the finance-user role (W2-2): the one-shot the
// principal runs once per named tester on the finance box. One person per
// run, by email, FB Console user id, display name and role, all at run
// time: the command provisions the org anchor, an ACTIVE users row and a
// global staff grant so the console exchange's gate 6 links the person to
// their role, and --revoke closes what it opened. Every write lands
// through the identity and audit modules' own seams inside one
// transaction, no agent ever sets or sees a password, and nothing is ever
// deleted. The guard refuses anywhere but the finance box, one line naming
// the check, never a value.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/exchange"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// The environment the finance box alone is allowed to carry. W2-1's
// boxkind guard owns the boot refusals over the same names; these local
// names keep this command self-contained until the lead reconciles the
// pair.
const (
	envBoxKind        = "HH_BOX_KIND"
	envAppEnv         = "APP_ENV"
	envWebOrg         = "HH_WEB_ORG"
	boxKindFinance    = "finance"
	financeBoxOrgName = "Finance Box"
)

// financeUserRoles is the one allowed list of finance roles a named tester
// may hold (the finance roles of the staff_role catalog, every one global
// scope). The default is financial_controller; the second finance role is
// what makes the four eyes approvals hold two people.
var financeUserRoles = []string{
	"financial_controller",
	"payables_receivables",
	"general_manager",
}

const financeUserDefaultRole = "financial_controller"

// financeBoxReady is the command's guard: the environment must name the
// finance box and the staging kind, the sign in must be the console
// exchange, and the database itself must carry the box kind mark. Each
// refusal is one line naming the check, never a value.
func financeBoxReady(ctx context.Context, getenv func(string) string, pool *pgxpool.Pool) error {
	if strings.TrimSpace(getenv(envBoxKind)) != boxKindFinance {
		return fmt.Errorf("finance-user: %s must name the finance box", envBoxKind)
	}
	if strings.TrimSpace(getenv(envAppEnv)) != "staging" {
		return fmt.Errorf("finance-user: %s must be staging", envAppEnv)
	}
	if strings.TrimSpace(getenv(envSigninMode)) != "console" {
		return fmt.Errorf("finance-user: %s must be console", envSigninMode)
	}
	kind, err := settings.NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil {
		return fmt.Errorf("finance-user: the database box kind (%s) could not be read: %w", settings.KeyBoxKind, err)
	}
	if kind != boxKindFinance {
		return fmt.Errorf("finance-user: the database is not marked as a finance box (%s)", settings.KeyBoxKind)
	}
	return nil
}

func financeUserRole(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("finance-user", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "the person's email, on the sign in allow list (required)")
	platformUserID := fs.String("platform-user-id", "", "the person's FB Console user id (required)")
	displayName := fs.String("display-name", "", "the person's display name (required)")
	role := fs.String("role", financeUserDefaultRole, "the finance role to grant")
	revoke := fs.Bool("revoke", false, "deactivate the person and revoke the grant instead of granting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("finance-user: unexpected arguments %q", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(*email) == "" {
		return errors.New("finance-user: -email is required")
	}
	if strings.TrimSpace(*platformUserID) == "" {
		return errors.New("finance-user: -platform-user-id is required")
	}
	if strings.TrimSpace(*displayName) == "" {
		return errors.New("finance-user: -display-name is required")
	}

	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("finance-user: %w", err)
	}
	printOneshotDatabaseTarget(stderr, "finance-user", dsn)
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("finance-user: cannot connect: %w", err)
	}
	defer pool.Close()

	if err := financeBoxReady(ctx, getenv, pool); err != nil {
		return err
	}
	if !slices.Contains(financeUserRoles, *role) {
		return fmt.Errorf("finance-user: -role must be one of the finance roles: %s", strings.Join(financeUserRoles, ", "))
	}
	listed := map[string]bool{}
	for _, a := range exchange.ParseAllowList(getenv(envSigninEmailAddresses)) {
		listed[a] = true
	}
	if !listed[strings.ToLower(strings.TrimSpace(*email))] {
		return fmt.Errorf("finance-user: the email is not on the sign in allow list (%s)", envSigninEmailAddresses)
	}

	if *revoke {
		return financeUserRevoke(ctx, pool, stdout, getenv, *platformUserID, *role)
	}
	return financeUserProvision(ctx, pool, stdout, getenv, strings.TrimSpace(*email), strings.TrimSpace(*platformUserID), strings.TrimSpace(*displayName), *role)
}

// financeUserProvision writes the org anchor, the staff users row and the
// global grant in one transaction, then the audit row, and prints only the
// users row id and the grant id.
func financeUserProvision(ctx context.Context, pool *pgxpool.Pool, stdout io.Writer, getenv func(string) string, email, platformUserID, displayName, role string) error {
	slug := strings.TrimSpace(getenv(envWebOrg))
	if slug == "" {
		return fmt.Errorf("finance-user: %s must name the org slug", envWebOrg)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-user: cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orgID, _, err := identity.ProvisionOrgAnchor(ctx, tx, slug, financeBoxOrgName)
	if err != nil {
		return fmt.Errorf("finance-user: the org anchor: %w", err)
	}
	userID, _, err := identity.EnsureStaffUser(ctx, tx, orgID, platformUserID, email, displayName)
	if err != nil {
		return fmt.Errorf("finance-user: the staff user: %w", err)
	}
	if _, err := identity.EnsureStaffGrant(ctx, tx, orgID, platformUserID, role, nil); err != nil {
		return fmt.Errorf("finance-user: the staff grant: %w", err)
	}
	grantID, ok, err := identity.StaffGrantID(ctx, tx, orgID, platformUserID, role)
	if err != nil || !ok {
		return fmt.Errorf("finance-user: the staff grant was not found after it was ensured")
	}
	svc := audit.NewService(audit.NewRepository(pool), slog.Default())
	if err := svc.RecordTx(ctx, tx, audit.SensitiveAccess{
		Action:     "STAFF_PROVISION",
		EntityType: "identity.staff_user",
		EntityID:   userID,
		ActorID:    userID,
		Purpose:    "FINANCE_BOX_ACCESS",
		Extra:      map[string]any{"status": "provisioned"},
	}); err != nil {
		return fmt.Errorf("finance-user: the audit row: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finance-user: cannot commit: %w", err)
	}
	fmt.Fprintf(stdout, "user_id=%s grant_id=%s\n", userID, grantID)
	return nil
}

// financeUserRevoke deactivates the staff users row and soft deletes the
// live grant in one transaction, then the audit row, and prints only the
// users row id and the grant id. Never a delete.
func financeUserRevoke(ctx context.Context, pool *pgxpool.Pool, stdout io.Writer, getenv func(string) string, platformUserID, role string) error {
	slug := strings.TrimSpace(getenv(envWebOrg))
	if slug == "" {
		return fmt.Errorf("finance-user: %s must name the org slug", envWebOrg)
	}
	resolver, err := authz.NewPgxOrgResolver(pool)
	if err != nil {
		return fmt.Errorf("finance-user: the org resolver could not be built: %w", err)
	}
	orgID, ok, err := resolver.ResolveSlug(ctx, slug)
	if err != nil {
		return fmt.Errorf("finance-user: the org anchor could not be read: %w", err)
	}
	if !ok {
		return errors.New("finance-user: the org anchor does not exist; nothing to revoke")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-user: cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	userID, deactivated, err := identity.DeactivateStaffUser(ctx, tx, platformUserID)
	if err != nil {
		return fmt.Errorf("finance-user: the deactivation: %w", err)
	}
	grantID, revoked, err := identity.RevokeStaffGrant(ctx, tx, orgID, platformUserID, role)
	if err != nil {
		return fmt.Errorf("finance-user: the grant revocation: %w", err)
	}
	if !deactivated && !revoked {
		return errors.New("finance-user: no live staff user or grant held that platform user")
	}
	svc := audit.NewService(audit.NewRepository(pool), slog.Default())
	if err := svc.RecordTx(ctx, tx, audit.SensitiveAccess{
		Action:     "STAFF_REVOKE",
		EntityType: "identity.staff_user",
		EntityID:   userID,
		ActorID:    userID,
		Purpose:    "FINANCE_BOX_ACCESS",
		Extra:      map[string]any{"status": "revoked"},
	}); err != nil {
		return fmt.Errorf("finance-user: the audit row: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finance-user: cannot commit: %w", err)
	}
	fmt.Fprintf(stdout, "user_id=%s grant_id=%s\n", userID, grantID)
	return nil
}

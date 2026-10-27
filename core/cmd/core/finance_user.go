// finance_user.go carries the finance-user role (W2-2): the one-shot the
// principal runs once per named tester on the finance box. One person per
// run, by email, FB Console user id, display name and role, all at run
// time: the command makes sure the org anchor exists (keeping the name it
// already has), writes an ACTIVE users row and a global staff grant so the
// console exchange's gate 6 links the person to their role, and --revoke
// closes what it opened. The revoke names its subject twice, by email and
// by platform id, and takes every live grant that subject holds in the
// org, so the last revocation is the durable one. Every write lands
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
// what makes the four eyes approvals hold two people. No other role is on
// the list: general_manager is the dealer leadership role, and it carries
// the dealer admin and house message surfaces, which no finance tester
// needs on a public box.
var financeUserRoles = []string{
	"financial_controller",
	"payables_receivables",
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
	email := fs.String("email", "", "the person's email, on the sign in allow list on the provision path (required)")
	platformUserID := fs.String("platform-user-id", "", "the person's FB Console user id (required)")
	displayName := fs.String("display-name", "", "the person's display name (required to provision)")
	role := fs.String("role", financeUserDefaultRole, "the finance role to grant")
	revoke := fs.Bool("revoke", false, "deactivate the person and revoke every live grant instead of granting")
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
	// The display name belongs to the provision path: a revoke names the
	// person by email and platform id, and the name is not what it acts on.
	if !*revoke && strings.TrimSpace(*displayName) == "" {
		return errors.New("finance-user: -display-name is required to provision a staff user")
	}
	if !slices.Contains(financeUserRoles, *role) {
		return fmt.Errorf("finance-user: -role must be one of the finance roles: %s", strings.Join(financeUserRoles, ", "))
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
	if *revoke {
		return financeUserRevoke(ctx, pool, stdout, getenv, strings.TrimSpace(*platformUserID), strings.TrimSpace(*email))
	}
	// The sign in allow list is the provision path's rule. A revoke must
	// still work after the email has left the list, because taking the
	// person off the list first is the order an operator cuts access in.
	listed := map[string]bool{}
	for _, a := range exchange.ParseAllowList(getenv(envSigninEmailAddresses)) {
		listed[a] = true
	}
	if !listed[strings.ToLower(strings.TrimSpace(*email))] {
		return fmt.Errorf("finance-user: the email is not on the sign in allow list (%s)", envSigninEmailAddresses)
	}
	return financeUserProvision(ctx, pool, stdout, getenv, strings.TrimSpace(*email), strings.TrimSpace(*platformUserID), strings.TrimSpace(*displayName), *role)
}

// financeUserProvision makes sure the org anchor exists, keeping the name
// it already has, then writes the staff users row and the global grant in
// one transaction, takes the live grants of the roles the provision does
// not name so one entry maps to exactly one live role, then the audit
// row, and prints only the users row id and the grant id. The audit row
// records only a run that wrote the anchor, the user or a grant: an
// identical re-run stands still and says nothing, so the boot time
// reconciler adds no rows of its own.
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

	orgID, _, anchorWritten, err := identity.EnsureOrgAnchor(ctx, tx, slug, financeBoxOrgName)
	if err != nil {
		return fmt.Errorf("finance-user: the org anchor: %w", err)
	}
	userID, userWritten, err := identity.EnsureStaffUser(ctx, tx, orgID, platformUserID, email, displayName)
	if err != nil {
		return fmt.Errorf("finance-user: the staff user: %w", err)
	}
	grantInserted, err := identity.EnsureStaffGrant(ctx, tx, orgID, platformUserID, role, nil)
	if err != nil {
		return fmt.Errorf("finance-user: the staff grant: %w", err)
	}
	// One entry of the finance list maps to exactly one live role: the
	// grants of the roles this provision does not name go in the same
	// transaction, so an edited entry demotes instead of stacking, and the
	// tier 1 role never outlives its entry.
	_, demoted, err := identity.RevokeStaffGrantsExcept(ctx, tx, orgID, platformUserID, role)
	if err != nil {
		return fmt.Errorf("finance-user: the grant demotion: %w", err)
	}
	grantID, ok, err := identity.StaffGrantID(ctx, tx, orgID, platformUserID, role)
	if err != nil || !ok {
		return fmt.Errorf("finance-user: the staff grant was not found after it was ensured")
	}
	if anchorWritten || userWritten || grantInserted || demoted {
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
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finance-user: cannot commit: %w", err)
	}
	fmt.Fprintf(stdout, "user_id=%s grant_id=%s\n", userID, grantID)
	return nil
}

// financeUserRevoke deactivates the staff users row and soft deletes every
// live grant the platform user holds in the org, in one transaction, then
// the audit row, and prints the users row id and the id of every grant it
// revoked. Never a delete, never an empty grant id: a revoke with nothing
// to revoke refuses.
func financeUserRevoke(ctx context.Context, pool *pgxpool.Pool, stdout io.Writer, getenv func(string) string, platformUserID, email string) error {
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

	// The subject: the users row the platform id holds must carry the email
	// the operator named, or the revoke would act on another person. The
	// comparison folds case, because an address is one address however it
	// is spelled.
	userID, rowEmail, found, err := identity.StaffUserByPlatformID(ctx, tx, platformUserID)
	if err != nil {
		return fmt.Errorf("finance-user: the staff user read: %w", err)
	}
	if !found {
		return errors.New("finance-user: no staff user row holds that platform user; nothing to revoke")
	}
	if !strings.EqualFold(strings.TrimSpace(rowEmail), strings.TrimSpace(email)) {
		return errors.New("finance-user: the email does not match the staff user row that platform user holds; refusing to revoke another person")
	}
	// Every live grant goes, not only the role the caller names: a grant
	// left live would come back with the next provision, and the last
	// revocation has to be the durable one.
	grantIDs, revoked, err := identity.RevokeStaffGrants(ctx, tx, orgID, platformUserID)
	if err != nil {
		return fmt.Errorf("finance-user: the grant revocation: %w", err)
	}
	if !revoked {
		return errors.New("finance-user: no live staff grant to revoke")
	}
	if _, _, err := identity.DeactivateStaffUser(ctx, tx, platformUserID); err != nil {
		return fmt.Errorf("finance-user: the deactivation: %w", err)
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
	for _, grantID := range grantIDs {
		fmt.Fprintf(stdout, "user_id=%s grant_id=%s\n", userID, grantID)
	}
	return nil
}

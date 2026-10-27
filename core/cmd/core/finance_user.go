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
// pair. The box kind names themselves live with the guard, in boxkind.go,
// so the command and the guard read one pair of names.
const (
	envAppEnv         = "APP_ENV"
	envWebOrg         = "HH_WEB_ORG"
	financeBoxOrgName = "Finance Box"
	// envFinanceUsers is the one name the box's finance user list lives in.
	// The entrypoint derives the sign in allow list from it and applies it
	// at boot, so the list is the one source of truth for the people on the
	// box.
	envFinanceUsers = "HH_FINANCE_USERS"
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
	applyFromEnv := fs.Bool("apply-from-env", false, "apply the box's finance user list from HH_FINANCE_USERS: provision every entry and revoke every finance person the list left out, in one transaction")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("finance-user: unexpected arguments %q", strings.Join(fs.Args(), " "))
	}
	if *applyFromEnv {
		// The list is the person here: a name of its own would be a second
		// source of truth, so the apply path takes no name of its own.
		var others []string
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "apply-from-env" {
				others = append(others, "-"+f.Name)
			}
		})
		if len(others) > 0 {
			return fmt.Errorf("finance-user: --apply-from-env applies the list in %s and takes no name of its own: %s does not belong to it", envFinanceUsers, strings.Join(others, ", "))
		}
	} else {
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
	if *applyFromEnv {
		return financeUserApplyFromEnv(ctx, pool, stdout, getenv)
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
// one transaction, then the audit row, and prints only the users row id
// and the grant id.
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

	orgID, _, _, err := identity.EnsureOrgAnchor(ctx, tx, slug, financeBoxOrgName)
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

// financeUserEntry is one person as the box's list carries them: the email,
// the platform user id, the display name and the finance role.
type financeUserEntry struct {
	email          string
	platformUserID string
	displayName    string
	role           string
}

// parseFinanceUserEntries reads the box's finance user list: the entries are
// separated by ';', each one 'email|platform_user_id|display name|role'. A
// bad entry refuses with one line naming the entry by its email alone, never
// the platform id, the display name or the role it carries.
func parseFinanceUserEntries(raw string) ([]financeUserEntry, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("finance-user: %s holds no entry: the box's finance users are applied from it, and there is nothing to apply without it", envFinanceUsers)
	}
	var out []financeUserEntry
	for i, part := range strings.Split(raw, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		fields := strings.Split(part, "|")
		if len(fields) != 4 {
			return nil, fmt.Errorf("finance-user: the entry for %s does not have four fields (email|platform_user_id|display name|role)", financeUserEntryName(fields, i+1))
		}
		e := financeUserEntry{
			email:          strings.TrimSpace(fields[0]),
			platformUserID: strings.TrimSpace(fields[1]),
			displayName:    strings.TrimSpace(fields[2]),
			role:           strings.TrimSpace(fields[3]),
		}
		if e.email == "" || e.platformUserID == "" || e.displayName == "" || e.role == "" {
			return nil, fmt.Errorf("finance-user: the entry for %s has an empty field (email|platform_user_id|display name|role)", financeUserEntryName(fields, i+1))
		}
		if !slices.Contains(financeUserRoles, e.role) {
			return nil, fmt.Errorf("finance-user: the entry for %s has a role off the finance roles list: %s", e.email, strings.Join(financeUserRoles, ", "))
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("finance-user: %s holds no entry: the box's finance users are applied from it, and there is nothing to apply without it", envFinanceUsers)
	}
	return out, nil
}

// financeUserEntryName is how a bad entry is named: by the email it carries,
// and by its position only when it carries no email to name it by. It never
// carries the platform id, the display name or the role.
func financeUserEntryName(fields []string, position int) string {
	if len(fields) > 0 {
		if email := strings.TrimSpace(fields[0]); email != "" {
			return email
		}
	}
	return fmt.Sprintf("the entry at position %d", position)
}

// financeUserApplyFromEnv applies the box's finance user list: every entry is
// provisioned the way the command already provisions one person, and every
// finance person the list left out is revoked, all of it in ONE transaction,
// so the box is the list or the box is as it was. It prints ids and counts
// only, never an email.
//
// The rules are the command's rules: the finance box alone, a role off the
// finance list refuses, an email another platform subject holds refuses, and
// a person already linked with the role the list gives is left alone.
func financeUserApplyFromEnv(ctx context.Context, pool *pgxpool.Pool, stdout io.Writer, getenv func(string) string) error {
	entries, err := parseFinanceUserEntries(getenv(envFinanceUsers))
	if err != nil {
		return err
	}
	slug := strings.TrimSpace(getenv(envWebOrg))
	if slug == "" {
		return fmt.Errorf("finance-user: %s must name the org slug", envWebOrg)
	}
	// The sign in allow list is the provision path's rule, and a person the
	// list names must be a person who can sign in.
	listed := map[string]bool{}
	for _, a := range exchange.ParseAllowList(getenv(envSigninEmailAddresses)) {
		listed[a] = true
	}
	if len(listed) == 0 {
		return fmt.Errorf("finance-user: %s is not set: a person the list names must be on the sign in allow list, and the list is empty", envSigninEmailAddresses)
	}
	for _, e := range entries {
		if !listed[strings.ToLower(e.email)] {
			return fmt.Errorf("finance-user: the entry for %s is not on the sign in allow list (%s)", e.email, envSigninEmailAddresses)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-user: cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orgID, _, _, err := identity.EnsureOrgAnchor(ctx, tx, slug, financeBoxOrgName)
	if err != nil {
		return fmt.Errorf("finance-user: the org anchor: %w", err)
	}
	svc := audit.NewService(audit.NewRepository(pool), slog.Default())

	// Every entry, provisioned the way the command provisions one person: the
	// person is linked, the role is granted, and the audit row says so.
	type appliedPerson struct {
		userID  string
		grantID string
	}
	var applied []appliedPerson
	for _, e := range entries {
		userID, written, err := identity.EnsureStaffUser(ctx, tx, orgID, e.platformUserID, e.email, e.displayName)
		if errors.Is(err, identity.ErrStaffEmailTaken) {
			return fmt.Errorf("finance-user: the entry for %s holds an email that belongs to another platform user", e.email)
		}
		if err != nil {
			return fmt.Errorf("finance-user: the staff user: %w", err)
		}
		inserted, err := identity.EnsureStaffGrant(ctx, tx, orgID, e.platformUserID, e.role, nil)
		if err != nil {
			return fmt.Errorf("finance-user: the staff grant: %w", err)
		}
		grantID, ok, err := identity.StaffGrantID(ctx, tx, orgID, e.platformUserID, e.role)
		if err != nil || !ok {
			return errors.New("finance-user: the staff grant was not found after it was ensured")
		}
		// The audit row says the person's access was established, so it is
		// written when the row or the grant changed and not when the person
		// was already linked: a boot with the same list changes nothing.
		if written || inserted {
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
		applied = append(applied, appliedPerson{userID: userID, grantID: grantID})
	}

	// Every finance person the list left out, revoked the way the command
	// revokes one person: the grants go as tombstones, the person is
	// deactivated, the audit row says so. Never a delete.
	keep := map[string]bool{}
	for _, e := range entries {
		keep[e.platformUserID] = true
	}
	subjects, err := identity.StaffSubjectsWithLiveGrantsInRoles(ctx, tx, orgID, financeUserRoles)
	if err != nil {
		return fmt.Errorf("finance-user: the finance people read: %w", err)
	}
	type revokedPerson struct {
		userID   string
		grantIDs []string
	}
	var revoked []revokedPerson
	for _, s := range subjects {
		if keep[s.PlatformUserID] {
			continue
		}
		userID, _, found, err := identity.StaffUserByPlatformID(ctx, tx, s.PlatformUserID)
		if err != nil {
			return fmt.Errorf("finance-user: the staff user read: %w", err)
		}
		if !found {
			continue
		}
		grantIDs, ok, err := identity.RevokeStaffGrants(ctx, tx, orgID, s.PlatformUserID)
		if err != nil {
			return fmt.Errorf("finance-user: the grant revocation: %w", err)
		}
		if !ok {
			continue
		}
		if _, _, err := identity.DeactivateStaffUser(ctx, tx, s.PlatformUserID); err != nil {
			return fmt.Errorf("finance-user: the deactivation: %w", err)
		}
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
		revoked = append(revoked, revokedPerson{userID: userID, grantIDs: grantIDs})
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finance-user: cannot commit: %w", err)
	}
	for _, p := range applied {
		fmt.Fprintf(stdout, "user_id=%s grant_id=%s\n", p.userID, p.grantID)
	}
	for _, p := range revoked {
		for _, grantID := range p.grantIDs {
			fmt.Fprintf(stdout, "revoked user_id=%s grant_id=%s\n", p.userID, grantID)
		}
	}
	fmt.Fprintf(stdout, "applied=%d revoked=%d\n", len(applied), len(revoked))
	return nil
}

package testkit

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// Fixture identifiers. Every value is invented and patterned: the 3ef1 UUID
// prefix block, fixture- slugs, FX- visible numbers, reserved-domain
// logins under the .test top level (reserved by RFC 6761, never routable).
// No repository data, no real names, emails or addresses. Identifiers are
// constants, not generated, so the builder is idempotent per run, the
// cleanup deletes exactly these rows, and sweeps and FIXTURES.md cite them
// by name.
const (
	// Org anchors (0213 org table), lowercase slugs per the pinned shape.
	OrgNorthSlug = "fixture-north"
	OrgSouthSlug = "fixture-south"
	OrgNorthID   = "3ef10000-0000-4000-8000-000000000001"
	OrgSouthID   = "3ef10000-0000-4000-8000-000000000002"

	// Branch locations (0003 locations, org stamped per 0213).
	BranchNorthAID   = "3ef10000-0000-4000-8000-000000000011"
	BranchNorthBID   = "3ef10000-0000-4000-8000-000000000012"
	BranchSouthAID   = "3ef10000-0000-4000-8000-000000000013"
	BranchNorthACode = "FXNA"
	BranchNorthBCode = "FXNB"
	BranchSouthACode = "FXSA"

	// Contractor accounts (0006 customers). The tenant of a tenant bearer
	// is the contractor account UUID; alpha and beta share the north org
	// (the isolation legs), gamma sits in the south org (the compose leg).
	TenantAlphaID = "3ef10000-0000-4000-8000-000000000021"
	TenantBetaID  = "3ef10000-0000-4000-8000-000000000022"
	TenantGammaID = "3ef10000-0000-4000-8000-000000000023"

	// Staff principals: JWT subjects and users.platform_user_id values.
	SubInsideSales  = "fxstaff-inside-sales"    // tier 3, branch scope, branch A
	SubSalesManager = "fxstaff-sales-manager"   // tier 2, branch scope, branch A
	SubGeneralMgr   = "fxstaff-general-manager" // tier 1, global
	SubTwoOrg       = "fxstaff-two-org"         // grants in both orgs
	SubRevoked      = "fxstaff-revoked"         // tombstoned grant
	SubInert        = "fxstaff-inert"           // branch-id rule violation row, the 503 data-defect leg
	SubDriver       = "fxstaff-driver"          // driver role plus profile, ceiling legs
	SubSuspended    = "fxstaff-suspended"       // users row SUSPENDED, active grant: the account-status 401 leg

	// users rows for the staff principals (id, then the reserved-domain
	// login pattern).
	UserInsideSalesID = "3ef10000-0000-4000-8000-000000000031"
	UserSalesMgrID    = "3ef10000-0000-4000-8000-000000000032"
	UserGeneralMgrID  = "3ef10000-0000-4000-8000-000000000033"
	UserTwoOrgID      = "3ef10000-0000-4000-8000-000000000034"
	UserRevokedID     = "3ef10000-0000-4000-8000-000000000035"
	UserInertID       = "3ef10000-0000-4000-8000-000000000036"
	UserDriverID      = "3ef10000-0000-4000-8000-000000000037"
	UserSuspendedID   = "3ef10000-0000-4000-8000-000000000038"

	// Portal principals: JWT subjects and portal_users.platform_user_id.
	// Canonical roles cover the whole capability span: account_admin carries
	// the full set, field_crew the minimal set, and buyer_no_pay, buyer and
	// ap_finance cover the middle of the catalogue (0160 CHECK set). The
	// buyer_no_pay principal is the image of the hhpro pm role through
	// hhpro_contractor_role_map, and the alpha admin is the owner image, so
	// the mapping rows are read, never bypassed.
	SubPortalAdminAlpha      = "fxportal-admin-alpha"
	SubPortalCrewAlpha       = "fxportal-crew-alpha"
	SubPortalBuyerNoPayAlpha = "fxportal-buynopay-alpha"
	SubPortalAdminBeta       = "fxportal-admin-beta"
	SubPortalCrewBeta        = "fxportal-crew-beta"
	SubPortalBuyerBeta       = "fxportal-buyer-beta"
	SubPortalAdminGamma      = "fxportal-admin-gamma"
	SubPortalApGamma         = "fxportal-ap-gamma"
	SubPortalInactiveAlpha   = "fxportal-inactive-alpha" // status inactive row: the deactivated portal 401 leg

	PortalAdminAlphaID      = "3ef10000-0000-4000-8000-000000000041"
	PortalCrewAlphaID       = "3ef10000-0000-4000-8000-000000000042"
	PortalBuyerNoPayAlphaID = "3ef10000-0000-4000-8000-000000000046"
	PortalAdminBetaID       = "3ef10000-0000-4000-8000-000000000043"
	PortalCrewBetaID        = "3ef10000-0000-4000-8000-000000000044"
	PortalBuyerBetaID       = "3ef10000-0000-4000-8000-000000000047"
	PortalAdminGammaID      = "3ef10000-0000-4000-8000-000000000045"
	PortalApGammaID         = "3ef10000-0000-4000-8000-000000000048"
	PortalInactiveAlphaID   = "3ef10000-0000-4000-8000-000000000049"

	// Reference rows the tenant data hangs from.
	ProductID = "3ef10000-0000-4000-8000-000000000051"
	TruckID   = "3ef10000-0000-4000-8000-000000000061"

	// Orders (0008, org and tenant stamped per 0213). One per lifecycle
	// status the lifecycle sweep drives, a money-attached cancellable for
	// the 409 leg, and the cross-branch row (branch B) for the carried
	// cross-branch refusal.
	OrderAlphaDraftID   = "3ef10000-0000-4000-8000-000000000071"
	OrderAlphaConfID    = "3ef10000-0000-4000-8000-000000000072"
	OrderAlphaPickID    = "3ef10000-0000-4000-8000-000000000073"
	OrderAlphaReadyID   = "3ef10000-0000-4000-8000-000000000074"
	OrderAlphaMoneyID   = "3ef10000-0000-4000-8000-000000000075"
	OrderAlphaXBranchID = "3ef10000-0000-4000-8000-000000000076"
	OrderBetaDraftID    = "3ef10000-0000-4000-8000-000000000077"
	OrderBetaConfID     = "3ef10000-0000-4000-8000-000000000078"
	OrderGammaDraftID   = "3ef10000-0000-4000-8000-000000000079"

	OrderAlphaDraftNo   = "FXORD-A-DRAFT"
	OrderAlphaConfNo    = "FXORD-A-CONFIRMED"
	OrderAlphaPickNo    = "FXORD-A-PICKING"
	OrderAlphaReadyNo   = "FXORD-A-READY"
	OrderAlphaMoneyNo   = "FXORD-A-MONEY"
	OrderAlphaXBranchNo = "FXORD-A-XBRANCH"
	OrderBetaDraftNo    = "FXORD-B-DRAFT"
	OrderBetaConfNo     = "FXORD-B-CONFIRMED"
	OrderGammaDraftNo   = "FXORD-G-DRAFT"

	// One line per order, named so the return-request lines can cite the
	// exact line they hang from.
	OrderLineAlphaDraftID   = "3ef10000-0000-4000-8000-000000000081"
	OrderLineAlphaConfID    = "3ef10000-0000-4000-8000-000000000082"
	OrderLineAlphaPickID    = "3ef10000-0000-4000-8000-000000000083"
	OrderLineAlphaReadyID   = "3ef10000-0000-4000-8000-000000000084"
	OrderLineAlphaMoneyID   = "3ef10000-0000-4000-8000-000000000085"
	OrderLineAlphaXBranchID = "3ef10000-0000-4000-8000-000000000086"
	OrderLineBetaDraftID    = "3ef10000-0000-4000-8000-000000000087"
	OrderLineBetaConfID     = "3ef10000-0000-4000-8000-000000000088"
	OrderLineGammaDraftID   = "3ef10000-0000-4000-8000-000000000089"

	// Quotes, invoices, payments, payment methods, projects, procurement,
	// returns: one pair across alpha and beta so every list route has a
	// foreign row to exclude, gamma keeps its order for the compose leg.
	QuoteAlphaID   = "3ef10000-0000-4000-8000-00000000a001"
	QuoteBetaID    = "3ef10000-0000-4000-8000-00000000a002"
	QuoteGammaID   = "3ef10000-0000-4000-8000-00000000a003"
	QuoteAlphaLine = "3ef10000-0000-4000-8000-00000000a011"
	QuoteBetaLine  = "3ef10000-0000-4000-8000-00000000a012"
	QuoteGammaLine = "3ef10000-0000-4000-8000-00000000a013"

	InvoiceAlphaID   = "3ef10000-0000-4000-8000-00000000b001"
	InvoiceBetaID    = "3ef10000-0000-4000-8000-00000000b002"
	InvoiceAlphaLine = "3ef10000-0000-4000-8000-00000000b011"
	InvoiceBetaLine  = "3ef10000-0000-4000-8000-00000000b012"

	PaymentAlphaID = "3ef10000-0000-4000-8000-00000000c001"
	PaymentBetaID  = "3ef10000-0000-4000-8000-00000000c002"

	PayMethodAlphaID = "3ef10000-0000-4000-8000-00000000d001"
	PayMethodBetaID  = "3ef10000-0000-4000-8000-00000000d002"

	ProjectAlphaID = "3ef10000-0000-4000-8000-00000000e001"
	ProjectBetaID  = "3ef10000-0000-4000-8000-00000000e002"

	ProcureAlphaID   = "3ef10000-0000-4000-8000-00000000f001"
	ProcureBetaID    = "3ef10000-0000-4000-8000-00000000f002"
	ProcureAlphaLine = "3ef10000-0000-4000-8000-00000000f011"
	ProcureBetaLine  = "3ef10000-0000-4000-8000-00000000f012"

	ReturnAlphaID   = "3ef10000-0000-4000-8000-000000001001"
	ReturnBetaID    = "3ef10000-0000-4000-8000-000000001002"
	ReturnAlphaLine = "3ef10000-0000-4000-8000-000000001011"
	ReturnBetaLine  = "3ef10000-0000-4000-8000-000000001012"

	DriverProfileID = "3ef10000-0000-4000-8000-000000001101"

	// staff_role_grant rows (0215). Role ids are the seeded catalogue
	// rows; the builder never inserts staff_role rows itself.
	GrantInsideSalesID = "3ef10000-0000-4000-8000-000000001201"
	GrantSalesMgrID    = "3ef10000-0000-4000-8000-000000001202"
	GrantGeneralMgrID  = "3ef10000-0000-4000-8000-000000001203"
	GrantTwoOrgNorthID = "3ef10000-0000-4000-8000-000000001204"
	GrantTwoOrgSouthID = "3ef10000-0000-4000-8000-000000001205"
	GrantRevokedID     = "3ef10000-0000-4000-8000-000000001206"
	GrantInertID       = "3ef10000-0000-4000-8000-000000001207"
	GrantDriverID      = "3ef10000-0000-4000-8000-000000001208"
	GrantSuspendedID   = "3ef10000-0000-4000-8000-000000001209"

	// Catalogue role ids the grants reference (seeded by 0215).
	RoleInsideSales  = "inside_sales"
	RoleSalesManager = "sales_manager"
	RoleGeneralMgr   = "general_manager"
	RoleDriver       = "driver"
	RoleYardTeam     = "yard_team"
)

// DB is the least the builder needs from a database handle: run a
// statement, read a scalar count. Both a database/sql handle and a pgx
// pool satisfy it through this package's adapters (SQLDB, PgxPool), so the
// B5 harness points the builder at whatever it holds: a fresh scratch
// database with migrations applied, never the shared proof databases.
type DB interface {
	// Exec runs one statement. Idempotent statements make re-running the
	// builder per sweep run safe.
	Exec(ctx context.Context, query string, args ...any) error
	// Count reads one integer scalar.
	Count(ctx context.Context, query string, args ...any) (int, error)
}

// SQLDB adapts a database/sql handle to DB.
func SQLDB(db *sql.DB) DB { return sqlDB{db: db} }

type sqlDB struct{ db *sql.DB }

// Exec implements DB.
func (d sqlDB) Exec(ctx context.Context, query string, args ...any) error {
	_, err := d.db.ExecContext(ctx, query, args...)
	return err
}

// Count implements DB.
func (d sqlDB) Count(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// Build applies the full synthetic fixture set. Idempotent: every insert
// carries a fixed id and ON CONFLICT DO NOTHING, so a second run in the
// same database is a no-op and count deltas stay meaningful. Fixture rows
// live beside any seed data without touching it; Cleanup removes exactly
// the fixture rows.
func Build(ctx context.Context, db DB) error {
	for _, stmt := range buildStatements() {
		if err := db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("testkit fixture: %w (statement %.60s...)", err, stmt)
		}
	}
	return nil
}

// buildStatements returns the fixture SQL in foreign-key order. Values are
// the package constants; org_id and tenant_id columns are stamped per the
// 0213 table lists; carried NOT NULL columns that carry no fixture meaning
// get explicit synthetic values, never defaults that might shift under the
// running schema.
func buildStatements() []string {
	return []string{
		// Org anchors.
		fmt.Sprintf(`INSERT INTO org (id, slug, name) VALUES
			('%s', '%s', 'Fixture Yard North'),
			('%s', '%s', 'Fixture Yard South')
			ON CONFLICT (id) DO NOTHING`, OrgNorthID, OrgNorthSlug, OrgSouthID, OrgSouthSlug),

		// Branch locations: two in the north (the cross-branch leg), one
		// in the south.
		fmt.Sprintf(`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id) VALUES
			('%s', '%s', NULL, 'BRANCH', '%s', 'Fixture North A', '{}', '%s'),
			('%s', '%s', NULL, 'BRANCH', '%s', 'Fixture North B', '{}', '%s'),
			('%s', '%s', NULL, 'BRANCH', '%s', 'Fixture South A', '{}', '%s')
			ON CONFLICT (id) DO NOTHING`,
			BranchNorthAID, BranchNorthAID, BranchNorthACode, OrgNorthID,
			BranchNorthBID, BranchNorthBID, BranchNorthBCode, OrgNorthID,
			BranchSouthAID, BranchSouthAID, BranchSouthACode, OrgSouthID),

		// Fleet truck for the driver profile FK. dispatch_trucks is not in
		// the 0213 org_id list (it carries seq and tombstone only), so the
		// row is scoped through its branch like the dispatch registry
		// reads it.
		fmt.Sprintf(`INSERT INTO dispatch_trucks (id, branch_id, code, name, truck_type) VALUES
			('%s', '%s', 'FX-TRK-1', 'Fixture Truck One', 'FLATBED')
			ON CONFLICT (id) DO NOTHING`, TruckID, BranchNorthAID),

		// Platform-linked users rows for the staff principals. The
		// suspended row carries an active grant on purpose: the
		// account-status leg decides at authority resolution, not at the
		// org gate.
		fmt.Sprintf(`INSERT INTO users (id, email, display_name, status, platform_user_id, org_id) VALUES
			('%s', 'fxstaff.inside.sales@fixture.test',  'Fixture Inside Sales',  'ACTIVE',   '%s', '%s'),
			('%s', 'fxstaff.sales.manager@fixture.test', 'Fixture Sales Manager', 'ACTIVE',   '%s', '%s'),
			('%s', 'fxstaff.general.manager@fixture.test','Fixture General Manager','ACTIVE', '%s', '%s'),
			('%s', 'fxstaff.two.org@fixture.test',       'Fixture Two Org Staff', 'ACTIVE',   '%s', '%s'),
			('%s', 'fxstaff.revoked@fixture.test',       'Fixture Revoked Staff', 'ACTIVE',   '%s', '%s'),
			('%s', 'fxstaff.inert@fixture.test',         'Fixture Inert Grant',   'ACTIVE',   '%s', '%s'),
			('%s', 'fxstaff.driver@fixture.test',        'Fixture Driver',        'ACTIVE',   '%s', '%s'),
			('%s', 'fxstaff.suspended@fixture.test',     'Fixture Suspended Staff','SUSPENDED','%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			UserInsideSalesID, SubInsideSales, OrgNorthID,
			UserSalesMgrID, SubSalesManager, OrgNorthID,
			UserGeneralMgrID, SubGeneralMgr, OrgNorthID,
			UserTwoOrgID, SubTwoOrg, OrgNorthID,
			UserRevokedID, SubRevoked, OrgNorthID,
			UserInertID, SubInert, OrgNorthID,
			UserDriverID, SubDriver, OrgNorthID,
			UserSuspendedID, SubSuspended, OrgNorthID),

		// staff_role_grant: every tier, the branch-id rule both ways, the
		// tombstone, the two-org principal, and the suspended principal's
		// active grant. The inert row (branch-scope role, NULL branch_id)
		// is legal SQL and stays exactly as it is: per the B3 review
		// arbitration Q-1 the branch-id rule violation is a provisioning
		// data defect the identity layer answers with the 503 family,
		// never an inert grant.
		fmt.Sprintf(`INSERT INTO staff_role_grant (id, platform_user_id, role_id, org_id, branch_id) VALUES
			('%s', '%s', '%s', '%s', '%s'),
			('%s', '%s', '%s', '%s', '%s'),
			('%s', '%s', '%s', '%s', NULL),
			('%s', '%s', '%s', '%s', '%s'),
			('%s', '%s', '%s', '%s', '%s'),
			('%s', '%s', '%s', '%s', '%s'),
			('%s', '%s', '%s', '%s', NULL),
			('%s', '%s', '%s', '%s', '%s'),
			('%s', '%s', '%s', '%s', NULL)
			ON CONFLICT (id) DO NOTHING`,
			GrantInsideSalesID, SubInsideSales, RoleInsideSales, OrgNorthID, BranchNorthAID,
			GrantSalesMgrID, SubSalesManager, RoleSalesManager, OrgNorthID, BranchNorthAID,
			GrantGeneralMgrID, SubGeneralMgr, RoleGeneralMgr, OrgNorthID,
			GrantTwoOrgNorthID, SubTwoOrg, RoleInsideSales, OrgNorthID, BranchNorthAID,
			GrantTwoOrgSouthID, SubTwoOrg, RoleInsideSales, OrgSouthID, BranchSouthAID,
			GrantRevokedID, SubRevoked, RoleSalesManager, OrgNorthID, BranchNorthAID,
			GrantInertID, SubInert, RoleYardTeam, OrgNorthID,
			GrantDriverID, SubDriver, RoleDriver, OrgNorthID, BranchNorthAID,
			GrantSuspendedID, SubSuspended, RoleGeneralMgr, OrgNorthID),

		// The tombstone on the revoked grant: the same statement shape as
		// the insert above keeps this a fixed-id update.
		fmt.Sprintf(`UPDATE staff_role_grant SET deleted_at = CURRENT_TIMESTAMP - INTERVAL '1 hour'
			WHERE id = '%s' AND deleted_at IS NULL`, GrantRevokedID),

		// Driver profile for the ceiling legs: invented licence values on
		// the fixture truck.
		fmt.Sprintf(`INSERT INTO driver_profile (id, platform_user_id, org_id, truck_id, licence_number, licence_class, licence_expiry, is_active) VALUES
			('%s', '%s', '%s', '%s', 'FX-LIC-0001', 'FXG', CURRENT_DATE + 365, TRUE)
			ON CONFLICT (id) DO NOTHING`,
			DriverProfileID, SubDriver, OrgNorthID, TruckID),

		// Carried branch grants: the cross-branch leg's granted branch is
		// A; the branch B row is absent on purpose, that absence is the
		// fixture.
		fmt.Sprintf(`INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES
			('%s', '%s', '%s'),
			('%s', '%s', '%s')
			ON CONFLICT (user_id, branch_id) DO NOTHING`,
			UserInsideSalesID, BranchNorthAID, OrgNorthID,
			UserSalesMgrID, BranchNorthAID, OrgNorthID),

		// Contractor accounts: the tenant rows. Alpha and beta share the
		// north org; gamma anchors the south org compose leg.
		fmt.Sprintf(`INSERT INTO customers (id, account_no, name, customer_type, home_branch_id, org_id) VALUES
			('%s', 'FXACC-NORTH-1', 'Fixture Contractor Alpha', 'CONTRACTOR', '%s', '%s'),
			('%s', 'FXACC-NORTH-2', 'Fixture Contractor Beta',  'CONTRACTOR', '%s', '%s'),
			('%s', 'FXACC-SOUTH-1', 'Fixture Contractor Gamma', 'CONTRACTOR', '%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			TenantAlphaID, BranchNorthAID, OrgNorthID,
			TenantBetaID, BranchNorthAID, OrgNorthID,
			TenantGammaID, BranchSouthAID, OrgSouthID),

		// Portal principals across the tenants: the whole canonical role
		// set of the 0160 CHECK (account_admin, buyer_no_pay, buyer,
		// ap_finance, field_crew), platform_user_id and tenant_id stamped.
		// The alpha admin is the hhpro owner image and the alpha
		// buyer_no_pay row the hhpro pm image, so the
		// hhpro_contractor_role_map rows (seeded by 0215) are read, never
		// bypassed.
		fmt.Sprintf(`INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status, platform_user_id, tenant_id) VALUES
			('%s', '%s', 'fxportal.admin.alpha@fixture.test',     'Fixture Portal Admin Alpha',     'fx-not-a-hash-1', 'account_admin', 'active', '%s', '%s'),
			('%s', '%s', 'fxportal.crew.alpha@fixture.test',      'Fixture Portal Crew Alpha',      'fx-not-a-hash-2', 'field_crew',    'active', '%s', '%s'),
			('%s', '%s', 'fxportal.buynopay.alpha@fixture.test',  'Fixture Portal Buyer NoPay Alpha', 'fx-not-a-hash-3', 'buyer_no_pay', 'active', '%s', '%s'),
			('%s', '%s', 'fxportal.admin.beta@fixture.test',      'Fixture Portal Admin Beta',      'fx-not-a-hash-4', 'account_admin', 'active', '%s', '%s'),
			('%s', '%s', 'fxportal.crew.beta@fixture.test',       'Fixture Portal Crew Beta',       'fx-not-a-hash-5', 'field_crew',    'active', '%s', '%s'),
			('%s', '%s', 'fxportal.buyer.beta@fixture.test',      'Fixture Portal Buyer Beta',      'fx-not-a-hash-6', 'buyer',         'active', '%s', '%s'),
			('%s', '%s', 'fxportal.admin.gamma@fixture.test',     'Fixture Portal Admin Gamma',     'fx-not-a-hash-7', 'account_admin', 'active',   '%s', '%s'),
			('%s', '%s', 'fxportal.ap.gamma@fixture.test',        'Fixture Portal AP Gamma',        'fx-not-a-hash-8', 'ap_finance',    'active',   '%s', '%s'),
			('%s', '%s', 'fxportal.inactive.alpha@fixture.test',  'Fixture Portal Inactive Alpha',  'fx-not-a-hash-9', 'account_admin', 'inactive', '%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			PortalAdminAlphaID, TenantAlphaID, SubPortalAdminAlpha, TenantAlphaID,
			PortalCrewAlphaID, TenantAlphaID, SubPortalCrewAlpha, TenantAlphaID,
			PortalBuyerNoPayAlphaID, TenantAlphaID, SubPortalBuyerNoPayAlpha, TenantAlphaID,
			PortalAdminBetaID, TenantBetaID, SubPortalAdminBeta, TenantBetaID,
			PortalCrewBetaID, TenantBetaID, SubPortalCrewBeta, TenantBetaID,
			PortalBuyerBetaID, TenantBetaID, SubPortalBuyerBeta, TenantBetaID,
			PortalAdminGammaID, TenantGammaID, SubPortalAdminGamma, TenantGammaID,
			PortalApGammaID, TenantGammaID, SubPortalApGamma, TenantGammaID,
			PortalInactiveAlphaID, TenantAlphaID, SubPortalInactiveAlpha, TenantAlphaID),

		// One catalog product for the line tables that require it.
		fmt.Sprintf(`INSERT INTO products (id, sku, name, base_uom, price_uom, org_id) VALUES
			('%s', 'FX-SKU-0001', 'Fixture Product One', 'EA', 'EA', '%s')
			ON CONFLICT (id) DO NOTHING`, ProductID, OrgNorthID),

		// Orders: the lifecycle set for tenant alpha (one per driven
		// status, the money-attached cancellable, the cross-branch row in
		// branch B), plus a draft and confirmed pair for beta and a draft
		// for gamma.
		fmt.Sprintf(`INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, deposit_required_cents, org_id, tenant_id) VALUES
			('%s', '%s', '%s', '%s', 'DRAFT',     'PICKUP', 0,    '%s', '%s'),
			('%s', '%s', '%s', '%s', 'CONFIRMED', 'PICKUP', 0,    '%s', '%s'),
			('%s', '%s', '%s', '%s', 'PICKING',   'PICKUP', 0,    '%s', '%s'),
			('%s', '%s', '%s', '%s', 'READY',     'PICKUP', 0,    '%s', '%s'),
			('%s', '%s', '%s', '%s', 'CONFIRMED', 'PICKUP', 5000, '%s', '%s'),
			('%s', '%s', '%s', '%s', 'DRAFT',     'PICKUP', 0,    '%s', '%s'),
			('%s', '%s', '%s', '%s', 'DRAFT',     'PICKUP', 0,    '%s', '%s'),
			('%s', '%s', '%s', '%s', 'CONFIRMED', 'PICKUP', 0,    '%s', '%s'),
			('%s', '%s', '%s', '%s', 'DRAFT',     'PICKUP', 0,    '%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			OrderAlphaDraftID, OrderAlphaDraftNo, TenantAlphaID, BranchNorthAID, OrgNorthID, TenantAlphaID,
			OrderAlphaConfID, OrderAlphaConfNo, TenantAlphaID, BranchNorthAID, OrgNorthID, TenantAlphaID,
			OrderAlphaPickID, OrderAlphaPickNo, TenantAlphaID, BranchNorthAID, OrgNorthID, TenantAlphaID,
			OrderAlphaReadyID, OrderAlphaReadyNo, TenantAlphaID, BranchNorthAID, OrgNorthID, TenantAlphaID,
			OrderAlphaMoneyID, OrderAlphaMoneyNo, TenantAlphaID, BranchNorthAID, OrgNorthID, TenantAlphaID,
			OrderAlphaXBranchID, OrderAlphaXBranchNo, TenantAlphaID, BranchNorthBID, OrgNorthID, TenantAlphaID,
			OrderBetaDraftID, OrderBetaDraftNo, TenantBetaID, BranchNorthAID, OrgNorthID, TenantBetaID,
			OrderBetaConfID, OrderBetaConfNo, TenantBetaID, BranchNorthAID, OrgNorthID, TenantBetaID,
			OrderGammaDraftID, OrderGammaDraftNo, TenantGammaID, BranchSouthAID, OrgSouthID, TenantGammaID),

		// One line per order, EA throughout, org and tenant stamped.
		fmt.Sprintf(`INSERT INTO order_lines (id, order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, org_id, tenant_id) VALUES
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			OrderLineAlphaDraftID, OrderAlphaDraftID, ProductID, OrgNorthID, TenantAlphaID,
			OrderLineAlphaConfID, OrderAlphaConfID, ProductID, OrgNorthID, TenantAlphaID,
			OrderLineAlphaPickID, OrderAlphaPickID, ProductID, OrgNorthID, TenantAlphaID,
			OrderLineAlphaReadyID, OrderAlphaReadyID, ProductID, OrgNorthID, TenantAlphaID,
			OrderLineAlphaMoneyID, OrderAlphaMoneyID, ProductID, OrgNorthID, TenantAlphaID,
			OrderLineAlphaXBranchID, OrderAlphaXBranchID, ProductID, OrgNorthID, TenantAlphaID,
			OrderLineBetaDraftID, OrderBetaDraftID, ProductID, OrgNorthID, TenantBetaID,
			OrderLineBetaConfID, OrderBetaConfID, ProductID, OrgNorthID, TenantBetaID,
			OrderLineGammaDraftID, OrderGammaDraftID, ProductID, OrgSouthID, TenantGammaID),

		// Quotes, one per tenant.
		fmt.Sprintf(`INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, org_id) VALUES
			('%s', 'FXQTE-A-1', '%s', '%s', 'DRAFT', '%s'),
			('%s', 'FXQTE-B-1', '%s', '%s', 'DRAFT', '%s'),
			('%s', 'FXQTE-G-1', '%s', '%s', 'DRAFT', '%s')
			ON CONFLICT (id) DO NOTHING`,
			QuoteAlphaID, TenantAlphaID, BranchNorthAID, OrgNorthID,
			QuoteBetaID, TenantBetaID, BranchNorthAID, OrgNorthID,
			QuoteGammaID, TenantGammaID, BranchSouthAID, OrgSouthID),

		fmt.Sprintf(`INSERT INTO quote_lines (id, quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, org_id) VALUES
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s')
			ON CONFLICT (id) DO NOTHING`,
			QuoteAlphaLine, QuoteAlphaID, ProductID, OrgNorthID,
			QuoteBetaLine, QuoteBetaID, ProductID, OrgNorthID,
			QuoteGammaLine, QuoteGammaID, ProductID, OrgSouthID),

		// Invoices and lines (carried due-date discipline via CURRENT_DATE
		// arithmetic, never a literal date).
		fmt.Sprintf(`INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, due_date, org_id) VALUES
			('%s', 'FXINV-A-1', '%s', '%s', 'DRAFT', CURRENT_DATE + 30, '%s'),
			('%s', 'FXINV-B-1', '%s', '%s', 'DRAFT', CURRENT_DATE + 30, '%s')
			ON CONFLICT (id) DO NOTHING`,
			InvoiceAlphaID, TenantAlphaID, BranchNorthAID, OrgNorthID,
			InvoiceBetaID, TenantBetaID, BranchNorthAID, OrgNorthID),

		fmt.Sprintf(`INSERT INTO invoice_lines (id, invoice_id, line_no, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, org_id) VALUES
			('%s', '%s', 1, 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s'),
			('%s', '%s', 1, 'Fixture line', 10, 'EA', 10, 1000, 'EA', 10000, '%s')
			ON CONFLICT (id) DO NOTHING`,
			InvoiceAlphaLine, InvoiceAlphaID, OrgNorthID,
			InvoiceBetaLine, InvoiceBetaID, OrgNorthID),

		// Payments: the money-attached signals beside the money order.
		fmt.Sprintf(`INSERT INTO payments (id, payment_no, customer_id, method, amount_cents, status, branch_id, org_id) VALUES
			('%s', 'FXPAY-A-1', '%s', 'CARD', 1000, 'RECEIVED', '%s', '%s'),
			('%s', 'FXPAY-B-1', '%s', 'CARD', 1000, 'RECEIVED', '%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			PaymentAlphaID, TenantAlphaID, BranchNorthAID, OrgNorthID,
			PaymentBetaID, TenantBetaID, BranchNorthAID, OrgNorthID),

		// Vaulted payment methods: opaque synthetic tokens, never a PAN.
		fmt.Sprintf(`INSERT INTO portal_payment_methods (id, customer_id, gateway, gateway_token, method_type, brand, last4, is_default, status, tenant_id) VALUES
			('%s', '%s', 'stub', 'FXVAULT-ALPHA-1', 'CARD', 'FX BRAND', '0000', TRUE, 'active', '%s'),
			('%s', '%s', 'stub', 'FXVAULT-BETA-1',  'CARD', 'FX BRAND', '0000', TRUE, 'active', '%s')
			ON CONFLICT (id) DO NOTHING`,
			PayMethodAlphaID, TenantAlphaID, TenantAlphaID,
			PayMethodBetaID, TenantBetaID, TenantBetaID),

		// Jobs rollup projects.
		fmt.Sprintf(`INSERT INTO projects (id, customer_id, name, job_address, tenant_id) VALUES
			('%s', '%s', 'Fixture Job Alpha 1', 'Fixture job address 1', '%s'),
			('%s', '%s', 'Fixture Job Beta 1',  'Fixture job address 2', '%s')
			ON CONFLICT (id) DO NOTHING`,
			ProjectAlphaID, TenantAlphaID, TenantAlphaID,
			ProjectBetaID, TenantBetaID, TenantBetaID),

		// Procurement planning projects and lines.
		fmt.Sprintf(`INSERT INTO procurement_projects (id, customer_id, name, status, created_by_portal_user_id, tenant_id) VALUES
			('%s', '%s', 'Fixture Procurement Alpha 1', 'PLANNING', '%s', '%s'),
			('%s', '%s', 'Fixture Procurement Beta 1',  'PLANNING', '%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			ProcureAlphaID, TenantAlphaID, PortalAdminAlphaID, TenantAlphaID,
			ProcureBetaID, TenantBetaID, PortalAdminBetaID, TenantBetaID),

		fmt.Sprintf(`INSERT INTO procurement_project_lines (id, project_id, line_no, product_id, description, qty_entered, uom, tenant_id) VALUES
			('%s', '%s', 1, '%s', 'Fixture line', 5, 'EA', '%s'),
			('%s', '%s', 1, '%s', 'Fixture line', 5, 'EA', '%s')
			ON CONFLICT (id) DO NOTHING`,
			ProcureAlphaLine, ProcureAlphaID, ProductID, TenantAlphaID,
			ProcureBetaLine, ProcureBetaID, ProductID, TenantBetaID),

		// Return requests against the alpha ready order and the beta
		// confirmed order, one line each. requested_by is the portal user
		// row id (the column is a uuid), never the platform subject.
		fmt.Sprintf(`INSERT INTO return_requests (id, request_no, order_id, customer_id, status, reason_code, requested_by, tenant_id) VALUES
			('%s', 'FXRREQ-A-1', '%s', '%s', 'PENDING', 'DAMAGED', '%s', '%s'),
			('%s', 'FXRREQ-B-1', '%s', '%s', 'PENDING', 'DAMAGED', '%s', '%s')
			ON CONFLICT (id) DO NOTHING`,
			ReturnAlphaID, OrderAlphaReadyID, TenantAlphaID, PortalAdminAlphaID, TenantAlphaID,
			ReturnBetaID, OrderBetaConfID, TenantBetaID, PortalAdminBetaID, TenantBetaID),

		fmt.Sprintf(`INSERT INTO return_request_lines (id, request_id, order_line_id, qty_base, tenant_id) VALUES
			('%s', '%s', '%s', 1, '%s'),
			('%s', '%s', '%s', 1, '%s')
			ON CONFLICT (id) DO NOTHING`,
			ReturnAlphaLine, ReturnAlphaID, OrderLineAlphaReadyID, TenantAlphaID,
			ReturnBetaLine, ReturnBetaID, OrderLineBetaConfID, TenantBetaID),
	}
}

// Cleanup removes exactly the fixture rows, children before parents. It
// never touches seed data or any other row: every delete is by fixed
// fixture id or by the fixture org and tenant anchors.
func Cleanup(ctx context.Context, db DB) error {
	for _, stmt := range cleanupStatements() {
		if err := db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("testkit cleanup: %w (statement %.60s...)", err, stmt)
		}
	}
	return nil
}

func cleanupStatements() []string {
	return []string{
		fmt.Sprintf(`DELETE FROM return_request_lines WHERE id IN ('%s', '%s')`, ReturnAlphaLine, ReturnBetaLine),
		fmt.Sprintf(`DELETE FROM return_requests WHERE id IN ('%s', '%s')`, ReturnAlphaID, ReturnBetaID),
		fmt.Sprintf(`DELETE FROM procurement_project_lines WHERE id IN ('%s', '%s')`, ProcureAlphaLine, ProcureBetaLine),
		fmt.Sprintf(`DELETE FROM procurement_projects WHERE id IN ('%s', '%s')`, ProcureAlphaID, ProcureBetaID),
		fmt.Sprintf(`DELETE FROM projects WHERE id IN ('%s', '%s')`, ProjectAlphaID, ProjectBetaID),
		fmt.Sprintf(`DELETE FROM portal_payment_methods WHERE id IN ('%s', '%s')`, PayMethodAlphaID, PayMethodBetaID),
		fmt.Sprintf(`DELETE FROM payments WHERE id IN ('%s', '%s')`, PaymentAlphaID, PaymentBetaID),
		fmt.Sprintf(`DELETE FROM invoice_lines WHERE id IN ('%s', '%s')`, InvoiceAlphaLine, InvoiceBetaLine),
		fmt.Sprintf(`DELETE FROM invoices WHERE id IN ('%s', '%s')`, InvoiceAlphaID, InvoiceBetaID),
		fmt.Sprintf(`DELETE FROM quote_lines WHERE id IN ('%s', '%s', '%s')`, QuoteAlphaLine, QuoteBetaLine, QuoteGammaLine),
		fmt.Sprintf(`DELETE FROM quotes WHERE id IN ('%s', '%s', '%s')`, QuoteAlphaID, QuoteBetaID, QuoteGammaID),
		fmt.Sprintf(`DELETE FROM order_lines WHERE id BETWEEN '%s' AND '%s'`, OrderLineAlphaDraftID, OrderLineGammaDraftID),
		fmt.Sprintf(`DELETE FROM orders WHERE id IN ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
			OrderAlphaDraftID, OrderAlphaConfID, OrderAlphaPickID, OrderAlphaReadyID, OrderAlphaMoneyID,
			OrderAlphaXBranchID, OrderBetaDraftID, OrderBetaConfID, OrderGammaDraftID),
		fmt.Sprintf(`DELETE FROM products WHERE id = '%s'`, ProductID),
		fmt.Sprintf(`DELETE FROM portal_users WHERE id IN ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
			PortalAdminAlphaID, PortalCrewAlphaID, PortalBuyerNoPayAlphaID, PortalAdminBetaID, PortalCrewBetaID,
			PortalBuyerBetaID, PortalAdminGammaID, PortalApGammaID, PortalInactiveAlphaID),
		fmt.Sprintf(`DELETE FROM customers WHERE id IN ('%s', '%s', '%s')`, TenantAlphaID, TenantBetaID, TenantGammaID),
		fmt.Sprintf(`DELETE FROM user_branch_grants WHERE user_id IN ('%s', '%s')`, UserInsideSalesID, UserSalesMgrID),
		fmt.Sprintf(`DELETE FROM driver_profile WHERE id = '%s'`, DriverProfileID),
		fmt.Sprintf(`DELETE FROM staff_role_grant WHERE id IN ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
			GrantInsideSalesID, GrantSalesMgrID, GrantGeneralMgrID, GrantTwoOrgNorthID, GrantTwoOrgSouthID,
			GrantRevokedID, GrantInertID, GrantDriverID, GrantSuspendedID),
		fmt.Sprintf(`DELETE FROM users WHERE id IN ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
			UserInsideSalesID, UserSalesMgrID, UserGeneralMgrID, UserTwoOrgID, UserRevokedID, UserInertID,
			UserDriverID, UserSuspendedID),
		fmt.Sprintf(`DELETE FROM dispatch_trucks WHERE id = '%s'`, TruckID),
		fmt.Sprintf(`DELETE FROM locations WHERE id IN ('%s', '%s', '%s')`, BranchNorthAID, BranchNorthBID, BranchSouthAID),
		fmt.Sprintf(`DELETE FROM org WHERE id IN ('%s', '%s')`, OrgNorthID, OrgSouthID),
	}
}

// VerifyBaseline asserts the seeded reference rows the fixtures depend on:
// the staff_role catalogue, the hhpro contractor role map (including the
// NULL canonical_role estimator row and the owner to account_admin
// mapping), and the carried uoms. Counts only, never values.
func VerifyBaseline(ctx context.Context, db DB) error {
	checks := []struct {
		name string
		sql  string
		want int
	}{
		{"staff_role catalogue rows", `SELECT count(*) FROM staff_role`, 16},
		{"hhpro contractor map rows", `SELECT count(*) FROM hhpro_contractor_role_map`, 5},
		{"hhpro estimator rows with no canonical role", `SELECT count(*) FROM hhpro_contractor_role_map WHERE canonical_role IS NULL`, 1},
		{"hhpro owner to account_admin mapping row", `SELECT count(*) FROM hhpro_contractor_role_map WHERE hhpro_role = 'owner' AND canonical_role = 'account_admin'`, 1},
		{"carried uoms seed", `SELECT count(*) FROM uoms WHERE code = 'EA'`, 1},
	}
	for _, c := range checks {
		got, err := db.Count(ctx, c.sql)
		if err != nil {
			return fmt.Errorf("testkit baseline check %s: %w", c.name, err)
		}
		if got != c.want {
			return fmt.Errorf("testkit baseline check %s: count %d, want %d (is the migration set applied?)", c.name, got, c.want)
		}
	}
	return nil
}

// FixtureCounts returns the fixture-owned row count per table, the cleanup
// verification shape: after Build the counts match ExpectedCounts, after
// Cleanup every count is zero, so the isolation plan's count deltas return
// to zero. Counting predicates are the fixture anchors only, so any other
// row in the same table (seed data, other lanes) does not disturb them.
func FixtureCounts(ctx context.Context, db DB) (map[string]int, error) {
	counts := map[string]int{}
	queries := map[string]string{
		"org":                       fmt.Sprintf(`SELECT count(*) FROM org WHERE id IN ('%s', '%s')`, OrgNorthID, OrgSouthID),
		"locations":                 fmt.Sprintf(`SELECT count(*) FROM locations WHERE id IN ('%s', '%s', '%s')`, BranchNorthAID, BranchNorthBID, BranchSouthAID),
		"dispatch_trucks":           fmt.Sprintf(`SELECT count(*) FROM dispatch_trucks WHERE id = '%s'`, TruckID),
		"users":                     fmt.Sprintf(`SELECT count(*) FROM users WHERE platform_user_id LIKE 'fxstaff-%%'`),
		"staff_role_grant":          fmt.Sprintf(`SELECT count(*) FROM staff_role_grant WHERE id IN ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`, GrantInsideSalesID, GrantSalesMgrID, GrantGeneralMgrID, GrantTwoOrgNorthID, GrantTwoOrgSouthID, GrantRevokedID, GrantInertID, GrantDriverID, GrantSuspendedID),
		"driver_profile":            fmt.Sprintf(`SELECT count(*) FROM driver_profile WHERE id = '%s'`, DriverProfileID),
		"user_branch_grants":        fmt.Sprintf(`SELECT count(*) FROM user_branch_grants WHERE user_id IN ('%s', '%s')`, UserInsideSalesID, UserSalesMgrID),
		"customers":                 fmt.Sprintf(`SELECT count(*) FROM customers WHERE id IN ('%s', '%s', '%s')`, TenantAlphaID, TenantBetaID, TenantGammaID),
		"portal_users":              fmt.Sprintf(`SELECT count(*) FROM portal_users WHERE platform_user_id LIKE 'fxportal-%%'`),
		"products":                  fmt.Sprintf(`SELECT count(*) FROM products WHERE id = '%s'`, ProductID),
		"orders":                    fmt.Sprintf(`SELECT count(*) FROM orders WHERE order_no LIKE 'FXORD-%%'`),
		"order_lines":               fmt.Sprintf(`SELECT count(*) FROM order_lines WHERE id BETWEEN '%s' AND '%s'`, OrderLineAlphaDraftID, OrderLineGammaDraftID),
		"quotes":                    `SELECT count(*) FROM quotes WHERE quote_no LIKE 'FXQTE-%%'`,
		"quote_lines":               fmt.Sprintf(`SELECT count(*) FROM quote_lines WHERE id IN ('%s', '%s', '%s')`, QuoteAlphaLine, QuoteBetaLine, QuoteGammaLine),
		"invoices":                  `SELECT count(*) FROM invoices WHERE invoice_no LIKE 'FXINV-%%'`,
		"invoice_lines":             fmt.Sprintf(`SELECT count(*) FROM invoice_lines WHERE id IN ('%s', '%s')`, InvoiceAlphaLine, InvoiceBetaLine),
		"payments":                  `SELECT count(*) FROM payments WHERE payment_no LIKE 'FXPAY-%%'`,
		"portal_payment_methods":    fmt.Sprintf(`SELECT count(*) FROM portal_payment_methods WHERE id IN ('%s', '%s')`, PayMethodAlphaID, PayMethodBetaID),
		"projects":                  fmt.Sprintf(`SELECT count(*) FROM projects WHERE id IN ('%s', '%s')`, ProjectAlphaID, ProjectBetaID),
		"procurement_projects":      fmt.Sprintf(`SELECT count(*) FROM procurement_projects WHERE id IN ('%s', '%s')`, ProcureAlphaID, ProcureBetaID),
		"procurement_project_lines": fmt.Sprintf(`SELECT count(*) FROM procurement_project_lines WHERE id IN ('%s', '%s')`, ProcureAlphaLine, ProcureBetaLine),
		"return_requests":           `SELECT count(*) FROM return_requests WHERE request_no LIKE 'FXRREQ-%%'`,
		"return_request_lines":      fmt.Sprintf(`SELECT count(*) FROM return_request_lines WHERE id IN ('%s', '%s')`, ReturnAlphaLine, ReturnBetaLine),
	}
	for name, q := range queries {
		n, err := db.Count(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("testkit count %s: %w", name, err)
		}
		counts[name] = n
	}
	return counts, nil
}

// ExpectedCounts is the per-table row count Build produces. FixtureCounts
// must equal it after Build and must be all zeros after Cleanup.
func ExpectedCounts() map[string]int {
	return map[string]int{
		"org":                       2,
		"locations":                 3,
		"dispatch_trucks":           1,
		"users":                     8,
		"staff_role_grant":          9,
		"driver_profile":            1,
		"user_branch_grants":        2,
		"customers":                 3,
		"portal_users":              9,
		"products":                  1,
		"orders":                    9,
		"order_lines":               9,
		"quotes":                    3,
		"quote_lines":               3,
		"invoices":                  2,
		"invoice_lines":             2,
		"payments":                  2,
		"portal_payment_methods":    2,
		"projects":                  2,
		"procurement_projects":      2,
		"procurement_project_lines": 2,
		"return_requests":           2,
		"return_request_lines":      2,
	}
}

// AssertCountsEqual fails the test unless got equals want, naming every
// table that differs, so a wrong count is a finding about a table, not a
// bare mismatch.
func AssertCountsEqual(t *testing.T, phase string, got, want map[string]int) {
	t.Helper()
	for name, wantN := range want {
		gotN, ok := got[name]
		if !ok {
			t.Fatalf("testkit: %s: no count read for table %s", phase, name)
		}
		if gotN != wantN {
			t.Errorf("testkit: %s: table %s has %d fixture rows, want %d", phase, name, gotN, wantN)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("testkit: %s: unexpected count read for table %s", phase, name)
		}
	}
}

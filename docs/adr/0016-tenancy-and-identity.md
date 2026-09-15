# ADR-0016: Tenancy and identity

Status: Proposed

Drafted in HH-2 lane HH-2.4 from `analysis/DECISIONS.md` D3, which Colton took at the close of HH-1. This draft records the decision; it does not reopen it.

## Decision

Appwrite Teams are the coarse gate only: org membership and audience (staff, contractor, customer). The thirteen staff roles live in app-role tables in the core keyed by platform user id, because per-membership Team roles would sprawl.

The ERP portal's contractor role set (`account_admin`, `buyer`, `field_crew`, `buyer_no_pay`, `ap_finance`) is canonical, since the ERP is the system of record for accounts. HH Pro's roles map onto it: `owner` to `account_admin`, `pm` to `buyer_no_pay`, `ap` to `ap_finance`, `field` to `field_crew`. A tenant holding the other set is reconciled once by a mapping table, with any extras becoming capabilities.

Driver is a role plus a profile row (vehicle, licence), not a separate identity, so one person can be yard staff and a driver.

Scope resolution order: org from the Team, tenant from the token, branch beneath both. There is no fail-open default: an unmapped user has no app role.

## Conflicts and open questions resolved

From `analysis/domain-map.md`: C29 (the ERP contractor set wins with a one-time mapping), C30 (staff roles as app tables), C31 (no fail-open default), C33 (org, tenant, branch resolution order). From `analysis/role-catalog.yaml`: open questions 5 (which contractor role set unifies, and what the migration does to a tenant holding the other) and 6 (whether driver is a role code or a membership row).

## Options rejected, and what each loses

### HH Pro's contractor vocabulary canonical

Rejected. What it loses, quoted from `domain-map.md` C29: "the ERP loses `account_admin` versus `buyer` as distinct, and loses the file-management capability, which no HH Pro capability covers." The reconciliation was already half-done from the ERP side: migration `0160` added `buyer_no_pay` and `ap_finance` because, in its own header's words, "the HH Pro portal models exactly that split (`pm` and `ap`), and until the ERP can express it, the portal must either render a role picker the server cannot enforce, the permission-gate-silently-off bug class, or lie about who its people are" (`domain-map.md` section 3.7).

Note that HH Pro carried two disagreeing contractor role enums of its own (`TeamRole` and `User.role`); the third enum goes regardless (`domain-map.md` C29).

### Staff roles as per-membership Team roles in Appwrite

Rejected: the thirteen staff roles across three tiers, with global or branch scope, would sprawl across Team memberships. Context section 3 maps only coarse roles onto platform teams (`org:<slug>` with operator, org_admin, reviewer, member; `tenant:<org>:<slug>` for dealers and customers), and `domain-map.md` C30 records that "neither repository's role set maps onto that shape at present." Staff roles are the axis that decides which micro-app a person gets (context section 4.5, "micro-app per role, shell per audience"), so they live in core tables (`staff_role_grant`) keyed by the platform user id, with the Team claim as the coarse gate.

### Fail-open when no role is found (HH Pro's default)

Rejected. HH Pro's default exists "so that an embed of the core without the crew feature costs nothing": "With no team seeded, everything is allowed" (`domain-map.md` C31, quoting `hh_pro_dibbits/CLAUDE.md`). The ERP's default is fail-closed: "An unknown role grants nothing" (`CapabilitiesFor`, `domain-map.md` C31). What rejecting fail-open protects against: "a deployment that fails to load a role grants every capability", the discipline the ERP side states as "undefined must never mean 'no check'" (`domain-map.md` C31). What the decision costs HH Pro, unsmoothed: "HH Pro's 'no crew seeded' path must seed an implicit owner, which is a behaviour change in the demo and in any embed" (`domain-map.md` C31).

### Driver as a separate identity

Rejected (`role-catalog.yaml` open question 6). The shipped rule is that the driver app asks the server rather than reading a role code; driver is a role plus a profile row carrying vehicle and licence on the same identity, so one person can be yard staff and a driver.

### Branch above tenant, or tenant above org

Rejected. C33's order stands: org from the Team (validated against Team membership before a database is touched, context section 4.4), tenant from the token, branch beneath both. The ERP's branch discipline (`branch_id NOT NULL` on every operational table, "no query without a branch predicate on branch-scoped tables", `domain-map.md` section 3.8) carries unchanged as the level below tenant. Neither repository has an `org_id` or `tenant_id` column at present ("no `org_id` or `tenant_id` column exists in any of the 196 migrations", `domain-map.md` section 3.8), so both columns are additive work.

## Carried as verification, not decided

Whether Appwrite Team memberships surface as OIDC claims stays an S2 check (context section 10). D3 stands either way: if claims are absent, a Function enriches userinfo and the engine resolves the coarse gate from Team membership by lookup (`analysis/DECISIONS.md`, "Carried as verification").

## Sessions gated

- HH-2.3: every operation in the OpenAPI document carries a `role` extension naming the gate, and the tenant routes assume the tenant-from-token rule.
- HH-3.3: JWKS verification, the `X-Org` middleware, tenant scoping middleware, and the app-role tables (`staff_role_grant`, `tenant_user.role`) keyed by platform user id, with the one-time contractor role mapping.
- HH-6 through HH-10: every role switch reads these tables; per the transformation path, D3 "unblocks HH-3.3 and every role switch."
- HH-10: contractor tenants are mapped per this decision and switched.

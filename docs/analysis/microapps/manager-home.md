> Origin: `analysis/microapps/manager-home.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# manager-home: Manager Home

Audience: internal   Roles: branch_manager, sales_manager, yard_manager, logistics_manager, procurement_manager, payables_receivables, general_manager, financial_controller, super_admin   Devices: desktop, tablet, phone   Environment: office
Kit: dashboard   Brand: fb-tokens   Shell: hh-work
Offline: read_cache_only. Scopes: widget_measures, approvals, warranty_board. Conflict policy: approvals server_wins. Why read cache only: a dashboard is a read, and the one write on it (deciding an approval) must not queue. The shipped decision path enforces separation of duties and re-resolves the approver's authority from the database on every request, so a queued decision would be a decision made against authority nobody re-checked (`backend/internal/identity/rbac.go`, `ApprovalRequest.Decide` and `StaffContext`).

## Three jobs
1. Open a role landing that leads with one "needs you" widget and then the numbers that role reads. The rule is already written: one needs-you widget per role landing, top left, everything else is context, and a landing carries six to nine widgets (`hardscapeos_dibbits/.claude/skills/hardscape-dashboard-widgets/SKILL.md`).
2. Decide a parked approval: margin below floor, journal over threshold, freight above quote. The authority is a named role allowlist rather than a tier test, and the requester may never decide their own (`backend/internal/identity/rbac.go`).
3. Run a report or read the commission register, then export or print it. The console generates every parameter form from the server's own schema so a filter cannot be silently misnamed (`app/src/reporting-console.ts`).

## Shared entities and handoffs
- approval_request: read and decide; raised by quote-desk, money-desk and dispatch-board via `fb://hh/approval_request/<id>`
- warranty_claim: read, settle, and maintain the vendor registry; raised by counter-till and public-desk via `fb://hh/warranty_claim/<id>`
- report: read across sales, inventory, dispatch and payroll; every widget measure comes from one curated catalog so a number means the same thing on every screen (`SKILL.md`)
- order, invoice, pick_task: read only, each widget row deep linking to the micro-app that owns it

## What it replaces now
- HardscapeOS: `reporting/reporting` (`app/src/reporting-console.ts`), `admin/approvals` (`hos-pending-approvals`), the board, reports and registry tabs of `warranty/warranty` (`app/src/warranty-surface.ts`), and the shell's `#home` dashboard itself (`app/src/dashboard-nav.ts`, `app/src/app-shell.ts`)
- HH Pro: nothing. The contractor's equivalent landing is pro-procurement.

## States to design once
- empty: a landing with nothing needing a person says so in its first line rather than hiding the needs-you widget, because an absent widget reads as a broken one.
- loading: each widget skeletons in its own size, never collapsing the grid, since the grid lands on a fixed row unit (`SKILL.md`).
- offline: measures are stamped with when they were pulled, and the decide control is unavailable rather than queued.
- conflict: an approval already decided elsewhere shows the decision and who made it, not a failed tap.
- error: a widget that cannot load its measure says so in place and stays on the grid; the rest of the landing still works.

## Device and environment notes
Read at a desk mostly, on a phone often, on a yard tablet sometimes. The grid is twelve columns wide, six at tablet and six at phone with full-width spans, and the whole card is a link (`SKILL.md`). Colour carries state and never alone, and the loud theme reaches the whole desktop app rather than the widget layer alone (same file). Approvals on a phone need the refusal and the amount legible in sunlight, which is why the freight policy deliberately carries no money on its card: the approvals list is open to every authenticated staff member (`backend/internal/identity/rbac.go`).

## Sources
- hardscapeos_dibbits/.claude/skills/hardscape-dashboard-widgets/SKILL.md
- hardscapeos_dibbits/app/src/reporting-console.ts
- hardscapeos_dibbits/app/src/warranty-surface.ts
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/backend/migrations/0140_dashboard_layouts.sql

> Origin: `analysis/microapps/quote-desk.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# quote-desk: Quote Desk

Audience: internal   Roles: inside_sales, outside_sales, sales_manager   Devices: tablet, desktop, phone   Environment: counter, site
Kit: guided-flow   Brand: fb-tokens   Shell: hh-work
Offline: read_cache_plus_outbox. Scopes: products, price_matrix, customers, quotes. Conflict policy: quotes last_write_wins_by_field, price_matrix server_wins. Why this mode: outside sales works at a customer's site where signal is not a given, and a quote moves no money until conversion, which is stated as the reason quote editing is open to any active in-branch staff member while conversion is narrowly gated (`hardscapeos_dibbits/app/src/nav-access.ts`, `backend/internal/identity/rbac.go`). So a quote may be drafted offline and replayed; the price matrix is never authored here, only read.

## Three jobs
1. Browse the catalogue and size the job: how much of this for a patio of this area. The rail is deliberately ordered browse, size, quote, and the reason is written into the registry itself (`hardscapeos_dibbits/app/src/dashboard-nav.ts`).
2. Build the quote and meet the guard when the price falls below the margin floor, which parks an approval rather than blocking silently (`hos-quote-guard`, mounted at `app/src/app-shell.ts`; the floor and the parked request live in `backend/internal/identity/rbac.go`).
3. Send it to the customer on a share link, then convert the accepted one into an order against the customer's credit.

## Shared entities and handoffs
- quote: write; handed to public-desk by share token via `fb://hh/quote/<id>` and to counter-till for conversion
- product: read; written by dealer-admin via `fb://hh/product/<id>`
- price rule: read only; authored in dealer-admin, never here
- approval_request: write; handed to manager-home via `fb://hh/approval_request/<id>`
- order: write on conversion; handed to dispatch-board and yard-pick via `fb://hh/order/<id>`
- customer: read; the credit headroom that decides conversion is read, never edited

## What it replaces now
- HardscapeOS: `sales/catalogue` (`hos-product-catalogue`), `sales/calculator` (`hos-material-calc`), `sales/quote` (`hos-quote-guard`), `sales/quotes` (`hos-quote-builder`), all mounted in `app/src/app-shell.ts` and registered in `app/src/dashboard-nav.ts`
- HH Pro: nothing directly. The contractor's own quote push arrives here as work, from pro-procurement.

## States to design once
- empty: no quote open shows the customer search and the recent quotes, not a blank form.
- loading: the line table keeps its columns so a price does not appear to move when it lands.
- offline: the draft is marked queued, and conversion is visibly unavailable rather than failing on tap, because conversion is the money.
- conflict: a quote edited elsewhere merges by field, and a line the other edit removed surfaces for a decision rather than reappearing.
- error: the refusal names the way forward. A below-floor price says an approval is needed and who may decide it, not "forbidden".

## Device and environment notes
Outside sales reads this on a tablet in sunlight on a driveway, one handed, so the material calculator's inputs and the line rows need touch targets, not a desktop table. The calculator is gated `authed` rather than `always` because it exposes the yard's packaging and waste rules, and offering a door more widely than the read behind it produces a door that refuses (`hardscapeos_dibbits/app/src/nav-access.ts`). The screen is a workflow, not a form, and helper paragraphs are banned (`hardscapeos_dibbits/.claude/skills/hardscape-ui-design/SKILL.md`).

## Sources
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/app/src/app-shell.ts
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/docs/contractor-portal-plan.md
- hardscapeos_dibbits/.claude/skills/hardscape-ui-design/SKILL.md

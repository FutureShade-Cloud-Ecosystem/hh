> Origin: `analysis/microapps/pro-procurement.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# pro-procurement: Pro Procurement Board

Audience: external   Roles: contractor_owner, contractor_buyer   Devices: phone, desktop   Environment: site
Kit: list-detail   Brand: hh-pro   Shell: hh-pro
Offline: read_cache_plus_outbox. Scopes: projects, orders, scope_items, price_matrix, catalog, customer_quotes, team. Conflict policy: orders server_wins, scope_items last_write_wins_by_field, customer_quotes manual, team manual. Why this mode: the contractor plans on a phone at a site, so a draft must survive a dead spot, but the supplier owns the order once it is placed and the scope locks at that point by rule (`hh_pro_dibbits/CLAUDE.md`, the M3 scope-lock rule). A customer quote is `manual` because a signed acceptance is frozen evidence, and the team store is `manual` because who you are acting as is per window and must never be adopted wholesale from another one (same file).

## Three jobs
1. Move a card across Plan, Quote, Order and Invoice, and read the refusal verbatim when a move is rejected. Board cards are orders, not projects, so every card has exactly one unambiguous stage (`hh_pro_dibbits/src/ui/pages/BoardPage.tsx`, `src/core/domain/stage.ts`).
2. Build the scope of an order from the catalogue at the account price, and push it to the supplier quote desk when a line has no price behind it. Pricing has a single binding, and a repeat SKU bumps a quantity rather than opening a second line (`src/core/selectors/pricing.ts`, `src/core/actions/scope.ts`).
3. Build a contractor-branded proposal for the homeowner and send it on a one-time link that rotates on revision (`src/ui/pages/QuoteStudioPage.tsx`, `src/core/domain/customer-quote.ts`).

## Shared entities and handoffs
- order: write; handed to quote-desk and counter-till on the supplier side via `fb://hh/order/<id>`
- customer_quote: write; handed to public-desk by one-time token via `fb://hh/customer_quote/<id>`
- product and price: read; written by dealer-admin via `fb://hh/product/<id>`
- project: write; read by pro-field and pro-pay via `fb://hh/project/<id>`
- invoice: read only; written by counter-till and money-desk, paid in pro-pay
- team member: write (owner only); read by every contractor micro-app for the capability gate (`src/core/domain/team.ts`)

## What it replaces now
- HardscapeOS: `portal /dashboard`, `/orders`, `/orders/:id`, `/projects`, `/projects/:id`, `/quotes`, `/procurement`, `/procurement/:id`, `/team`, `/settings`, and the document `portal.html` (`app/src/portal/portal-shell.ts`)
- HH Pro: `/`, `/orders/:orderId`, `/orders/:orderId/quote`, `/projects/:projectId`, `/catalog`, `/catalog/:sku`, `/today` (`src/ui/App.tsx`)

## States to design once
- empty: an empty draft shows a dash, never a confident zero, because an unknown total is not zero (`src/core/domain/totals.ts`, `hasKnownSubtotal`).
- loading: cards keep their product image slots; a card is recognised by picture faster than by two truncated lines.
- offline: the queued count sits on the board chrome, and the stages the supplier owns are visibly unavailable.
- conflict: a supplier-owned change wins and is narrated; a signed customer quote never merges, it surfaces for review.
- error: the refusal is a contractor-readable sentence that conjugates, shown on a rejected drop and handed verbatim to the assistant; the named remedy is a button, not prose (`src/core/domain/stage.ts`, `hh_pro_dibbits/CLAUDE.md`).

## Device and environment notes
Mobile first, desktop capable, light first and high contrast because contractors use this in trucks and direct sun (`src/ui/styles/theme.css`, `hh_pro_dibbits/CLAUDE.md`). Exactly one board layout is mounted at a time; keeping both mounted doubles the DOM on a phone and registers two drop targets under the same ids (`src/ui/components/board/Board.tsx`). Invalid drops are accepted and then explained, because disabling the target prevents the mistake and teaches nothing. Every mutating assistant tool calls the same action a button calls, so this app has one mutation path whatever is driving it.

## Sources
- hh_pro_dibbits/src/ui/App.tsx
- hh_pro_dibbits/src/ui/pages/BoardPage.tsx
- hh_pro_dibbits/src/ui/pages/OrderPage.tsx
- hh_pro_dibbits/src/ui/pages/QuoteStudioPage.tsx
- hh_pro_dibbits/src/core/domain/stage.ts
- hh_pro_dibbits/src/core/domain/team.ts
- hh_pro_dibbits/src/core/selectors/pricing.ts
- hardscapeos_dibbits/app/src/portal/portal-shell.ts
- hardscapeos_dibbits/app/src/portal/portal-api.ts

> Origin: `analysis/microapps/public-desk.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# public-desk: Public Desk

Audience: external   Roles: homeowner (and any unauthenticated member of the public holding a token or a printed code)   Devices: phone, desktop   Environment: home, counter
Kit: guided-flow   Brand: hh-yard   Shell: hh-public
Offline: none. Scopes: none. Conflict policy: none. Why none: every entry here is a single-use, time-boxed credential whose liveness only the server knows. The will-call page already distinguishes a dead code from an unknown one, deliberately, because "not found" for a code somebody is holding produces a phone call (`hardscapeos_dibbits/app/src/dispatch-willcall-public.ts`). A cache would make a revoked code look live. There is also nothing to cache for: no session, no account, no return visit.

## Three jobs
1. Open a proposal or a quote from a one-time link, read the lines and the terms, and accept it. The ERP page captures a typed-name attestation and states in plain words exactly what is being given, because an artifact must not imply more than it captured (`app/src/quote-public.ts`). The contractor proposal captures the typed legal name, the drawn mark, the verbatim consent text, and the total agreed to, all frozen (`hh_pro_dibbits/src/core/domain/customer-quote.ts`).
2. Confirm a will-call collection against a printed code, with a typed full name and an explicit affirmative, refused if either is missing (`app/src/dispatch-willcall-public.ts`).
3. Report a failed installation with photos and receive a reference number, with no account offered and deliberately no "check my claim" link, because a public read keyed on that reference is the auth bypass the surface refuses (`app/src/warranty-intake-public.ts`).

## Shared entities and handoffs
- customer_quote: read and accept; written by pro-procurement, and the acceptance is read back there via `fb://hh/customer_quote/<id>`
- quote: read and accept; written by quote-desk via `fb://hh/quote/<id>`
- will_call: read and confirm; minted by dispatch-board or yard-pick via `fb://hh/will_call/<id>`
- warranty_claim: write; handed to manager-home and counter-till via `fb://hh/warranty_claim/<id>`

## What it replaces now
- HardscapeOS: `quote.html` and `app/src/quote-public.ts`; the will-call route mounted on the main document at `#will-call/<token>` (`app/src/app-shell.ts`, `app/src/dispatch-willcall-public.ts`); the public warranty intake mounted on the same document (`app/src/app-shell.ts`, `app/src/warranty-intake-public.ts`)
- HH Pro: `/q/:token` (`src/ui/pages/CustomerQuotePage.tsx`)

## States to design once
- empty: not applicable; a page with no token is the unknown-code state, not an empty one.
- loading: the document skeleton shows the lines and the total placeholder, never a bare spinner on a page somebody is about to sign.
- offline: the page says it needs a connection to verify the link, because the link's liveness is the only thing it can offer.
- conflict: a superseded link is dead, by design. A revised proposal rotates its token the moment intent to change forms, not at re-send (`hh_pro_dibbits/CLAUDE.md`).
- error: three distinct answers, kept distinct: live, expired or already used (see the counter), and not recognised. The server's public wording reaches the page verbatim, naming a field or a limit and never a table (`app/src/warranty-intake-public.ts`).

## Device and environment notes
A phone at a kitchen table or beside a counter, held by somebody who has never seen this product and will not see it again. No login, no session, no branch. Every page here uses bare fetch rather than the authenticated helper, so no staff bearer token can ever reach a pre-auth surface (`app/src/quote-public.ts`, `app/src/dispatch-willcall-public.ts`, `app/src/warranty-intake-public.ts`). The share page forces its own light palette and renders no supplier identity and no contractor cost (`hh_pro_dibbits/CLAUDE.md`). The signature pad clears only on a genuine width change, because wiping a finished signature when somebody taps the name field is unforgivable (same file).

## Sources
- hardscapeos_dibbits/app/src/quote-public.ts
- hardscapeos_dibbits/app/quote.html
- hardscapeos_dibbits/app/src/dispatch-willcall-public.ts
- hardscapeos_dibbits/app/src/warranty-intake-public.ts
- hardscapeos_dibbits/app/src/app-shell.ts
- hh_pro_dibbits/src/ui/pages/CustomerQuotePage.tsx
- hh_pro_dibbits/src/core/domain/customer-quote.ts
- hh_pro_dibbits/src/ui/styles/theme.css

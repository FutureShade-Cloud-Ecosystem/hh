# web/apps/public-desk

The Public Desk micro-app: exactly one page, the customer's quote page on a
share link token (the contract's `quotePublicViewQuote`,
`quotePublicAcceptQuote` and `productGetImage`). The customer opens the link a
salesperson minted, reads the quote's lines and terms, and accepts by typing
their full name and ticking the attestation. There is deliberately no
signature pad: the page says in plain words what an acceptance records.

## The token is the credential

The page runs before any authentication. It holds no session, reads no staff
token, and ships none of the session machinery: the bundle never imports the
auth package, and the `ApiClient` it builds carries no header provider at all,
so no credential can ride a request even by accident. The suite holds both
halves of that rule: a source scan (no storage sink, no auth import, no
credential header anywhere in `src/`) and a runtime assertion against the
stub's request ledger (every call arrives with no `Authorization`, no cookie
and no org header, and every path is a public route or the product image
read).

Pre-auth also pins the light theme in `index.html`, before any stylesheet: a
customer document must look identical to everyone and must print correctly,
so there is no theme preference to read (the ERP's own ruling for this page,
carried).

The brand is pinned beside the theme: `index.html` sets `data-brand="hh-pro"`,
because the manifest names `brand: hh-pro` for this app and the design system's
`hh-pro` override is the dealer blue. So the Accept button is the dealer blue,
not the ERP's house gold. That is a deliberate departure from the reference
shots, where the button is gold; nothing else on the page changes with the
brand. If the house decides this page should stay gold, drop the attribute.

## What it ports

This is a port of the older product's customer quote page, never from
scratch: the page shell, the component, the totals logic (the taxed and
before-HST framings decided from the data, so a total that already carries
HST is never labelled pre-tax) and the typed name acceptance all carry the
reference's structure and finish, mapped onto this repository's design system
tokens. Two deliberate departures, both house rules:

- The disabled accept button goes grey through the `--hh-disabled-*` token
  swap, not a fade. Disabled must look disabled: colour, not opacity.
- Every sentence is carried across without a dash of the forbidden kinds, so
  a few copy strings re-punctuate the reference's wording (colons and
  parentheses in its place). The words are otherwise the reference's, except the
  plain sentences for a failed accept (the server's words show only when it
  sent the JSON error envelope). The page carries no copy addressed to its
  builders: everything on it is written for the customer.

## Testing

- `test/unit.test.mjs`: the pure logic in Node, against the workspace's
  api-client loader: the taxed and before-HST totals framings, the acceptance
  amount phrase, the typed name gate, the status words, the money formatter,
  and the field-by-field response validation.
- `test/sinks.test.mjs`: the credential scan, the same gate the Counter Till
  holds, tightened for this page: no storage sink, and no auth package, no
  session store, and no credential header anywhere in the source.
- `test/page.pw.ts`: the page in a real browser against the stub
  (`test/stub/public-stub.mjs`, which serves the contract's shapes): view by
  token, the expired and revoked refusals, the acceptance refused when blank
  and accepted when typed, the second visit shown as already accepted, and
  the no-credential rule held at runtime.

The stub is a development and test double only; nothing in the app imports
it. When the real routes land, the page and this suite keep working against
them unchanged, because both speak only the contract's shapes.

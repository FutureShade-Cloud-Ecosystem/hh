# core/db

The database lane of the unified core: `migrations/` holds the forward-only SQL set, `queries/` the sqlc files. `PORTED.md` is the ledger binding the baseline to its ERP source.

## The baseline, and the additive-only rule

`migrations/0001` through `0212` (196 files) are the HardscapeOS ERP's own migration set, carried from `hardscapeos_dibbits` `master` at `6eaea3ec` (D33): DDL, filenames and version numbers verbatim, fixture text scrubbed of third-party identifying values before the first commit (D32), the only point at which that is possible, since migrations are forward-only. Every migration after the baseline, starting at `0213`, is additive: every new column nullable or defaulted, no destructive statement anywhere. The running ERP must be unaffected by the full set, proved by running the ERP's own integration suite against the migrated database.

## Scrub exclusions, by reason class

Scrubbed, in comments as well as in SQL values: customer names, individual names, standalone first names, email addresses on live domains, street addresses, speaker attributions. An ambiguous case scrubs. The kept classes, recorded by reason, never by value, each with its count from the scrub pass:

- The client's own business name where it identifies the client (comments, fixture identifiers, reserved-domain logins): a label, not a routing target, not third-party data. Customer rows whose names embed it are not excluded. (count: excluded from the set by config; not enumerated)
- Branch town names standing alone: public business locations. (count: two town tokens and four city-shaped json leaves excluded by the masked review)
- Reserved-domain test logins and the fixture identifiers the ERP's gate suite depends on: the suite depends on them. (count: 12 addresses on reserved domains, plus the fixture identifiers the classification recorded)
- A persona display name spelled by a kept reserved-domain login: kept with the login, since scrubbing one while the other spells it removes nothing. (count: 4)
- First-name literals whose every occurrence is the common-word sense: excluded before the set froze, after whole-word, case-sensitive review through masked context. (count: the masked review dropped every candidate never seen in a person context; four mapped tokens resolved to no occurrence after longer originals consumed them)
- Manufacturer and supplier names: functional reference data in the product catalogue. Their email addresses are not excluded. (count: six manufacturer names kept; two supplier-name candidates excluded; five live vendor, client and partner domains scrubbed)
- Addresses on domains FutureBuild owns: not third-party data; they route to the party shipping the product. (count: 2)
- Quoted ruling content in comments: the client's business logic; only the attribution, the speaker's name, was scrubbed. (count: ten attributions scrubbed across the whole carried set)

## The freeze

**Lifted by D50.** `SCHEMA-FROZEN` is removed, so `ci`'s freeze check passes inert (it passes when the marker is absent). Migrations stay forward-only and numbered, a merged migration is never edited, and the principal assigns each cycle a block of numbers. The paragraph below is kept as the record of the freeze that was.

At Phase A sign-off, `SCHEMA-FROZEN` lands here, naming the commit at which the migration set froze. From then on `ci` fails any pull request whose diff touches `migrations/` after that commit. One door: HH-3.7 ports ERP migrations landing on `master` above the baseline, each a human decision of ported or deferred recorded in `PORTED.md`, and the pull request carrying that entry is the only one permitted to move the marker. The freeze is proven real: the verifier adds a migration on a scratch branch and shows `ci` fails.

## The replacement map

The scrub's replacement map was built on the workstation, never committed, never reported, discarded once the proofs passed. Replacements are patterned placeholders, obviously synthetic by construction, checked for collisions against the ERP's and this repository's text offline only, never by web search. Three proofs, all required: the from-zero apply succeeds; the ERP's gate suite passes; a search for every original literal across `migrations/` returns zero, reported as a count, never as the literals.

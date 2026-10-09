# Before you push: every gate an HH change runs

Every HH change runs the same gates before it leaves the workstation. Some
are core, some are web, some are policy; each has the exact command CI runs
it with, and each fails the push when it goes red. The list below is the
single page to walk when a change is ready to leave. CI repeats the same set
on the branch head, so a clean run here is the only honest signal that
nothing the gate cares about has.

## The whole untagged core suite, with the demo job's database variables

The untagged suite is the day gate. It runs once, in foreground, with the
demo job's three variables set on its side. The `-p 1` keeps two packages
from racing one another's fixture cleanups on one shared database. The
targeted runs are not enough: cross-module gates, tablegate's census
included, catch what they miss.

```bash
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_base?sslmode=disable \
HH_CORE_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_core_gate?sslmode=disable \
HH_B3_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_b3_gate?sslmode=disable \
go test ./... -count=1 -p 1 -timeout 30m
```

The ports come from the local CI service; the `postgres:postgres` user and
password come from the demo job's `postgres:16-alpine` fixture, and the
three databases are `till_base` (empty, as the demo job's service leaves
it), `till_core_gate` (migrated) and `till_b3_gate` (migrated). The
demo job's first runs are read, not relied on, but the database variables
are the same.

## CI's whole integration package list, in `go list` order

The integration tag suite is the second gate. CI runs the same
`go test -tags integration -p 1 -count=1 -timeout 30m` against `go list ./...`, with one
exclusion, on one migrated and seeded database. The exclusion is the single
line CI carries; a package rejoins the run when its lane lands the fix and
drops its line here.

```bash
go test -tags integration -p 1 -count=1 -timeout 30m $(go list ./... | grep -Ev "/internal/(dispatch)$")
```

The two scratch databases are `integration_gate` (migrated and seeded; the
module suites' dibbits catalogue and demo users the seed plants) and
`integration_core_gate` (migrated only; the composition kit's fixture world).
Fixtures with `LIMIT 1` and no `ORDER BY` pick differently on a shared
database, so the suite uses one of each.

```bash
psql "$BASE/postgres?sslmode=disable" -v ON_ERROR_STOP=1 \
  -c 'CREATE DATABASE integration_gate' \
  -c 'CREATE DATABASE integration_core_gate'
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/integration_gate?sslmode=disable \
  go run ./cmd/core migrate
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/integration_gate?sslmode=disable \
  go run ./cmd/core seed
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/integration_core_gate?sslmode=disable \
  go run ./cmd/core migrate
```

The integration gate runs against the module database:

```bash
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/integration_gate?sslmode=disable \
HH_CORE_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/integration_core_gate?sslmode=disable \
go test -tags integration -p 1 -count=1 -timeout 30m $(go list ./... | grep -Ev "/internal/(dispatch)$")
```

## The contract step, twice byte identical

A contract move (a fragment under `core/api/fragments/`) builds the document
and the client types twice, byte identical, before the change leaves. CI
runs the same step, in order:

```bash
npm --prefix core/api/tools ci
node core/api/tools/merge.mjs
git diff --exit-code -- core/api/openapi.yaml
node core/api/tools/gen-types.mjs
node core/api/tools/parity.mjs
node core/api/tools/fixtures.mjs
node core/api/tools/statemap.mjs
node core/api/tools/d8-isolation.mjs
```

`merge.mjs` rebuilds `openapi.yaml` from the fragments; the `git diff
--exit-code` step fails the build when the rebuilt document names a
fragment the change forgot. `gen-types.mjs` rebuilds the client types.
`parity.mjs`, `fixtures.mjs`, `statemap.mjs` and `d8-isolation.mjs` are
the four CI runs the contract carries. None of these scripts edits
`openapi.yaml` by hand; the merge is the only path.

Run the same script twice on a clean tree:

```bash
node core/api/tools/merge.mjs > /tmp/contract-1.txt
node core/api/tools/merge.mjs > /tmp/contract-2.txt
diff -u /tmp/contract-1.txt /tmp/contract-2.txt
```

A clean diff is the byte-identity gate. A change that does not touch the
fragments has nothing to assert; the step still runs, because the move
or a non-move is the same proof.

The counted pins are the census's anchors: `core/cmd/core/census_test.go`
on the Go side, `web/packages/api-client/test/result-shape.test.mjs` and
`web/packages/api-client/test/idempotence.test.mjs` on the web side.
When a fragment or a handler moves the route list, the census counts
rise or fall and the test red on the way. A change that does not move
the route list recouns the same number and is no more.

## The web loop, as `ci.yml` runs it

The web loop is one typecheck and one test pass per package and per app.
The design system's binding-position gate (`test/positions.test.mjs`) is
the visible instance of the loop's reading: a refusal of a binding the
owned renderer does not allow. CI checks for `google-chrome` before the
loop, names the browser that ran, and stops on a missing one.

```bash
command -v google-chrome >/dev/null 2>&1 || { echo "google-chrome is missing" >&2; exit 1; }
google-chrome --version

checked=0
for dir in packages/*/ apps/*/; do
  [ -f "$dir/package.json" ] || continue
  name="$(basename "$dir")"
  if grep -q '"typecheck"[[:space:]]*:' "$dir/package.json"; then
    npm --prefix "$dir" run typecheck
  elif find "$dir" -path '*/node_modules' -prune -o -name '*.ts' -print | grep -q .; then
    echo "$name has TypeScript sources but no typecheck script; add one" >&2
    exit 1
  else
    echo "$name: nothing to typecheck"
  fi
  if grep -q '"test"[[:space:]]*:' "$dir/package.json"; then
    npm --prefix "$dir" test
    checked=$((checked + 1))
  elif [ -d "$dir/test" ]; then
    echo "$name has a test directory but no test script; add one" >&2
    exit 1
  fi
done
[ "$checked" -ge 1 ] || { echo "no web package ran a test suite; the loop above is broken" >&2; exit 1; }
```

A package that ships TypeScript without a typecheck script, or a `test/`
folder without a `test` script, fails the loop with the package named.
Nothing under `packages/` or `apps/` is allowed to opt out.

## The tablegate allowlist rule

Cross-module table access is the gate the worktree enforces at the file.
A change that reads a foreign module's table through the wrong file, or
writes one through the wrong file, fails the census (`TestCensusZeroCrossModuleTableAccess`).
The allowlist (`core/internal/platform/tablegate/allowlist_test.go`) is
the one place a cross-module access can be honest; the rule is "empty
diff, or a `scope_sequences` allocator line with its reason":

```bash
git diff origin/main -- core/internal/platform/tablegate/allowlist_test.go
```

The diff is empty for any change that does not add a `scope_sequences`
allocator line. The allocator is the 0213 shared sync seq stream; every
adapter that stamps pushed rows through it adds one entry to the
allowlist, naming the file and the reason read from that file's own
source. The census fails the fingerprint's bite proof: removing an entry
turns the census red because the entry's reason and the file's source
disagree.

`TestMigrationNumbersAreUnique` is the second tablegate gate; every
migration number is used once on the tree (the one recorded pair is
frozen at `0043`).

## Sign-offs on every commit and merge

Every commit carries `Signed-off-by`. Merge commits included. The merge
command is signed off as well:

```bash
git merge --no-ff --signoff
```

The one-line loop over `origin/main..HEAD` is the receipt:

```bash
for c in $(git rev-list origin/main..HEAD); do git log -1 --format=%B $c | grep -q Signed-off-by || echo MISSING $c; done
```

A change with no merge commit and one feature commit prints nothing.

## Migration numbering, in flight

A new migration is forward only, never edited once on main, and numbered
above main's last at the moment the branch left `origin/main`. A branch
in flight against main that adds the same number renumbers before it
merges. The number the brief gives is the floor; if main has reached it
by the time the change merges main in, renumber inside that merge and
say so in the report.

```bash
ls core/db/migrations/ | sort -u
git diff origin/main -- core/db/migrations/
```

`TestMigrationNumbersAreUnique` reads the same directory and fails the
suite when two files share a number. The recorded pair is `0043`.

## The grep for old words, when anything is reworded

A change that rewords or retitles anything a person or a test sees owes
an update in every place the old wording lives: tests, acceptance legs,
the claims ledger (`test/acceptance/claims.ts`; `40-claims.pw.ts` matches
titles exactly), stubs and tester pages. Stubs drift from the server:
compare a stub's refusal order, body fields and words with the core's
whenever a refusal moves.

```bash
grep -rn "<old words>" web core
```

The grep is the receipt: every line it returns is a place the new
wording must reach before the change leaves.

## The sign-off is the reader's own loop; the relay holds back an unsigned box branch

The repository does not ship a pre-push hook: a reader's clone carries no
hook, and the push goes straight through. The gate above (the loop over
`origin/main..HEAD`) is the reader's own receipt; a branch whose loop
prints a `MISSING` line is not signed off even for run locally.

The HH supervisor's local clone carries a pre-push hook that does the same
check, and it also reads `push-hold.txt` for branches the supervisor is
holding back. The HQ relay holds back a box branch whose push attempt
arrived with an unsigned commit, and the hook returns a non-zero exit so
the push lands only when every commit carries `Signed-off-by`. The relay
is not in the repository either; a reader's clone never sees it.

## What the brief leaves out, named once

The dispatch package stays on CI's `red=` exclusion list. No command
above mentions it; the integration command carries the exclusion in its
`grep -Ev` argument, and a unit test on it is not part of any gate. A
package rejoins the run when its lane lands the fix and drops its line
from the exclusion.

## The short form, in order

```bash
# 1. Build and vet core
cd core && go build ./... && go vet ./...

# 2. The untagged suite with the demo job's variables
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_base?sslmode=disable \
HH_CORE_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_core_gate?sslmode=disable \
HH_B3_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_b3_gate?sslmode=disable \
go test ./... -count=1 -p 1 -timeout 30m

# 3. The contract step, twice byte identical, then CI's contract tools
npm --prefix core/api/tools ci
node core/api/tools/merge.mjs
git diff --exit-code -- core/api/openapi.yaml
node core/api/tools/gen-types.mjs
node core/api/tools/parity.mjs
node core/api/tools/fixtures.mjs
node core/api/tools/statemap.mjs
node core/api/tools/d8-isolation.mjs

# 4. The integration tag suite on one migrated and seeded database
go test -tags integration -p 1 -count=1 -timeout 30m $(go list ./... | grep -Ev "/internal/(dispatch)$")

# 5. The web loop, folder by folder
for dir in packages/*/ apps/*/; do
  [ -f "$dir/package.json" ] || continue
  name="$(basename "$dir")"
  if grep -q '"typecheck"[[:space:]]*:' "$dir/package.json"; then
    npm --prefix "$dir" run typecheck
  fi
  if grep -q '"test"[[:space:]]*:' "$dir/package.json"; then
    npm --prefix "$dir" test
  fi
done

# 6. The tablegate allowlist rule: empty diff
git diff origin/main -- core/internal/platform/tablegate/allowlist_test.go

# 7. Sign-offs on every commit and merge
for c in $(git rev-list origin/main..HEAD); do git log -1 --format=%B $c | grep -q Signed-off-by || echo MISSING $c; done

# 8. Migrations numbered above main's last and unique against branches in flight
ls core/db/migrations/ | sort -u

# 9. The grep for old words, when anything is reworded
grep -rn "<old words>" web core
```

The order is the order a CI test runs them in: build and vet first, then
the day gates, then the contract and the web loops, then the gates on the
change itself. A red at any step is the verdict; a green at every step
is the only honest signal the change is ready to leave.
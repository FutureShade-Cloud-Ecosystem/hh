# The business day and the evening window

Every date the system names is the dealer's local day, in the house zone, never the
server's zone and never the database session's `CURRENT_DATE`. AGENTS.md states this in
the "Daily means the dealer's day" rule, and `core/internal/platform/houseday` is the
place code asks for that day. The hour after the UTC day has rolled over and the
house zone's has not is the dangerous window: a date read at Greenwich is one ahead
of the dealer's, and a wall clock run inside it can hide the defect.

## The rule, named once

"Daily" means the dealer's local day in `America/Toronto`. UTC rolls over in the
evening in Ontario. Anything a person reads as today's cap, today's run, today's
statement, today's aging bucket, today's invoice issue date, today's promise
date, or today's register counts whole days in the house zone. The decision
lives in Go (`houseday.Today(now)`, `houseday.AddDays(now, n)`,
`houseday.JustAfterUTCDayChange(day)`) and in SQL (`(now() AT TIME ZONE
'America/Toronto')::date`, the constant `arAgingZone`). A test or a tool that
reads the UTC clock and slices it to a date literal, or reads `CURRENT_DATE`,
is wrong on principle, not just at one wall hour.

## Why the hour after the UTC day change is the dangerous window

Half past UTC midnight on a given day is `houseday.JustAfterUTCDayChange` of that
day. The UTC date has rolled over; the house zone (four or five hours behind) is
still on the previous evening. Code that reads the day at Greenwich answers
tomorrow; code that reads it in the house zone answers today. The two agree in
the middle of the afternoon and disagree every evening.

A test suite that pins the host's clock sees one answer. A test suite that
pins a clock in the evening sees the other. The defect lives only in the
disagreeing window, and a wall clock that happens to land inside it can hide
or expose it. The work is to make every date leg red on the wrong code at any
wall hour, so neither a daytime nor an evening CI run can drift past it.

## How to write a date leg that is red on wrong code at any hour

### Go tests: pin the clock to the previous evening in the house zone

The platform package ships the instant:

```
JustAfterUTCDayChange(day) returns time.Date(d.Year(), d.Month(), d.Day(), 0, 30, 0, 0, time.UTC)
```

`day` is any instant on the wanted UTC day. The leg in `houseday_test.go` and
every date leg on the HH-195 / AR house day work pass a fixed UTC instant of
mid-afternoon on a named day; passing it to `JustAfterUTCDayChange` returns
half past UTC midnight on that day, the previous evening in `America/Toronto`.

The leg hands the instant to the code under test instead of `time.Now()`:

- a `Landing` leg hands it as `asOf`;
- an invoice writer hands it as the writer's `now`;
- a register repository hands it as `houseClock(now)` at the SQL boundary;
- an aging reader hands it as `syncscope`'s clock parameter.

A leg that pins `JustAfterUTCDayChange` is red on code that reads
`time.Now().UTC()` or `CURRENT_DATE` at any wall clock hour the suite runs,
because the leg's clock disagrees with the wrong code's clock on which day
the answer names.

### Playwright legs: pin the page clock and the stub's clock together

The drive-run app's test helper pins both clocks to the previous evening in
the dealer's zone, through one fixture that every leg picks up:

```
const at = new Date();
at.setUTCHours(0, 30, 0, 0);
return at.getTime();
```

`eveningInstant()` builds the instant from the host's own date so no calendar
literal is written into the helper, then `page.clock.install({ time:
eveningInstant() })` pins the browser and `await request.post("/_stub/reset", {
data: { now: eveningInstant() } })` pins the stub. The stub answers from
`nowMs()` on every read, so the world it builds (the dealer's day, the run, the
stops) and the day the page asks for (the same dealer's day, in the house
zone) are anchored to the same instant whatever the wall clock says.

A leg that wants a different instant pins its own on top:

```
await page.clock.setSystemTime(instant);
await resetStub(request, instant);
```

Two instant pins, one day leg each:

- `utcAt(0, 30)` is the previous evening in the dealer's zone: the two days
  disagree, and the run is on the sheet;
- `utcAt(19, 0)` is the middle of the dealer's afternoon: the two days agree,
  and the run is still on the sheet.

A third leg puts the stub on the next UTC day while the page stands on the
evening instant: the dealer's day has no run, and the sheet is empty. Together
the three are red on code that reads `new Date().toISOString().slice(0, 10)`
instead of the house zone, at any wall hour.

## What the wrong code looks like, at the pinned instant

The signs to look for, named so a reviewer can read the failure without
opening the test:

- an invoice's `issue_date` reads the database session's `CURRENT_DATE`
  instead of the house day of the writer's clock;
- a register's `days_past_due` answers `n + 1` for a wanted `n` because the
  due date scanned off a `DATE` column arrives as midnight UTC, which is the
  previous evening in the house zone;
- a statement's lines list is short by every evening transaction because the
  date cutoff compares in the session zone, not the house zone;
- a Promise refused as "already past" for the dealer's day, because the
  comparison truncated `time.Now()` to a UTC date;
- the page asks for `/api/v1/dispatch/my-runs?date=<UTC day>` while the stub's
  world was built on the dealer's day, and the sheet is empty.

## The CI evening guard once it lands

The two evening fix branches agree on one rule: CI runs the date-sensitive
legs a second time with the clock (and `TZ` for the web legs) pinned inside
the evening window, every run, so a daytime run can never hide this class
again. The PR body of the branch that lands it names which suites are
date-sensitive. Until it lands, the leg itself is the guard: a date leg that
runs at `JustAfterUTCDayChange` for the Go tests and at the page-and-stub
evening pin for the Playwright legs is the same red at any wall hour.

## Commands CI runs, quoted exactly

The exact forms the gates use, copied from `.github/workflows/ci.yml` and the
suite runbook, for the legs above:

```
cd core && go test ./... -count=1 -p 1
```

with the demo job's database variables set inline on the command (the
`postgres://postgres:postgres@127.0.0.1:<port>/<name>?sslmode=disable` shape
that `ci.yml`'s `demo` job builds against its own `postgres` service):

```
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_base?sslmode=disable \
HH_CORE_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_core_gate?sslmode=disable \
HH_B3_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:<port>/till_b3_gate?sslmode=disable \
go test ./... -count=1 -p 1
```

CI's whole integration package list, in `go list` order, on one migrated and
seeded database, the `red` exclusion being the single line CI carries:

```
go test -tags integration -p 1 -count=1 $(go list ./... | grep -Ev "/internal/(dispatch)$")
```

The web loop, folder by folder, typecheck then test:

```
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
  elif [ -d "$dir/test" ]; then
    echo "$name has a test directory but no test script; add one" >&2
    exit 1
  fi
done
```

The Playwright fix at the leg level, the page clock and the stub clock both
pinned through the helper:

```
await page.clock.setSystemTime(instant);
await resetStub(request, instant);
```

The stub's own control, the route the helper posts to set its clock:

```
POST /_stub/reset {"now": <epoch ms>}
```

## How to read a red at this hour

When a leg fails inside the evening window and the wall clock of the run is
inside it, the red is the rule working. When the wall clock is outside, the
leg was pinned, and the red is the same defect found at the pinned instant
regardless of the wall clock. Either way, the leg's red is the verdict. The
fix is in the production code or in the SQL it reads, not in the test.
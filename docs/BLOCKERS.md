# Blockers

Lane-by-lane record of anything that blocked or redirected work in this
repository. Newest at the bottom; never edit another lane's entry.

## HH-2.1 (skeleton)

- **No `fb` CLI exists yet.** The lane acceptance calls for `fb check` to exit
  0 on the empty skeleton, but `fb` is a platform deliverable that has not been
  built and there is no template repository to take it from. The lane provides
  `scripts/check-shape.sh` as the in-repo stand-in (it validates the App Shape
  layout, the manifest header, the `ci` job contract and the no-dashes house
  rule) and the `ci` job runs it. When the real `fb check` lands, replace the
  script call in `.github/workflows/ci.yml` and delete this entry.
- **`hardscapeos_dibbits` carries no `CLAUDE.md` on `master` or `Demo`.** The
  lane brief says to read it at the repository root. The file exists only on
  the `fb/*` and `Feedback9_08_swarm` branch tips, byte-identical across all of
  them (blob `3d396ab1`), so the invariants were read from that blob. If a
  different revision was intended, diff `AGENTS.md` against it when it appears.

## HH-2.2: `fb check` cannot be executed yet

Lane HH-2.2's acceptance includes "`fb check` accepts the manifest". The `fb`
CLI does not exist yet: there is no template repository and no platform binary
in this session, and HH-2.1's skeleton (which carries the `ci` workflow and any
first-party check shim) lands on `qa/HH-2` only at integration time.

Mitigation in this lane: the manifest's structural conformance is covered by
`scripts/manifest-catalog.test.ts`, which asserts the App Shape identity fields
(`shape: 1`, `app: hh`, the runtimes list, `database.extensions` with `pg_trgm`
and `citext`), full catalog parity for every frontend, and byte-identical
regeneration. `scripts/generate-manifest.ts --check` is the drift gate the `ci`
job can run once HH-2.1's workflow exists. When a real `fb check` is available
it must be run against `manifest.yaml` and this note removed.

## HH-3 preflight

- **The second provenance header line is now stale on eight frozen files.**
  "content below this header is unchanged" no longer holds for
  `docs/analysis/role-catalog.yaml` (the D23 roles line) or for seven files
  under `docs/analysis/sessions/` (the D25 reassignments in HH-2, HH-4, HH-6,
  HH-7, HH-8, HH-9 and HH-10). The preflight permitted only the D23 and D24
  sentences above those headers, so the line was left as it is. Amending it,
  as `DECISIONS.md` was amended at D20, is Colton's call.
- **Where the D25 reassignment stopped: Colton's review outcome.**
  - Accepted: unassigned approvals, sign-offs and read-backs on the checkpoint lines now name Colton.
  - Accepted: dealer and delivery staff left alone; the sales manager, the finance controller and the droplet operator each give their own sign-off.
  - Accepted: the "switched and observed" outcome lines in HH-6 and HH-10 left alone.
  - Overruled: the HH-4 acceptance lines "Grant can open" and "Grant opens" now name Colton (addendum item 6).

## HH-3 preflight addendum

- **The brief input is the superseded brief, so `docs/sessions/HH-3.md` did
  not change.** `hh3-pre-inputs/session-brief-hh-3.md` (sha256
  `207aeb64b6e1e7dbe55e8d1a9aaf5e1b94354dc6616eb402b60384f8f1c2fa76`) is byte
  for byte the file already committed: it names the preflight as landing D22
  to D25, puts HH-3.5 in Phase C, and still asks whether HH-3.5 is attended.
  The copy was made and `cmp` exits 0, so there is no diff to commit. D26 is
  recorded in `DECISIONS.md`, but its Touches on `docs/sessions/HH-3.md` (the
  phases table, the HH-3.5 lane heading and acceptance, the integrator order,
  the human checkpoints, the harness note) are not applied, because the live
  brief is an input and was not authored here. Until the D26 brief is handed
  over, the live brief contradicts D26 on HH-3.5.

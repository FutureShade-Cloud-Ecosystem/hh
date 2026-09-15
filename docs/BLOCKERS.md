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

- **The decisions input covers D22 to D25; no D26 exists.** The preflight
  prompt names its input `decisions-d22-d26.md` and asks for D22 to D26 to be
  recorded. The file in the preflight inputs is `decisions-d22-d25.md`, and
  its marked block holds D22, D23, D24 and D25 only; the prompt's four
  decision items are those four, and the live HH-3 brief names the preflight
  as landing D22 to D25. The block was appended exactly as it stands and
  nothing was written for a D26. The pull request keeps the title the prompt
  gave. If a D26 was meant, it has not been handed over.
- **The second provenance header line is now stale on eight frozen files.**
  "content below this header is unchanged" no longer holds for
  `docs/analysis/role-catalog.yaml` (the D23 roles line) or for seven files
  under `docs/analysis/sessions/` (the D25 reassignments in HH-2, HH-4, HH-6,
  HH-7, HH-8, HH-9 and HH-10). The preflight permitted only the D23 and D24
  sentences above those headers, so the line was left as it is. Amending it,
  as `DECISIONS.md` was amended at D20, is Colton's call.
- **Where the D25 reassignment stopped, for Colton to overrule.** Item 4 was
  scoped by function to human-checkpoint lines, design-sync lines and Produces
  lines assigning a design review; unattributed approvals, sign-offs and
  read-backs on those lines were attributed to Colton. Left unchanged: the two
  HH-4 acceptance lines describing the kit harness as one "Grant can open" and
  "Grant opens" (acceptance criteria, not a checkpoint, sync or Produces line);
  the checkpoint actions of the dealer's and the delivery's own people (the
  sales manager's difference report in HH-7, the finance controller's tie-out
  in HH-9 and HH-11, the droplet operator's steps in HH-5, HH-11 and HH-12),
  because each is that party's attestation or that party's environment; and
  the outcomes "the yard roles are switched and observed" (HH-6) and "one real
  contractor account is switched and observed" (HH-10).

> Origin: `analysis/BLOCKERS.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# HH-1 blockers and deviations

Recorded as they were met. None stopped the session; each names what was done instead.

## B1. The `context/` directory named by the brief is absent

The brief lists `context/` with ADR-011, ADR-011a, ADR-011b, ADR-013, ADR-014, the harness-routing note, and the App Shape template skeleton as inputs on disk. Neither repository carries a `context/` directory (checked: `/home/user/hh_pro_dibbits/`, `/home/user/hardscapeos_dibbits/`), and no template skeleton exists in either.

What was done: the single-file synthesis `FUTURESHADE-CONTEXT.md` was treated as authoritative for all of the above, per its own opening paragraph ("It is authoritative; the individual ADRs and plans it summarises are the long form"). Where a session file below references an ADR by number, it references the synthesis section that summarises it. Any detail that lives only in the long-form ADRs was not available and is not claimed.

## B2. The start checkpoint (which ERP branches to read) was not answered before reading

The brief's start checkpoint asks Colton to confirm whether unstable ERP branches are excluded from reading. No answer was available in-session.

What was done: only the checked-out working trees were read. For HardscapeOS that is the tree at the `master` head plus one documentation-only commit (a `CLAUDE.md` added by an earlier session on the same branch; it changes no code). For HH Pro that is the `master` head. No other branch was fetched or checked out. In-flight work is described from the notes files and the commit log of those trees only, and is kept under its own heading in each inventory as the brief requires.

## B3. Output location

The brief says `analysis/` goes to the `hh` monorepo skeleton, or to `infra/docs/analysis/` if `hh` does not exist. Neither repository exists in this session's scope.

What was done: per the session instruction, everything is written under `analysis/` on the branch `analysis/hh-1` in the HH Pro repository, as new files only. The first session in the path (the monorepo skeleton) carries a lane that moves this directory into `hh/docs/analysis/` so the path's own record travels with the code it describes.

## B4. Dated identifiers in the repositories

Both repositories contain paths and identifiers that embed dates (some notes files, some migration names, roadmap text in the ERP README). The house rule forbids dates and calendar references in this session's output.

What was done: such files are described by purpose and directory rather than by their dated name, roadmap phases are named in order without their timing, and the ERP's period-boundary rule is described as "resolved in the house time zone" without the calendar noun. Where a citation could only be made by a dated path, the citation was replaced by the directory plus a distinguishing phrase. A reader with the repository open will find the file; the analysis does not reproduce the date.

# Blockers and deviations on `analysis/hh-demo-layer`

The demo and walkthrough layer folded into the path. None stopped the branch; each names what was done instead.

## B5. `rules-check.sh` is not in this repository

The session instruction names the tooling used on `analysis/hh-1` and says to write an equivalent if it is not available. It is not: no `rules-check*` file exists anywhere in `hh_pro_dibbits`, and the branch it was used on left no copy behind.

What was done: an equivalent was written and run over every changed file before every commit. It checks four things, which are the four the instruction names: em and en dashes (with the horizontal bar and the minus sign, since both read as a dash), four-digit years, numeric durations (a number followed by a time noun), and timing or calendar nouns. It was self-tested against a fixture containing one of each before it was trusted, and its output is in the branch's report.

One calibration was needed. The first version treated "cadence" as a timing noun, which flagged two pre-existing lines ("its release cadence") in `transformation-path.md` and `sessions/HH-5-database-convergence-and-seeds.md`. Those files were produced clean by the real tool on `analysis/hh-1`, so the real tool does not flag it, and a replacement that is stricter than the thing it replaces would have forced edits to text this branch was not asked to touch. "cadence" was dropped from the word list; every other term stayed.

The instruction anticipated one clash and it happened: the source wording of the reset guardrail carries a calendar word. Per the instruction the guardrail was kept and phrased as a reset that runs both from a schedule and on demand from an operator action, which is the same guarantee without the calendar noun.

## B6. The attachments were not on disk under `context/`

The starter prompt places `FUTURESHADE-CONTEXT.md` and `hh-first-deployment-and-demo.md` under `context/` in the working directory. This session received them as attachments rather than as files in the checkout, and nothing outside `analysis/` may be committed, so `context/` was neither created nor committed.

What was done: both documents were read in full and treated exactly as the prompt describes, with the context file authoritative. Where a new lane cites the context, it cites it as `context/FUTURESHADE-CONTEXT.md` and a section number, which is the path a session executing that lane will have, and which is already the convention in the lanes written on `analysis/hh-1`.

## B7. The new section's number

The instruction says to add a section "Environments and orgs" to `transformation-path.md`. Inserting it where it reads best, beside the dependency graph, would have renumbered the standing rules, the Gable section and the decisions section, and two live references point at those numbers: `DECISIONS.md` cites "standing rule 4 in `transformation-path.md`" in D5 and "`transformation-path.md` section 6" in its own header.

What was done: the section was appended as section 7 rather than inserted, so every existing cross-reference stays correct and nothing outside the addition changed. The session table in section 3 names each session's demo addition, so a reader arriving at the table is pointed forward.

## B8. Two lanes want the same shell, in two different sessions

HH-7.7 needs a pre-auth public web shell for the demo entry page, and HH-10.3 already names `hh/web/apps/hh-public-shell/**` in its Touches line for `public-desk`, which lands three sessions later.

What was done: HH-7.7 creates the shell and its bundle fence and is told in its own prompt to keep it empty of anything specific to the entry page; HH-10.3 then lands `public-desk` in the shell that exists, which its Touches line already allows for without amendment. HH-7's Unlocks line records the dependency in that direction so the two lanes cannot both believe they own the shell.

## B9. `impl-hh-demo` is a repository this path does not yet have

HH-5.6 creates an implementation repository, which is the one lane in this addition whose Touches are not a path inside the `hh` monorepo. Context section 5 is explicit that an implementation is its own repository and never a directory of the product, so naming a path inside `hh` would have been the wrong answer rather than the convenient one.

What was done: the lane's Touches names the `impl-hh-demo` repository and the files it holds, and names `hh/docs/runbooks/impl-hh-demo.md` in the product repository as the place the pin and the upgrade path are recorded, so the lane still has a file inside `hh` that a verifier can read.

## B10. The review document was an attachment, not a file, and the checkers are session tooling

The fix instruction for the review round cites `context/review-pr2-hh-demo-layer.md` under `context/`. It arrived as an attachment rather than a file in the checkout, and `context/` is not committed on this branch, so the review was read as attached and never written to the repository. The four findings (F1, F2, the sixth guardrail, the HH-11.5 renumber) and D19 were applied from it as handed over.

What was done: the dash and timing checker and the catalog coverage checker were not in this session either, so equivalents were written under `/tmp`, self-tested against fixtures with planted violations (the coverage checker caught a duplicate replaces claim, an unknown shell, an unknown kit, a ghost load and a missing source; the rules checker caught one of each of its four categories), run over every changed file before every commit, and never committed, per the instruction that they stay session tooling. The coverage checker's output is in the branch's fix report, including one finding about the catalog header itself: its micro_apps and launcher_sections counts were stale from the D4 move, which the re-run corrected.

# tauri/hh-work

Tauri 2 shell for office and counter roles (DECISIONS.md D2, plan B.4):
Counter Till, Quote Desk, Dispatch and Load Building, Buying and Stock, Money
Desk, Manager Home, House Messages and Dealer Admin. Desktop and tablet. The
profile data (identifier `ai.futurebuild.hh.work`, product name HH Work, the
apps, their offline modes and scope sets, window defaults, the Work update
channel) lives in the shared core (`tauri/core/src/profiles.rs`) and is
generated from `manifest.yaml` by `scripts/generate-field-profiles.ts`, with
the drift test `scripts/field-profiles.test.ts`.

## Permissions

Work holds no camera and no location permission: those are Field only
(plan B.3). The capability file (`capabilities/default.json`) is per profile
and was written for Work, not copied from `hh-field`: the core's default
window permissions, the two store commands by name, and the updater. Tests
in `src/lib.rs` pin that list, that no keychain command and no open SQL
grant exists, and that the built ACL manifest holds no keychain, probe,
camera or location entry. There is no `probe` feature in this crate.

## Offline (plan B.3)

Read cache plus drafts. The store is the shared core's SQLite executor behind
one fixed file for this profile (`work.sqlite3`, in the app config dir) driven
through `run_statement` and `run_atomic` under the capability allow list. Per
app, from the manifest: Counter Till read cache only (no outbox, D5); Quote
Desk read cache plus an outbox (quotes last write wins by field); Dispatch
Board, Buying and Stock, House Messages read cache plus outbox; Manager Home
read cache only; Money Desk and Dealer Admin online only. Money never queues:
every tender, approval and price change resolves online with an honest "no
signal" state, which is the shell chrome and the apps' work in item 8b.

## What ships inside the profile

`assemble-dist.sh` (run by `beforeBuildCommand`) builds the Work shell host
(`web/apps/work-shell`), the front door and every Work app's web bundle with
the demo image's flags (`deploy/demo/Dockerfile`) and assembles the launcher
entry `dist/`: the host at the root is the entry; the front door is a page
under it at `/door/` (the host's sign in and role picker); each desk app sits
at the path the front door's tile opens; `profile.json` carries the generated
profile data and `launch.json` the `{app id: serve path}` of every bundle
shipped, written from the script's `LAUNCH_MAP`. Apps with no web bundle yet
(House Messages) are skipped with a note. Like Field's, it needs a git
checkout and bash.

## The host (item 8c)

`web/apps/work-shell` is the one page the window opens. It mounts the shell
chrome (`web/packages/shell-chrome`): the header band with the app and surface
selector built from `profile.json` by the signed in role, the pin control, the
sync chip and the account chip with Switch role, the offline banner, and the
collapsed rail of the person's own pins. The role's landing is derived from the
role catalog (a manager role lands on Manager Home, D48 and Q8; every other
role on the first app of its catalog list that its switcher holds). The
selected desk app runs in a frame of the content area, because each app is a
complete bundle at its own base and registers the same custom element names as
the others; the CSP therefore carries `frame-src 'self'` and `frame-ancestors
'self'` (it opens no remote origin). Switch role clears the session and returns
to the front door's picker; pins and per user stores stay for their person.
In the shell the host tries the auth package's controller over the four Rust
commands first (a shell with no Console configuration answers `unavailable`),
then the front door's demo mint.

## The shell proof (feature `shell-proof`)

Off by default and compiled out of every shipped build (the webview cannot turn
it on: it is a cargo feature, and its one command only prints and exits). Built
with `cargo build -p hh-work --features shell-proof` after `assemble-dist.sh`
and run under `xvfb-run -a`, it injects `proof.js` into every frame of the
window, answers the front door's demo mint locally (there is no core in the run)
and holds every other `/api/` call, drives the host through its role leg by the
host's own controls, prints one `HH_SHELL_PROOF` JSON line (the roles tried, the
switcher each saw, the landing, the app switch, the Switch role result) and
exits by itself. The line proves the Tauri webview loaded the host, the front
door and the real desk bundles from the shell's own origin; it does not prove
any desk app past its boot (no core answers).

## Updates

The Work channel: endpoint and verify key sit in `tauri.conf.json` under
`plugins.updater`, both placeholders until the releases path and the signing
pair exist (the release signing key is in FB Vault, never in git).

## Desktop targets

Linux is built and run here. Windows and macOS are configured in
`tauri.conf.json` (`bundle.targets` "all", the Windows WebView2 bootstrapper,
the macOS minimum system version) but not built on this workstation. Tablets
(iPad, Android) share the lib's mobile entry point; no mobile project is
generated yet.

## Sign in

The four contract commands (`auth_sign_in`, `auth_resume`, `auth_exchange`,
`auth_sign_out`, in `src/auth.rs`) mirror Field's over the shared core's
`AuthService` and custody. They are declared in `build.rs` and allowed by name
in the capability for the bundled local origin only. The Console endpoint, the
project id and the core URL are the profile's `AuthConfig`, fixed at build time
from `HH_CONSOLE_ENDPOINT`, `HH_CONSOLE_PROJECT_ID` and `HH_CORE_URL`; a build
without them answers `unavailable`. The keychain service is
`ai.futurebuild.hh.work`; custody is the desktop keyring only (no Android
Keystore for Work). The webview holds no keychain command, and the CSP needs
no network origin because the calls run in Rust.

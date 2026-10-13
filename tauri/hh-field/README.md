# tauri/hh-field

Internal Tauri 2 shell for field roles (DECISIONS.md D2): Yard Pick and
Drive Run, plus House Messages. Phone-first, with camera and GPS permissions
and offline full mode for the declared yard-style scopes. Yard apps
assume gloves, sunlight, one hand, noise.

## Permissions

Camera and fine location are Field only (plan B.3). The Android manifest
names them with their rationale (HH Drive proof of delivery capture, the
driver's stop position); the capture file itself travels through the
FileProvider, whose wry capture root in `gen/android`'s `file_paths.xml`
must stay (pinned by the tests in `src/lib.rs`). The capability file is per
profile: it grants only what the Field window needs, and the Work and Pro
profiles must never copy these grants.

iOS keys, listed in a note only (no iOS build path on this workstation; the
values go into the generated iOS project's Info.plist when a Mac path
exists):

- `NSCameraUsageDescription`: HH Field captures proof of delivery and yard
  photos with the camera.
- `NSLocationWhenInUseUsageDescription`: HH Field reads your position while
  you drive a delivery run, to place the stop you are on.

## Updates

The updater is registered for the Field channel: the endpoint and the verify
key sit in `tauri.conf.json` under `plugins.updater`, and both are
placeholders until the releases path on the demo host and the signing pair
exist. The endpoint is a configuration value of this file (one Field channel
URL carrying `{{target}}`, `{{arch}}` and `{{current_version}}`); the
configuration is pinned by a test in `src/lib.rs`, so no real key material
can enter a tracked file silently. Nothing polls at start: the shell chrome's
update prompt (the shells brief's item 5) is the only caller, through the
`updater:default` grants of this profile's capability.

Release builds (`bundle.createUpdaterArtifacts`) produce the signed update
artifacts. The signing keys are the principal's, held in FB Vault, never in
git: at a release build the principal supplies `TAURI_SIGNING_PRIVATE_KEY`
and `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` as build environment values.

## What ships inside the profile

The micro app bundles ship inside the app, never loaded from a host (plan
B.3): `assemble-dist.sh` builds the front door and every Field app's web
bundle and assembles the launcher entry directory `dist/` that the window
opens (the front door at the root is the entry until the shell chrome
package takes it over; the app bundles sit at the paths the front door's
role tiles open; `profile.json` carries the profile data for the chrome).
The profile data itself (identifier, apps, offline scope sets, permissions,
window defaults, update channel) lives in the shared core
(`tauri/core/src/profiles.rs`) and is generated from `manifest.yaml` by
`scripts/generate-field-profiles.ts`, with a drift test
(`scripts/field-profiles.test.ts`) and the script's `--check`.

Build note: `beforeBuildCommand` in `tauri.conf.json` runs
`tauri/hh-field/assemble-dist.sh`, which finds the repository with
`git rev-parse --show-toplevel`, so a build needs a git checkout and bash,
not a source archive.

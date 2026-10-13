# tauri/hh-field

Internal Tauri 2 shell for field roles (DECISIONS.md D2): Yard Pick and
Drive Run, plus House Messages. Phone-first, with camera and GPS permissions
and offline full mode for the declared yard-style scopes. Yard apps
assume gloves, sunlight, one hand, noise.

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

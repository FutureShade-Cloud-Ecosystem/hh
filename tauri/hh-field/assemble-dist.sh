#!/usr/bin/env bash
# assemble-dist.sh: ships the Field profile's micro app bundles INSIDE the
# app (plan B.3: a shell that loads its pages from the network is not
# offline). It builds the front door and every Field app's web bundle and
# assembles the single launcher entry directory the shell window opens:
#
#   dist/            the launcher entry. Today the front door bundle at the
#                    root is the entry (its role picker and role homes are how
#                    the apps show by role) until the shell chrome package
#                    (web/packages/shell-chrome) takes the entry over.
#   dist/yard/       the Yard Pick bundle, at the path the front door's own
#                    role tile opens.
#   dist/drive/      the HH Drive bundle, same rule.
#   dist/profile.json  the Field profile data (the generated
#                    tauri/core/profiles/field.json) for the chrome to drive.
#
# The web dirs and serve paths are the demo box's own conventions, from
# deploy/demo/Caddyfile.template and deploy/demo/Dockerfile; the build flags
# per app are exactly that Dockerfile's (the same bundles the box serves).
# The launch map is pinned to the manifest and the front door's tile table by
# scripts/field-profiles.test.ts.
#
# A Field app of the manifest with no web app yet (House Messages) is skipped
# with a note: its front door tile is a coming tile until its module lands.
#
# Run by beforeBuildCommand in tauri.conf.json (cwd independent: it resolves
# the repository root itself), or by hand from anywhere:
#   bash tauri/hh-field/assemble-dist.sh
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
DIST="$ROOT/tauri/hh-field/dist"

build_app() {
  # build_app <web app dir> <serve path> <out dir under dist>
  local dir="$1" base="$2" out="$3"
  echo "assemble: building web/apps/$dir at base $base"
  npm --prefix "$ROOT/web/apps/$dir" run build -- --mode demo --base="$base" --outDir "$out" --emptyOutDir
}

rm -rf "$DIST"
mkdir -p "$DIST"

echo "assemble: building the front door (the launcher entry)"
npm --prefix "$ROOT/web/apps/front-door" run build
cp -r "$ROOT/web/apps/front-door/dist/." "$DIST/"

# The launch map: <manifest app id> <web app dir> <serve path>. One line per
# Field app whose web bundle exists; the path must be the front door tile's
# href (pinned by scripts/field-profiles.test.ts).
LAUNCH_MAP=(
  "yard-pick yard-pick /yard/"
  "drive-run drive-run /drive/"
)

for entry in "${LAUNCH_MAP[@]}"; do
  read -r app dir path <<EOF
$entry
EOF
  if [ ! -d "$ROOT/web/apps/$dir" ]; then
    echo "assemble: SKIP $app: no web app at web/apps/$dir (its tile stays a coming tile)"
    continue
  fi
  build_app "$dir" "$path" "$DIST/$path"
done

cp "$ROOT/tauri/core/profiles/field.json" "$DIST/profile.json"
shipped=0
for entry in "${LAUNCH_MAP[@]}"; do
  read -r app dir path <<EOF
$entry
EOF
  [ -d "$DIST/$path" ] && shipped=$((shipped + 1))
done
echo "assemble: wrote $DIST (the launcher entry at the root, $shipped app bundles, profile.json)"

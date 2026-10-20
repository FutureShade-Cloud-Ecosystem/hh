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
# A Field app of the manifest with no web app yet is skipped
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
  "house-messages house-messages /messages/"
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

# The native config the host page reads at boot (PRR-531): where the core and
# the Console are for this build, public facts only, no secret. The webview
# makes no network call itself (the CSP holds no network origin; sign in and
# sign out revocation run on the Rust side), the page reads this only to know
# the base. An empty coreBaseUrl (an unconfigured build) resolves nothing on
# the page, exactly as before. The value is validated here the way the Rust
# side's clean_base_url validates it: https, or loopback http for a dev build.
CORE_URL="${HH_CORE_URL:-}"
CONSOLE_ENDPOINT="${HH_CONSOLE_ENDPOINT:-https://api.futurebuild.ai/v1}"
valid_url() {
  case "$1" in
    https://* | http://localhost:* | http://127.0.0.1:*)
      case "$1" in
        *[![:print:]]* | *'"'* | *'\'* | *'@'* | *'?'* | *'#'*) return 1 ;;
        *) return 0 ;;
      esac
      ;;
    "") return 0 ;;
    *) return 1 ;;
  esac
}
if ! valid_url "$CORE_URL"; then
  echo "assemble: refusing HH_CORE_URL (it must be https, or loopback http for a dev build)" >&2
  exit 1
fi
if ! valid_url "$CONSOLE_ENDPOINT"; then
  echo "assemble: refusing HH_CONSOLE_ENDPOINT (it must be https, or loopback http for a dev build)" >&2
  exit 1
fi
printf '{\n  "coreBaseUrl": "%s",\n  "consoleEndpoint": "%s"\n}\n' "$CORE_URL" "$CONSOLE_ENDPOINT" > "$DIST/config.json"

shipped=0
for entry in "${LAUNCH_MAP[@]}"; do
  read -r app dir path <<EOF
$entry
EOF
  [ -d "$DIST/$path" ] && shipped=$((shipped + 1))
done
echo "assemble: wrote $DIST (the launcher entry at the root, $shipped app bundles, profile.json)"

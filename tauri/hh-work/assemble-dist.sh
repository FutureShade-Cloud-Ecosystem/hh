#!/usr/bin/env bash
# assemble-dist.sh: ships the Work profile's desk app bundles INSIDE the app
# (plan B.3 and B.4: a shell that loads its pages from the network is not
# offline). It builds the Work shell host, the front door and every Work app's
# web bundle and assembles the single launcher entry directory the shell
# window opens:
#
#   dist/            the launcher entry: the Work shell host
#                    (web/apps/work-shell). It mounts the shell chrome
#                    (web/packages/shell-chrome), builds the app switcher from
#                    profile.json by the signed in role, lands each role on its
#                    own app and hosts the selected desk app in a frame.
#   dist/door/       the front door bundle, a page under the host: the sign in
#                    and the role picker the host shows until a role is
#                    signed in (the browser demo box keeps it at its root).
#   dist/till/ ...   each desk app bundle, at the path the front door's own
#                    role tile opens.
#   dist/profile.json  the Work profile data (the generated
#                    tauri/core/profiles/work.json) for the host to drive.
#   dist/launch.json   { app id: serve path } for every bundle shipped, written
#                    from the LAUNCH_MAP below, so the host offers only apps
#                    that have a bundle and opens each at its path.
#
# The web dirs and serve paths are the demo box's own conventions, from
# deploy/demo/Caddyfile.template and deploy/demo/Dockerfile; the build flags
# per app are exactly that Dockerfile's (the same bundles the box serves). The
# launch map is pinned to the manifest, the front door's tile table and the
# Dockerfile's build lines by scripts/field-profiles.test.ts.
#
# A Work app of the manifest with no web app yet is not in the launch map: its
# front door tile is a coming tile until its module lands, and the map grows
# with it (the test turns red when an app gets a web bundle and the map does
# not name it).
#
# Run by beforeBuildCommand in tauri.conf.json (cwd independent: it resolves
# the repository root itself), or by hand from anywhere:
#   bash tauri/hh-work/assemble-dist.sh
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
DIST="$ROOT/tauri/hh-work/dist"

build_app() {
  # build_app <web app dir> <serve path> <out dir under dist>
  local dir="$1" base="$2" out="$3"
  echo "assemble: building web/apps/$dir at base $base"
  npm --prefix "$ROOT/web/apps/$dir" run build -- --mode demo --base="$base" --outDir "$out" --emptyOutDir
}

rm -rf "$DIST"
mkdir -p "$DIST"

echo "assemble: building the Work shell host (the launcher entry)"
npm --prefix "$ROOT/web/apps/work-shell" run build -- --outDir "$DIST" --emptyOutDir

# The front door is a page under the host, at /door/ (the host mounts it as
# its sign in and role picker). Its tiles open the desk apps at the launch
# paths below; the host does not use them, the picker is what it needs.
build_app front-door /door/ "$DIST/door/"

# The launch map: <manifest app id> <web app dir> <serve path>. One line per
# Work app whose web bundle exists; the path must be the front door tile's
# href (pinned by scripts/field-profiles.test.ts).
LAUNCH_MAP=(
  "counter-till counter-till /till/"
  "quote-desk quote-desk /quote/"
  "dispatch-board dispatch-board /dispatch/"
  "buying-and-stock buying-and-stock /buying-and-stock/"
  "money-desk money-desk /money-desk/"
  "manager-home manager-home /home/"
  "house-messages house-messages /messages/"
  "dealer-admin dealer-admin /dealer-admin/"
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

cp "$ROOT/tauri/core/profiles/work.json" "$DIST/profile.json"

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
launch_json="{"
for entry in "${LAUNCH_MAP[@]}"; do
  read -r app dir path <<EOF
$entry
EOF
  if [ -d "$DIST/$path" ]; then
    shipped=$((shipped + 1))
    [ "$launch_json" = "{" ] || launch_json="$launch_json,"
    launch_json="$launch_json\"$app\":\"$path\""
  fi
done
printf '%s}\n' "$launch_json" > "$DIST/launch.json"
echo "assemble: wrote $DIST (the host at the root, the front door at /door/, $shipped app bundles, profile.json, launch.json)"

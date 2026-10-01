#!/usr/bin/env bash
# The logic of .github/workflows/sites-placeholder-deploy.yml, one phase per
# workflow step, kept in a script so shellcheck reads it and every phase runs
# on a workstation against local stand-ins before the workflow ever runs on
# GitHub (Console 2, CON-2.4, lane W4.4; the steps it serves are S3, S4 and S7
# of the Sites preparation, con-2.4-sites-prep.md r4 in the infra repository).
#
# It deploys the design system harness pages, served as committed (four
# index*.html pages and sheets/, no build step), to the FB Console site
# sites-proof with the Appwrite CLI, then proves the deploy from outside by
# polling the public page until its sha256 equals the committed index.html.
# The deploy key holds sites.write and nothing else, so it cannot read a
# deployment's status; the public page is the only proof it can reach.
#
#   bash scripts/sites-placeholder-deploy.sh gate|install|deploy|verify
#
# Environment, set by the workflow:
#   gate:    KEY_SET (true or false, whether the secret exists; the secret
#            itself never reaches this phase), APPWRITE_PROJECT_ID, GITHUB_REF
#   install: RUNNER_TEMP
#   deploy:  APPWRITE_SITES_KEY (this phase only), APPWRITE_ENDPOINT,
#            APPWRITE_PROJECT_ID, APPWRITE_CLI (written by install), SITE_ID,
#            CREATE_SITE (true only on the first run, step S3), RUNNER_TEMP
#   verify:  SITE_URL, POLL_ATTEMPTS, POLL_WAIT_SECONDS
# GITHUB_OUTPUT, GITHUB_ENV and GITHUB_STEP_SUMMARY are used when set.
set -euo pipefail

HARNESS=web/packages/design-system/harness
CLI_VERSION=27.3.0

say() { printf 'sites-placeholder: %s\n' "$*"; }
fail() { printf 'sites-placeholder: FAIL: %s\n' "$*" >&2; exit 1; }
summary() { if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then printf '%s\n' "$*" >> "$GITHUB_STEP_SUMMARY"; fi; }

# Safe to merge before act P4.4: with no key there is nothing to deploy, so
# the run ends green and says why, and every later step is skipped.
phase_gate() {
  if [ "${KEY_SET:-false}" != "true" ]; then
    say "the repository secret APPWRITE_SITES_KEY is not set, so nothing is deployed."
    say "It is set at act P4.4 (step S2 of the Sites preparation), with the repository variable APPWRITE_HH_PROJECT_ID."
    summary "### Sites placeholder deploy: skipped"
    summary ""
    summary "The repository secret \`APPWRITE_SITES_KEY\` is not set, so nothing was deployed. It is set at act P4.4 (step S2 of the Sites preparation), with the repository variable \`APPWRITE_HH_PROJECT_ID\`."
    if [ -n "${GITHUB_OUTPUT:-}" ]; then echo "ready=false" >> "$GITHUB_OUTPUT"; fi
    return 0
  fi
  # A key without its project is half a configuration: refuse it by name.
  [ -n "${APPWRITE_PROJECT_ID:-}" ] \
    || fail "the secret APPWRITE_SITES_KEY is set but the repository variable APPWRITE_HH_PROJECT_ID is empty; set it to the HH project's id."
  # The preparation deploys from main (step S4); a dispatch from any other
  # branch would put unreviewed files on the public host with the same key.
  [ "${GITHUB_REF:-}" = "refs/heads/main" ] \
    || fail "dispatched from ${GITHUB_REF:-an unknown ref}; this workflow deploys only from refs/heads/main."
  say "the key and the project are configured; deploying from main."
  if [ -n "${GITHUB_OUTPUT:-}" ]; then echo "ready=true" >> "$GITHUB_OUTPUT"; fi
}

# The CLI is installed by npm ci from the manifest and lockfile below, so npm
# checks the sha512 integrity of both packages it fetches: appwrite-cli 27.3.0
# (the launcher, the integrity the preparation records) and
# appwrite-cli-linux-x64 27.3.0 (the native binary the launcher runs). The
# lockfile carries only the linux x64 platform package because the job runs on
# ubuntu x64. The version check after the install is part of the integrity
# check, not a courtesy: npm ci refuses a launcher whose bytes differ from the
# lockfile (EINTEGRITY), but a platform package that fails its integrity is an
# optional dependency, which npm skips with a warning and exits 0; the launcher
# then finds no binary and the version check fails the step.
phase_install() {
  local dir="${RUNNER_TEMP:?}/appwrite-cli"
  rm -rf "$dir"
  mkdir -p "$dir"
  cat > "$dir/package.json" <<'JSON'
{
  "name": "sites-placeholder-cli",
  "private": true,
  "dependencies": {
    "appwrite-cli": "27.3.0"
  }
}
JSON
  cat > "$dir/package-lock.json" <<'JSON'
{
  "name": "sites-placeholder-cli",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "sites-placeholder-cli",
      "dependencies": {
        "appwrite-cli": "27.3.0"
      }
    },
    "node_modules/appwrite-cli": {
      "version": "27.3.0",
      "resolved": "https://registry.npmjs.org/appwrite-cli/-/appwrite-cli-27.3.0.tgz",
      "integrity": "sha512-kWgP0o3QcaDJpZblVgYRiGlpz+8mt1pRNjI2gYNTiQlv1A/JoScfKtj5NgGjcdCj+JQzH4wLH4MGZqIgCzbtuw==",
      "license": "BSD-3-Clause",
      "bin": {
        "appwrite": "run.js"
      },
      "engines": {
        "node": ">=18"
      },
      "optionalDependencies": {
        "appwrite-cli-darwin-arm64": "27.3.0",
        "appwrite-cli-darwin-x64": "27.3.0",
        "appwrite-cli-linux-arm64": "27.3.0",
        "appwrite-cli-linux-x64": "27.3.0",
        "appwrite-cli-win-arm64": "27.3.0",
        "appwrite-cli-win-x64": "27.3.0"
      }
    },
    "node_modules/appwrite-cli-linux-x64": {
      "version": "27.3.0",
      "resolved": "https://registry.npmjs.org/appwrite-cli-linux-x64/-/appwrite-cli-linux-x64-27.3.0.tgz",
      "integrity": "sha512-CJO9SkM9eMLbsB8/mRRF3TDsb/ZYVueqBI9bE+uOxNmn8NxmOCizmO1PYv+hY/s2K4DF3H9xBBOiowDPt4VaLw==",
      "cpu": [
        "x64"
      ],
      "license": "BSD-3-Clause",
      "optional": true,
      "os": [
        "linux"
      ]
    }
  }
}
JSON
  npm ci --prefix "$dir" --ignore-scripts --no-audit --no-fund
  local cli="$dir/node_modules/.bin/appwrite"
  local version
  version="$(APPWRITE_NO_UPDATE_CHECK=1 "$cli" --version)"
  [ "$version" = "appwrite version $CLI_VERSION" ] \
    || fail "the installed CLI reports '$version', expected 'appwrite version $CLI_VERSION'"
  say "installed $version, integrity checked by npm ci against the lockfile"
  if [ -n "${GITHUB_ENV:-}" ]; then echo "APPWRITE_CLI=$cli" >> "$GITHUB_ENV"; fi
}

# The key reaches the CLI through the CLI's own preferences file, never through
# an argument: `appwrite client --key` would put it on a command line, where any
# process listing on the runner could read it. The file is the one the CLI
# itself writes (prefs.json, session "default", fields endpoint and key), made
# mode 0600 under a HOME of this step's own, by node reading the key from its
# environment; the key is then unset, so no CLI process carries it. The HOME
# is removed by this script's EXIT trap when the script ends, fails, or takes
# SIGINT or SIGTERM (the workflow execs the script, so a cancel's signals reach
# it and not a parent shell), and again by the workflow's last step, which runs
# always and covers a SIGKILL the trap cannot see. The project comes
# from APPWRITE_PROJECT_ID, which the CLI reads from its environment. No flag
# that prints credentials (--show-secrets, --verbose, --report, client --debug)
# is passed.
phase_deploy() {
  : "${APPWRITE_SITES_KEY:?the key is not in the environment of this step}"
  : "${APPWRITE_ENDPOINT:?}" "${APPWRITE_PROJECT_ID:?}" "${APPWRITE_CLI:?}" "${SITE_ID:?}" "${RUNNER_TEMP:?}"
  local work="$RUNNER_TEMP/sites-proof"
  # Global, not local: the EXIT trap runs after this function has returned.
  CLI_HOME="$RUNNER_TEMP/appwrite-home"
  rm -rf "$work" "$CLI_HOME"
  trap 'rm -rf "$CLI_HOME"' EXIT
  mkdir -p "$work/site" "$CLI_HOME/.appwrite"
  chmod 700 "$CLI_HOME" "$CLI_HOME/.appwrite"

  APPWRITE_PREFS="$CLI_HOME/.appwrite/prefs.json" node -e '
    const fs = require("node:fs");
    const prefs = { current: "default", default: { endpoint: process.env.APPWRITE_ENDPOINT, key: process.env.APPWRITE_SITES_KEY } };
    fs.writeFileSync(process.env.APPWRITE_PREFS, JSON.stringify(prefs), { mode: 0o600 });
  '
  unset APPWRITE_SITES_KEY

  # The placeholder, served as committed: the four harness pages and the
  # sheets they link, copied as they stand in this checkout (no build step;
  # the design system's suite holds the committed bytes to a fresh build).
  cp "$HARNESS"/index*.html "$work/site/"
  cp -R "$HARNESS/sheets" "$work/site/sheets"
  say "the site's files:"
  (cd "$work/site" && find . -type f | sort)

  export HOME="$CLI_HOME" APPWRITE_NO_UPDATE_CHECK=1
  cd "$work"
  if [ "${CREATE_SITE:-false}" = "true" ]; then
    # Step S3, once: the first dispatch creates the site; a later dispatch
    # with this input set fails here, because the site exists.
    say "creating the site $SITE_ID (step S3)"
    "$APPWRITE_CLI" sites create --site-id "$SITE_ID" --name "Sites proof" \
      --framework other --build-runtime static-1 --adapter static --output-directory ./
  fi
  # Step S4. Not `appwrite push sites`: it needs sites.read and rules.read,
  # and without rules.read it prints "No sites were pushed" and exits 0.
  say "uploading a deployment of $SITE_ID and activating it when built (step S4)"
  "$APPWRITE_CLI" sites create-deployment --site-id "$SITE_ID" --code ./site --activate true
  summary "### Sites placeholder deploy"
  summary ""
  summary "A deployment of \`$SITE_ID\` was uploaded with activation on build (step S4)."
}

# Step S7's byte check, bounded: POLL_ATTEMPTS fetches of SITE_URL, waiting
# POLL_WAIT_SECONDS between them, each fetch itself capped by curl. The page
# matches when its sha256 equals the committed index.html's.
phase_verify() {
  : "${SITE_URL:?}"
  local attempts="${POLL_ATTEMPTS:-30}" wait="${POLL_WAIT_SECONDS:-10}"
  [[ "$attempts" =~ ^[1-9][0-9]*$ ]] || fail "POLL_ATTEMPTS must be a positive whole number, got '$attempts'"
  [[ "$wait" =~ ^[0-9]+$ ]] || fail "POLL_WAIT_SECONDS must be a whole number, got '$wait'"
  local expected got i
  expected="$(sha256sum < "$HARNESS/index.html" | cut -d' ' -f1)"
  say "the committed index.html: sha256 $expected"
  for ((i = 1; i <= attempts; i++)); do
    if got="$(curl -fsS --max-time 20 "$SITE_URL" | sha256sum | cut -d' ' -f1)"; then
      if [ "$got" = "$expected" ]; then
        say "attempt $i of $attempts: $SITE_URL serves the committed index.html (sha256 $got)"
        summary "The public page \`$SITE_URL\` serves the committed \`index.html\` (sha256 \`$got\`), attempt $i of $attempts."
        return 0
      fi
      say "attempt $i of $attempts: $SITE_URL served sha256 $got, not the committed one"
    else
      got="no page"
      say "attempt $i of $attempts: $SITE_URL gave no page"
    fi
    if [ "$i" -lt "$attempts" ]; then sleep "$wait"; fi
  done
  summary "**The page check failed:** after $attempts attempts \`$SITE_URL\` did not serve the committed \`index.html\` (last: $got)."
  summary ""
  summary "Expected on the first run: the page cannot answer until the custom domain (step S5) and the proxy route (step S6) are in place. After S6, dispatch this workflow again (step S7)."
  fail "the served page never matched the committed index.html in $attempts attempts (last: $got). Expected on the first run, before steps S5 and S6; after S6, dispatch again (step S7)."
}

cd "$(git rev-parse --show-toplevel)"
case "${1:-}" in
  gate) phase_gate ;;
  install) phase_install ;;
  deploy) phase_deploy ;;
  verify) phase_verify ;;
  *) fail "usage: $0 gate|install|deploy|verify" ;;
esac

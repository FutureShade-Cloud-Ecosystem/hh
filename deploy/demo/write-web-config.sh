#!/bin/sh
# The box's web runtime config (/srv/app/config.json), written at boot from
# named public variables, so one image serves every copy without a rebuild.
# Sourced by the demo entrypoint; it defines write_web_config and runs nothing.
#
#   write_web_config PATH
#
# When HH_WEB_MODE is unset or demo the file is left exactly as the image
# baked it. In console mode the variables are validated with the same rules
# as the browser's parser (web/packages/auth/src/runtime-config.ts), and a
# refusal names the variable and never repeats its value. The file holds
# public facts only: never put a key, a token or a secret in these names.
#
#   HH_WEB_MODE                 demo | console
#   HH_WEB_ORG                  org slug, ^[a-z0-9][a-z0-9-]{0,62}$
#   HH_WEB_CONSOLE_ENDPOINT     FB Console API endpoint, https (or local http)
#   HH_WEB_CONSOLE_PROJECT_ID   the HH project id in FB Console
#   HH_WEB_CORE_BASE_URL        optional, https (or local http)
#   HH_WEB_PERSONAS             optional, true | false
#   HH_WEB_PERSONAS_OPEN        optional, true | false (the public copy)
#   HH_WEB_PERSONA_CALLER       optional, public | partner; public iff open
#   HH_WEB_DEALER_NAME          the dealer's name
#   HH_WEB_DEALER_MARK          optional, a built in mark code

wc_refuse() {
	echo "entrypoint: $1 $2" >&2
	return 1
}

# wc_get NAME: the value, empty when unset or empty.
wc_get() { printenv "$1" || true; }

# wc_plain NAME VALUE: no control character (a newline included) in a value.
wc_plain() {
	[ "$(printf '%s' "$2" | tr -d '\000-\037\177' | wc -c)" -eq "$(printf '%s' "$2" | wc -c)" ] || \
		wc_refuse "$1" "holds a control character: the value must be one plain line"
}

# wc_url NAME VALUE: https, or http on localhost or 127.0.0.1, no whitespace.
wc_url() {
	wc_plain "$1" "$2" || return 1
	if printf '%s' "$2" | grep -Eq '^https://[^/?#[:space:]]+([/?#][^[:space:]]*)?$'; then return 0; fi
	if printf '%s' "$2" | grep -Eq '^http://(localhost|127\.0\.0\.1)(:[0-9]+)?([/?#][^[:space:]]*)?$'; then return 0; fi
	wc_refuse "$1" "is not an https URL (http is accepted only for localhost and 127.0.0.1)"
}

# wc_bool NAME VALUE: true or false, or empty (false).
wc_bool() {
	case "$2" in
		""|true|false) return 0 ;;
		*) wc_refuse "$1" "must be true or false" ;;
	esac
}

# wc_json VALUE: the value as a JSON string literal body.
wc_json() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }

write_web_config() {
	wc_path="$1"
	wc_mode="$(wc_get HH_WEB_MODE)"
	case "$wc_mode" in
		""|demo)
			for wc_name in HH_WEB_ORG HH_WEB_CONSOLE_ENDPOINT HH_WEB_CONSOLE_PROJECT_ID HH_WEB_CORE_BASE_URL \
				HH_WEB_PERSONAS HH_WEB_PERSONAS_OPEN HH_WEB_PERSONA_CALLER HH_WEB_DEALER_NAME HH_WEB_DEALER_MARK; do
				if [ -n "$(wc_get "$wc_name")" ]; then
					wc_refuse "$wc_name" "is set but HH_WEB_MODE is not console: set HH_WEB_MODE=console, or unset it"
					return 1
				fi
			done
			return 0
			;;
		console) ;;
		*)
			wc_refuse HH_WEB_MODE "must be demo or console"
			return 1
			;;
	esac

	wc_org="$(wc_get HH_WEB_ORG)"
	wc_endpoint="$(wc_get HH_WEB_CONSOLE_ENDPOINT)"
	wc_project="$(wc_get HH_WEB_CONSOLE_PROJECT_ID)"
	wc_core="$(wc_get HH_WEB_CORE_BASE_URL)"
	wc_personas="$(wc_get HH_WEB_PERSONAS)"
	wc_open="$(wc_get HH_WEB_PERSONAS_OPEN)"
	wc_caller="$(wc_get HH_WEB_PERSONA_CALLER)"
	wc_dealer="$(wc_get HH_WEB_DEALER_NAME)"
	wc_mark="$(wc_get HH_WEB_DEALER_MARK)"

	for wc_name in HH_WEB_ORG HH_WEB_CONSOLE_ENDPOINT HH_WEB_CONSOLE_PROJECT_ID HH_WEB_DEALER_NAME; do
		if [ -z "$(wc_get "$wc_name")" ]; then
			wc_refuse "$wc_name" "is not set: console mode needs it"
			return 1
		fi
	done

	wc_plain HH_WEB_ORG "$wc_org" || return 1
	printf '%s' "$wc_org" | grep -Eq '^[a-z0-9][a-z0-9-]{0,62}$' || { wc_refuse HH_WEB_ORG "is not an org slug (lowercase letters, digits and dashes, starting with a letter or digit)"; return 1; }
	wc_url HH_WEB_CONSOLE_ENDPOINT "$wc_endpoint" || return 1
	wc_plain HH_WEB_CONSOLE_PROJECT_ID "$wc_project" || return 1
	if [ -n "$wc_core" ]; then wc_url HH_WEB_CORE_BASE_URL "$wc_core" || return 1; fi
	wc_bool HH_WEB_PERSONAS "$wc_personas" || return 1
	wc_bool HH_WEB_PERSONAS_OPEN "$wc_open" || return 1
	case "$wc_caller" in
		"") ;;
		public)
			[ "$wc_open" = true ] || { wc_refuse HH_WEB_PERSONA_CALLER "is public but HH_WEB_PERSONAS_OPEN is not true: the two must agree"; return 1; }
			;;
		partner)
			[ "$wc_open" != true ] || { wc_refuse HH_WEB_PERSONA_CALLER "is partner but HH_WEB_PERSONAS_OPEN is true: the two must agree"; return 1; }
			;;
		*) wc_refuse HH_WEB_PERSONA_CALLER "must be public or partner"; return 1 ;;
	esac
	wc_plain HH_WEB_DEALER_NAME "$wc_dealer" || return 1
	[ -n "$(printf '%s' "$wc_dealer" | tr -d '[:space:]')" ] || { wc_refuse HH_WEB_DEALER_NAME "is blank"; return 1; }
	wc_plain HH_WEB_DEALER_MARK "$wc_mark" || return 1

	[ "$wc_personas" = true ] || wc_personas=false
	[ "$wc_open" = true ] || wc_open=false

	wc_tmp="${wc_path}.tmp.$$"
	{
		printf '{"mode":"console","org":"%s","consoleEndpoint":"%s","consoleProjectId":"%s"' \
			"$(wc_json "$wc_org")" "$(wc_json "$wc_endpoint")" "$(wc_json "$wc_project")"
		[ -z "$wc_core" ] || printf ',"coreBaseUrl":"%s"' "$(wc_json "$wc_core")"
		printf ',"personas":%s,"personasOpen":%s' "$wc_personas" "$wc_open"
		[ -z "$wc_caller" ] || printf ',"personaCaller":"%s"' "$wc_caller"
		printf ',"dealer":{"name":"%s"' "$(wc_json "$wc_dealer")"
		[ -z "$wc_mark" ] || printf ',"mark":"%s"' "$(wc_json "$wc_mark")"
		printf '}}'
	} > "$wc_tmp" && mv "$wc_tmp" "$wc_path" || { rm -f "$wc_tmp"; wc_refuse "the web config" "could not be written to its path"; return 1; }
}

#!/bin/sh
# The finance box's sign in allow list, derived from HH_FINANCE_USERS: one
# source of truth for the people on the box. Sourced by the finance
# entrypoint; it defines derive_signin_allow_list and runs nothing. The
# entrypoint calls the function before its variable checks, so the allow list
# is derived from the box's list of people instead of being set beside it.
# Two sources that disagree refuse the boot, naming the variables and never
# the values: an address is a person's, not a value to print.
#
# The list's shape: the entries are separated by ';', each one
# 'email|platform_user_id|display name|role'. A field cannot hold a ';' or a
# '|', so a display name is a display name with spaces and no pipes.

# allow_list_refuse prints one refusal line and fails the caller. The line
# names variables and rules, never an address.
allow_list_refuse() {
	echo "entrypoint: $*" >&2
	return 1
}

# normalize_allow_list folds a comma separated list into the sorted, case
# folded, duplicate free form two lists are compared in.
normalize_allow_list() {
	printf '%s' "$1" | tr ',;' '\n\n' | tr 'A-Z' 'a-z' \
		| sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e '/^$/d' \
		| LC_ALL=C sort -u
}

# derive_signin_allow_list derives CORE_SIGNIN_EMAIL_ADDRESSES from
# HH_FINANCE_USERS and exports it. With no list the allow list is left as it
# is: the entrypoint's required name check still asks for it. With a list and
# an allow list that disagree the boot stops, naming the two variables and
# never an address.
derive_signin_allow_list() {
	users="${HH_FINANCE_USERS:-}"
	if [ -z "$users" ]; then
		return 0
	fi
	derived=""
	# The entries, split on ';'. The separator is the only thing that
	# splits, so a display name with spaces stays whole, and the globbing
	# is off so a name that holds a '*' is not a file name.
	saved_ifs="$IFS"
	IFS=';'
	set -f
	set -- $users
	set +f
	IFS="$saved_ifs"
	for entry in "$@"; do
		if [ -z "$entry" ]; then
			continue
		fi
		saved_ifs="$IFS"
		IFS='|'
		set -f
		set -- $entry
		set +f
		IFS="$saved_ifs"
		if [ "$#" -ne 4 ]; then
			allow_list_refuse "HH_FINANCE_USERS has an entry that is not email|platform_user_id|display name|role: the box's finance users cannot be read, and the entrypoint refuses to start"
			return 1
		fi
		email="$1"
		if [ -z "$email" ]; then
			allow_list_refuse "HH_FINANCE_USERS has an entry with an empty email: the box's finance users cannot be read, and the entrypoint refuses to start"
			return 1
		fi
		if [ -z "$2" ] || [ -z "$3" ] || [ -z "$4" ]; then
			allow_list_refuse "HH_FINANCE_USERS has an entry with an empty field: the box's finance users cannot be read, and the entrypoint refuses to start"
			return 1
		fi
		folded=$(printf '%s' "$email" | tr 'A-Z' 'a-z')
		case ",$derived," in
			*",$folded,"*) continue ;;
		esac
		if [ -z "$derived" ]; then
			derived="$folded"
		else
			derived="$derived,$folded"
		fi
	done
	if [ -z "$derived" ]; then
		allow_list_refuse "HH_FINANCE_USERS holds no entry: the box's finance users are applied from it, and the entrypoint refuses to start without it"
		return 1
	fi
	if [ -n "${CORE_SIGNIN_EMAIL_ADDRESSES:-}" ]; then
		if [ "$(normalize_allow_list "$derived")" != "$(normalize_allow_list "$CORE_SIGNIN_EMAIL_ADDRESSES")" ]; then
			allow_list_refuse "CORE_SIGNIN_EMAIL_ADDRESSES and HH_FINANCE_USERS disagree on the sign in allow list: the box's finance users are one source of truth, and the entrypoint refuses to start on two"
			return 1
		fi
	fi
	CORE_SIGNIN_EMAIL_ADDRESSES="$derived"
	export CORE_SIGNIN_EMAIL_ADDRESSES
	return 0
}

#!/bin/sh
# database-url.sh: the box's database URL, parsed once and never on a command
# line.
#
# psql reads its connection information from the environment or from a libpq
# service file, and a URL passed as an argument sits in the process list of
# every process in the container, where anything that can read that list can
# read the database's password. The URL is parsed here into a libpq service
# file on the shared memory tmpfs instead: the file is mode owner only, the
# probe alone reads it, the children never inherit it, and it is removed as
# soon as the probe is done. The password never reaches a command line, never
# reaches a child's environment, and is never printed.
#
# The parser reads the URL's shape exactly:
#
#   postgres://[user[:password]@]host[:port]/database[?params]
#
# and refuses anything it cannot read, naming DATABASE_URL and never a
# value: a URL this file cannot parse must never reach psql as an argument,
# and a URL this file reads wrong must never reach psql at all.
#
# The parse follows libpq's own reading of a URI: the userinfo ends at the
# LAST '@' (so a password may carry a raw '@'), the user and the password
# split at the FIRST ':', a bracketed host carries its port after the ']',
# and every parameter after the '?' is a libpq connection parameter whose
# name is decoded (libpq reads the URI's names decoded) and checked for a
# line break, raw and encoded, before it is written into the service file.

# The libpq service name the probe asks for. It names the service, not the
# target, so it says nothing about the database.
PG_SERVICE_NAME="finance-box"

# pg_url_decode VALUE prints VALUE with its percent escapes decoded (the
# URI's own encoding). The decoder is byte oriented (the C locale), so a
# multi byte character comes back as the bytes it was encoded from.
pg_url_decode() {
	printf '%s' "$1" | LC_ALL=C awk '
		function hexchar(h,   v, d, i, c) {
			v = 0
			for (i = 1; i <= 2; i++) {
				c = substr(h, i, 1)
				d = index("0123456789abcdef", tolower(c)) - 1
				v = v * 16 + d
			}
			return sprintf("%c", v)
		}
		{
			s = $0
			n = length(s)
			out = ""
			for (i = 1; i <= n; i++) {
				c = substr(s, i, 1)
				if (c == "%" && i + 2 <= n) {
					h = substr(s, i + 1, 2)
					if (h ~ /^[0-9A-Fa-f][0-9A-Fa-f]$/) {
						out = out hexchar(h)
						i += 2
						continue
					}
				}
				out = out c
			}
			printf "%s", out
		}'
}

# pg_url_value_is_one_line answers whether a value can be written into the
# service file as one line: a line break or a carriage return would break
# the file's shape, and the file is the only place the password lives.
pg_url_value_is_one_line() {
	case "$1" in
		*'
'*|*"$(printf '\r')"*)
			return 1
			;;
	esac
	return 0
}

# pg_service_write PATH DATABASE_URL writes the libpq service file at PATH
# (mode owner only) and returns zero. It returns one and sets PG_URL_ERROR
# to a one line reason when the URL cannot be read; the reason names the
# variable and never a value.
pg_service_write() {
	PG_URL_ERROR=""
	pg_path="$1"
	pg_url="$2"

	case "$pg_url" in
		postgres://*|postgresql://*)
			pg_rest="${pg_url#*://}"
			;;
		*)
			PG_URL_ERROR="DATABASE_URL must be a postgres URL (postgres:// or postgresql://)"
			return 1
			;;
	esac

	# the query string: everything after the first question mark. The
	# bracket keeps the question mark literal.
	pg_query=""
	case "$pg_rest" in
		*[?]*)
			pg_query="${pg_rest#*[?]}"
			pg_rest="${pg_rest%%[?]*}"
			;;
	esac

	# the userinfo ends at the last '@', so a password may carry a raw '@'.
	pg_userinfo=""
	case "$pg_rest" in
		*@*)
			pg_userinfo="${pg_rest%@*}"
			pg_hostport="${pg_rest##*@}"
			;;
		*)
			pg_hostport="$pg_rest"
			;;
	esac

	# the path: the database name.
	case "$pg_hostport" in
		*/*)
			pg_authority="${pg_hostport%%/*}"
			pg_database="${pg_hostport#*/}"
			;;
		*)
			pg_authority="$pg_hostport"
			pg_database=""
			;;
	esac
	if [ -z "$pg_database" ]; then
		PG_URL_ERROR="DATABASE_URL must name a database (the path after the host)"
		return 1
	fi

	# the host and the port. A bracketed host carries its port after the ']'.
	pg_port=""
	case "$pg_authority" in
		\[*\]*)
			pg_host="${pg_authority%%\]*}"
			pg_host="${pg_host#\[}"
			pg_tail="${pg_authority#*\]}"
			case "$pg_tail" in
				:*) pg_port="${pg_tail#:}" ;;
			esac
			;;
		*:*)
			pg_host="${pg_authority%%:*}"
			pg_port="${pg_authority##*:}"
			;;
		*)
			pg_host="$pg_authority"
			;;
	esac
	if [ -z "$pg_host" ]; then
		PG_URL_ERROR="DATABASE_URL must name a host"
		return 1
	fi
	if [ -n "$pg_port" ]; then
		case "$pg_port" in
			*[!0-9]*)
				PG_URL_ERROR="DATABASE_URL's port must be digits"
				return 1
				;;
		esac
	fi

	# the user and the password split at the first ':'.
	pg_user=""
	pg_password=""
	if [ -n "$pg_userinfo" ]; then
		case "$pg_userinfo" in
			*:*)
				pg_user="${pg_userinfo%%:*}"
				pg_password="${pg_userinfo#*:}"
				;;
			*)
				pg_user="$pg_userinfo"
				;;
		esac
	fi

	# the parameters: each one is a libpq connection parameter, written into
	# the service file under its own name.
	pg_params=""
	pg_remaining="$pg_query"
	while [ -n "$pg_remaining" ]; do
		case "$pg_remaining" in
			*[\&]*)
				pg_pair="${pg_remaining%%[\&]*}"
				pg_remaining="${pg_remaining#*[\&]}"
				;;
			*)
				pg_pair="$pg_remaining"
				pg_remaining=""
				;;
		esac
		case "$pg_pair" in
			*=*)
				pg_key="${pg_pair%%=*}"
				pg_value="${pg_pair#*=}"
				;;
			*)
				pg_key="$pg_pair"
				pg_value=""
				;;
		esac
		# The name is read twice: once as it stands, once decoded. The raw
		# check is the line break check the name had never had, and it comes
		# first because the decoder reads line by line, so a raw newline
		# would be folded away before a check after it could see it. The
		# decoded check catches the percent encoded newline, and the decoded
		# name is what the service file carries, because libpq reads the
		# URI's parameter names decoded.
		if ! pg_url_value_is_one_line "$pg_key"; then
			PG_URL_ERROR="DATABASE_URL carries a line break in one of its parts"
			return 1
		fi
		pg_key_decoded="$(pg_url_decode "$pg_key")"
		if ! pg_url_value_is_one_line "$pg_key_decoded"; then
			PG_URL_ERROR="DATABASE_URL carries a line break in one of its parts"
			return 1
		fi
		case "$pg_key_decoded" in
			service|servicefile)
				PG_URL_ERROR="DATABASE_URL must not carry a service parameter (the probe reads one database, the one the URL names)"
				return 1
				;;
			'')
				continue
				;;
		esac
		pg_value_decoded="$(pg_url_decode "$pg_value")"
		if ! pg_url_value_is_one_line "$pg_value_decoded"; then
			PG_URL_ERROR="DATABASE_URL carries a line break in one of its parts"
			return 1
		fi
		pg_params="$pg_params$pg_key_decoded=$pg_value_decoded
"
	done

	pg_host_decoded="$(pg_url_decode "$pg_host")"
	pg_database_decoded="$(pg_url_decode "$pg_database")"
	pg_user_decoded="$(pg_url_decode "$pg_user")"
	pg_password_decoded="$(pg_url_decode "$pg_password")"

	# A value that carries a line break would break the service file's
	# shape, and the file is the only place the password lives, so such a
	# URL is refused rather than written.
	for pg_part in "$pg_host_decoded" "$pg_database_decoded" "$pg_user_decoded" "$pg_password_decoded"; do
		if ! pg_url_value_is_one_line "$pg_part"; then
			PG_URL_ERROR="DATABASE_URL carries a line break in one of its parts"
			return 1
		fi
	done

	(
		umask 077
		{
			printf '[%s]\n' "$PG_SERVICE_NAME"
			printf 'host=%s\n' "$pg_host_decoded"
			if [ -n "$pg_port" ]; then
				printf 'port=%s\n' "$pg_port"
			fi
			printf 'dbname=%s\n' "$pg_database_decoded"
			if [ -n "$pg_user_decoded" ]; then
				printf 'user=%s\n' "$pg_user_decoded"
			fi
			if [ -n "$pg_password_decoded" ]; then
				printf 'password=%s\n' "$pg_password_decoded"
			fi
			printf '%s' "$pg_params"
		} > "$pg_path"
	) || {
		PG_URL_ERROR="the libpq service file could not be written"
		return 1
	}

	return 0
}

#!/usr/bin/env bash
# artifact-check.sh <dir>...: refuses a failure artifact that would carry the run's secrets.
#
# hh is public, so a workflow artifact can be downloaded by anyone. The run's session key is
# throwaway and the box is gone by the time the artifact uploads, but a key never leaves the
# runner, and the subject the job masked stays masked (FROM-PRINCIPAL 397). This check runs
# before the upload and fails the job when any directory it is given holds:
#   - a key file (*.pem), a keys/ directory, or the state file (state.json);
#   - a private key's PEM header in any file;
#   - the masked subject's value (MDREAL_SUBJECT), in any file, inside trace zips included.
# A missing directory is fine (a run that failed before the suite has no report). The job runs
# with no traces, so a subject inside a JWT's base64url payload has no file to hide in; the plain
# subject and the key are searched in every file, binary ones and zip streams included.
set -u
command -v unzip >/dev/null || { echo "artifact-check: unzip is missing, so archives cannot be searched" >&2; exit 1; }

found=0
say() { echo "artifact-check: $*" >&2; found=1; }

for dir in "$@"; do
	[ -d "$dir" ] || continue
	while IFS= read -r path; do say "a key or state file would upload: $path"; done < <(
		find "$dir" \( -name '*.pem' -o -name 'state.json' -o \( -type d -name keys \) \) -print
	)
	while IFS= read -r path; do say "a private key would upload inside: $path"; done < <(
		grep -rla -e 'PRIVATE KEY-----' "$dir" 2>/dev/null
	)
	while IFS= read -r zip; do
		if unzip -p "$zip" 2>/dev/null | grep -qa -e 'PRIVATE KEY-----'; then
			say "a private key would upload inside the archive: $zip"
		fi
	done < <(find "$dir" -name '*.zip' -print)
	if [ -n "${MDREAL_SUBJECT:-}" ]; then
		while IFS= read -r path; do say "the masked subject would upload inside: $path"; done < <(
			grep -rlaF -e "$MDREAL_SUBJECT" "$dir" 2>/dev/null
		)
		while IFS= read -r zip; do
			if unzip -p "$zip" 2>/dev/null | grep -qaF -e "$MDREAL_SUBJECT"; then
				say "the masked subject would upload inside the archive: $zip"
			fi
		done < <(find "$dir" -name '*.zip' -print)
	fi
done

if [ "$found" -ne 0 ]; then
	echo "artifact-check: refused; nothing uploads" >&2
	exit 1
fi
echo "artifact-check: clean"

#!/usr/bin/env bash
# Downloads a few courses from the community course site smm.pretendo.zip,
# to have real courses to test a private server with.
#
# It picks courses with the site's own "random course" link and saves each
# one with the site's download link, one at a time. The site allows 15
# downloads per 5 minutes; this waits 25 seconds between courses to stay
# under that, and stops as soon as the site says to slow down.
#
# The courses were made by other players. Keep them for private testing,
# and do not publish them.
#
# Usage: scripts/fetch-test-levels.sh [count] [directory]
#   count      how many courses to download (default 50)
#   directory  where to save them (default local-data/levels)
#
# SMM_LEVEL_SITE can be set to use another address, such as a local copy
# of the site.
set -euo pipefail

count="${1:-50}"
directory="${2:-local-data/levels}"
site="${SMM_LEVEL_SITE:-https://smm.pretendo.zip}"
wait_seconds=25
agent="smm-test-level-fetch/1.0 (run by hand; one course every ${wait_seconds}s)"

case "$count" in
	'' | *[!0-9]*)
		echo "count must be a number, got: $count" >&2
		exit 2
		;;
esac

mkdir -p "$directory"

saved=0
tries=0
# * Random picks can repeat or have no files, so allow some extra tries
max_tries=$((count * 3))

while [ "$saved" -lt "$count" ] && [ "$tries" -lt "$max_tries" ]; do
	tries=$((tries + 1))

	# * The random link answers with a redirect to /course/<id>
	location="$(curl -sS -m 30 -A "$agent" -o /dev/null -w '%{redirect_url}' "$site/course/random")"
	id="${location##*/course/}"

	case "$id" in
		'' | *[!0-9]*)
			echo "The site did not give a course (answer: '${location}'). Stopping" >&2
			break
			;;
	esac

	file="$directory/$id.zip"

	if [ -e "$file" ]; then
		sleep 2
		continue
	fi

	status="$(curl -sS -m 60 -A "$agent" -o "$file.part" -w '%{http_code}' "$site/course/$id/download")"

	case "$status" in
		200)
			mv -f "$file.part" "$file"
			saved=$((saved + 1))
			echo "[$saved/$count] saved course $id"
			;;
		404)
			# * The site knows the course but does not have its files
			rm -f "$file.part"
			echo "course $id has no files on the site, skipping"
			;;
		*)
			rm -f "$file.part"
			echo "The site answered $status for course $id. Stopping, so as not to keep asking" >&2
			break
			;;
	esac

	if [ "$saved" -lt "$count" ]; then
		sleep "$wait_seconds"
	fi
done

echo "Saved $saved courses this run. $(find "$directory" -name '*.zip' | wc -l | tr -d ' ') in $directory"

if [ "$saved" -lt "$count" ]; then
	exit 1
fi

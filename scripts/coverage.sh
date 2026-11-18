#!/bin/sh
# Measures how much of the code the tests run, counting the server the
# end to end tests start as well as the code the tests call directly.
# Prints the coverage of each package, worst first, and the total, and
# writes build/coverage.html to look through line by line.
#
#   scripts/coverage.sh
#
# The game protocol client test only counts when it is set up, as for
# go test (see "Testing" in the README):
#
#   PN_SMM_E2E_PYTHON=e2e/client/.venv/bin/python
set -eu

cd "$(dirname "$0")/.."

counts=$(mktemp -d)
trap 'rm -rf "$counts"' EXIT
mkdir "$counts/tests" "$counts/server"

[ -n "${PN_SMM_E2E_PYTHON:-}" ] || echo "PN_SMM_E2E_PYTHON is not set: the game protocol client test is skipped, so the handlers it reaches count less" >&2

PN_SMM_E2E_COVERAGE="$counts/server" go test -count=1 -cover -coverpkg=./... ./... -args -test.gocoverdir="$counts/tests" >"$counts/test-output" 2>&1 || {
	cat "$counts/test-output"
	echo "The tests failed, so there is no coverage to report" >&2
	exit 1
}

mkdir -p build
go tool covdata textfmt -i="$counts/tests,$counts/server" -o build/coverage.out
go tool cover -html=build/coverage.out -o build/coverage.html

# * Each block of code is counted once, as run if any test ran it
tail -n +2 build/coverage.out | awk '
{
	block = $1
	statements[block] = $2
	if ($3 > 0) ran[block] = 1
}
END {
	for (block in statements) {
		split(block, parts, ":")
		count = split(parts[1], path, "/")
		package = path[1]
		for (i = 2; i < count; i++) package = package "/" path[i]
		total[package] += statements[block]
		all += statements[block]
		if (block in ran) { covered[package] += statements[block]; allCovered += statements[block] }
	}
	for (package in total) printf "%5.1f%%  %5d of %5d statements  %s\n", 100 * covered[package] / total[package], covered[package], total[package], package
	printf "TOTAL %5.1f%%  %5d of %5d statements\n", 100 * allCovered / all, allCovered, all > "/dev/stderr"
}' | sed 's#github.com/PretendoNetwork/super-mario-maker/\{0,1\}##; s#statements  $#statements  (root)#' | sort -n

echo "Line by line: build/coverage.html"

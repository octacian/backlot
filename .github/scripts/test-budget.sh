#!/usr/bin/env bash
# Passes `go test` output through and fails when a package's elapsed time
# exceeds LIMIT seconds, so suite growth is caught before the Go timeout is.
# Usage: go test ... | test-budget.sh LIMIT    or    test-budget.sh --self-test
set -euo pipefail

check() {
	awk -v limit="$1" '
		{ print; fflush() }
		$1 == "ok" && $NF ~ /^[0-9.]+s$/ {
			seconds = $NF + 0
			if (seconds > limit) { over = over sprintf("\n  %s took %.0fs", $2, seconds) }
		}
		END {
			if (over != "") {
				printf "test budget exceeded (limit %ds per package):%s\nSee docs/testing.md#writing-tests.\n", limit, over > "/dev/stderr"
				exit 1
			}
		}'
}

if [ "${1:-}" = "--self-test" ]; then
	printf 'ok  \texample.com/fast\t9.5s\nok  \texample.com/cached\t(cached)\n' | check 10 >/dev/null
	if printf 'ok  \texample.com/slow\t10.5s\n' | check 10 >/dev/null 2>&1; then
		echo "test-budget self-test: slow package was accepted" >&2
		exit 1
	fi
	exit 0
fi

check "${1:?limit in seconds}"

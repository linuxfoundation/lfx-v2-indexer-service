#!/bin/sh
# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT

set -eu

cd "$(dirname "$0")/.."

expected=$(cat .go-version)
if ! printf '%s\n' "$expected" | grep -Eq '^1\.[0-9]+\.[0-9]+$'; then
	echo "Invalid Go build version in .go-version" >&2
	exit 1
fi
toolchain=$(awk '$1 == "toolchain" {sub(/^go/, "", $2); print $2}' go.mod)
scanner=$(sed -n 's/^GoVersionOverride = "\([^"]*\)"$/\1/p' osv-scanner.toml)

if [ "$expected" != "$toolchain" ] || [ "$expected" != "$scanner" ]; then
	echo "Go build/scanner version mismatch: align .go-version, go.mod toolchain, and GoVersionOverride" >&2
	exit 1
fi

actual=$(go env GOVERSION)
actual=${actual#go}
actual=${actual%% *}
actual=${actual%%-*}
if ! printf '%s\n' "$actual" | grep -Eq '^1\.[0-9]+\.[0-9]+$'; then
	echo "Cannot verify Go compiler version: $actual" >&2
	exit 1
fi

if [ "$actual" != "$expected" ]; then
	echo "Go $actual does not match the scanned build toolchain $expected; set GOTOOLCHAIN=go$expected or install Go $expected" >&2
	exit 1
fi

echo "Go build/scanner versions aligned at $expected; compiler is $actual"

#!/bin/sh
# Usage: gen-errcodes.sh <output>
# Run from cmd/sample (go generate does). Exports the sample's error codes as
# Markdown into a temporary file next to <output> and replaces <output> only
# when the export succeeds, so a failed run leaves the document untouched.
set -eu
out=${1:?usage: gen-errcodes.sh <output>}
tmp=$(mktemp "$out.XXXXXX")
trap 'rm -f "$tmp"' EXIT
trap 'exit 1' HUP INT TERM
go run . errcodes --log.level=warn >"$tmp"
chmod 644 "$tmp"
mv "$tmp" "$out"

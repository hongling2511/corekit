#!/usr/bin/env bash
# Checks that every non-merge commit in <base>..<head> has a Conventional Commits subject.
# Usage: scripts/check-commits.sh <base> [<head>]
set -euo pipefail

base=${1:?usage: check-commits.sh <base> [<head>]}
head=${2:-HEAD}
pattern='^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(\([^)]+\))?!?: .+'

git rev-parse --verify --quiet "$base^{commit}" >/dev/null || { echo "unknown base commit: $base" >&2; exit 1; }
git rev-parse --verify --quiet "$head^{commit}" >/dev/null || { echo "unknown head commit: $head" >&2; exit 1; }

bad=0
while IFS= read -r line; do
  sha=${line%% *}
  subject=${line#* }
  if [[ ! $subject =~ $pattern ]]; then
    echo "not a Conventional Commit: ${sha:0:12} $subject" >&2
    bad=1
  fi
done < <(git log --no-merges --format='%H %s' "$base..$head")

if [[ $bad -ne 0 ]]; then
  echo "commit subjects must look like 'type(scope)?: description', see https://www.conventionalcommits.org" >&2
  exit 1
fi
echo "all commits in $base..$head follow Conventional Commits"

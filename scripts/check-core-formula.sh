#!/usr/bin/env bash
#
# Check that the tele formula in homebrew-core is on the latest stable release,
# and open an issue when it is not.
#
# Nothing here publishes: homebrew-core keeps the formula current itself, with
# `brew bump --auto` every three hours. This script watches that arrangement,
# because when it stops working it stops quietly - our release is green, their
# formula simply stays where it was.
#
# Usage: scripts/check-core-formula.sh
# Environment:
#   GRACE_HOURS  how long after a release the lag is normal (default 48)
#   DRY_RUN      1 to print the verdict and never touch issues (default 0)
# Requires: gh authenticated against this repository, and curl.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
# shellcheck source=scripts/core-watch-lib.sh
source scripts/core-watch-lib.sh

grace_hours=${GRACE_HOURS:-48}
dry_run=${DRY_RUN:-0}

# One issue, reopened by title rather than multiplied: a formula that is stuck
# stays stuck for days, and a daily job that says so daily is a job people mute.
issue_title="Infra: homebrew-core is behind the latest release"

core_version=$(curl -fsSL https://formulae.brew.sh/api/formula/tele.json |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["versions"]["stable"])')

# The latest published non-prerelease release: betas never reach homebrew-core,
# so a beta cut after a stable one must not read as the version core is missing.
read -r tag_name published_at < <(
  gh api repos/:owner/:repo/releases/latest --jq '"\(.tag_name) \(.published_at)"'
)
tag_version=${tag_name#v}

now=$(date -u +%s)
# BSD date (macOS) and GNU date (CI) disagree on parsing, so the timestamp is
# turned into epoch seconds by python, which is already a dependency above.
published_epoch=$(python3 -c 'import datetime,sys; print(int(datetime.datetime.fromisoformat(sys.argv[1]).timestamp()))' "$published_at")
age_hours=$(((now - published_epoch) / 3600))

open_prs=$(gh api -X GET search/issues \
  -f q='repo:Homebrew/homebrew-core is:pr is:open tele in:title' --jq '.total_count')

verdict=$(core_watch_verdict "$core_version" "$tag_version" "$age_hours" "$grace_hours")

echo "homebrew-core: ${core_version}"
echo "latest stable: ${tag_version} (${age_hours}h old)"
echo "open tele PRs in homebrew-core: ${open_prs}"
echo "verdict: ${verdict}"

if [ "$verdict" != behind ]; then
  exit 0
fi

body=$(core_watch_report "$verdict" "$core_version" "$tag_version" "$age_hours" "$open_prs")

if [ "$dry_run" = 1 ]; then
  echo "--- issue that would be opened ---"
  echo "$body"
  exit 0
fi

existing=$(gh issue list --state open --search "\"${issue_title}\" in:title" \
  --json number,title --jq "map(select(.title == \"${issue_title}\")) | .[0].number // empty")

if [ -n "$existing" ]; then
  echo "issue #${existing} is already open; leaving it alone"
  exit 0
fi

gh issue create --title "$issue_title" --body "$body" --label area:infra --label bug

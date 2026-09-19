#!/usr/bin/env bash
#
# Show download counts for tele releases. Prints per-tag totals and a grand
# total. Betas are included (they are prerelease GitHub tags).
#
# What the numbers cover: every package manager tele ships through — Homebrew,
# Scoop, winget, and the Nix flake — fetches its artifact from the GitHub
# release assets. So each `brew install` / `scoop install` / `winget install` /
# nix build increments the GitHub downloadCount. That means these per-asset
# counts already fold in all of those channels; they must NOT be summed on top
# or they double count. Gemfury (apt/dnf/apk) serves bytes from its own infra
# and is invisible here. Snap is not published yet; once it is, query
# `snapcraft metrics` for it separately.
#
# checksums.txt, *.sigstore.json and *.sbom.json are excluded: those are pulled
# by verification tooling and crawlers, not by users installing tele, so they
# would inflate the install count.
#
# Usage:
#   ./release-downloads.sh          # every release (all majors, betas included)
#   ./release-downloads.sh 0        # all v0.x.y releases
#   ./release-downloads.sh v1       # all v1.x.y releases
#
set -euo pipefail

# Tag filter: no arg -> all releases; otherwise restrict to one major version.
if [[ $# -ge 1 ]]; then
  major="${1#v}"          # accept both "0" and "v0"
  tag_filter="^v?${major}\."
  scope="major version v${major}"
else
  tag_filter="^v"
  scope="all versions"
fi

# Pull every release tag, keep only those matching the requested scope.
tags="$(gh release list --limit 200 --json tagName --jq '.[].tagName' \
  | grep -E "$tag_filter" || true)"

if [[ -z "$tags" ]]; then
  echo "no releases found for ${scope}" >&2
  exit 1
fi

# jq expression: sum downloadCount across installable assets only, skipping
# checksums/signatures/SBOMs. Empty asset list -> 0.
count_jq='[.assets[]
  | select(
      (.name | endswith(".sbom.json")) or
      (.name | endswith(".sigstore.json")) or
      (.name == "checksums.txt")
      | not
    )
  | .downloadCount] | add // 0'

grand_total=0

while IFS= read -r tag; do
  [[ -z "$tag" ]] && continue
  count="$(gh release view "$tag" --json assets --jq "$count_jq")"
  printf '%-16s %d\n' "$tag" "$count"
  grand_total=$(( grand_total + count ))
done <<< "$tags"

printf '%-16s %d\n' "TOTAL" "$grand_total"

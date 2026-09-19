#!/usr/bin/env bash
# Pure helpers for scripts/check-core-formula.sh: the decision about whether
# homebrew-core has fallen behind, separated from fetching the facts it needs.
# Sourced, not executed. No network, no git, no side effects.

# core_watch_verdict <core_version> <tag_version> <release_age_hours> <grace_hours>
# Echoes exactly one of:
#   current  the formula is on the latest stable release
#   pending  it is behind, but the release is younger than the grace period
#   behind   it is behind and the grace period has passed
#
# The grace period exists because nobody publishes to homebrew-core: their
# autobump runs every three hours and their CI then builds bottles for every
# platform, so a formula is legitimately behind for a while after every release.
core_watch_verdict() {
  local core=$1 tag=$2 age=$3 grace=$4

  if [ "$core" = "$tag" ]; then
    echo current
    return
  fi
  if [ "$age" -lt "$grace" ]; then
    echo pending
    return
  fi
  echo behind
}

# core_watch_report <verdict> <core_version> <tag_version> <age_hours> <open_prs>
# Echoes the body of the issue to open. open_prs is the count of open pull
# requests naming tele in homebrew-core, which is what separates the two ways
# this goes wrong: a bump that was never proposed, and one that was proposed and
# is stuck.
core_watch_report() {
  local verdict=$1 core=$2 tag=$3 age=$4 prs=$5

  echo "homebrew-core is on ${core}; the latest stable release is ${tag}, published ${age} hours ago."
  echo
  if [ "$prs" -gt 0 ]; then
    echo "There are ${prs} open pull requests naming tele in Homebrew/homebrew-core, so the bump was proposed and has not landed. Check their CI before doing anything here: a red test block is ours to fix."
    echo "https://github.com/Homebrew/homebrew-core/pulls?q=is%3Apr+is%3Aopen+tele"
  else
    echo "No open pull request in Homebrew/homebrew-core names tele, so the bump was never proposed. Their autobump runs every three hours and skips a formula whose livecheck cannot resolve a version, which is the first thing to check."
    echo "brew livecheck --debug homebrew/core/tele"
  fi
  echo
  echo "Verdict: ${verdict}. Raised by scripts/check-core-formula.sh."
}

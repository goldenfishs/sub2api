#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
[[ -z $(git status --porcelain) ]] || { echo 'Commit or separately preserve working changes first.' >&2; exit 1; }
case "$(git remote get-url origin)" in
  https://github.com/goldenfishs/sub2api.git|git@github.com:goldenfishs/sub2api.git) ;;
  *) echo 'origin must be the goldenfishs/sub2api fork.' >&2; exit 1 ;;
esac
case "$(git remote get-url upstream)" in
  https://github.com/Wei-Shaw/sub2api.git|git@github.com:Wei-Shaw/sub2api.git) ;;
  *) echo 'upstream must be Wei-Shaw/sub2api.' >&2; exit 1 ;;
esac
git fetch origin main
git fetch upstream main
branch="sync/upstream-$(date -u +%Y%m%d-%H%M%S)"
git switch -c "$branch" origin/main
git merge --no-edit upstream/main
printf 'Review the merge, especially migrations and subscription behavior, then push %s and open a PR against goldenfishs/sub2api:main.\n' "$branch"
echo 'Require Lumivia Release / verify to pass. Deploy the resulting main commit image with lumivia-update.sh.'

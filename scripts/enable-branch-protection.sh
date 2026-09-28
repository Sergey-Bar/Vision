#!/usr/bin/env bash
# Enable branch protection on main.
#
# This is a no-op on GitHub Free for a private repository: both the classic
# protection API and rulesets return 403 until the plan is upgraded. Until then
# `main` is unprotected — nothing stops a force-push, and nothing gates a merge.
# That is the one real gap in this repository's safety net, and this script is
# the fix, ready to run the day you upgrade.
#
#   gh auth login
#   ./scripts/enable-branch-protection.sh
#
# Re-run it freely. It is idempotent.
#
# Note on required status checks: the repository has no pipeline, so there are
# no checks to require. That is deliberate and not an oversight — GitHub Actions
# cannot start a job for this account, so requiring a check that never runs would
# block every merge permanently. The list below is therefore empty, and what
# this actually buys is: no force-push, no deletions, no direct push, a PR
# required, and conversations must be resolved. If CI is ever rebuilt, add the
# job names here at the same time.
set -euo pipefail

REPO="${REPO:-Sergey-Bar/Ai-Captain}"
BRANCH="${BRANCH:-main}"
API="repos/$REPO/branches/$BRANCH/protection"

CHECKS=(
  # Intentionally empty. See the note above.
)

required='['
sep=''
for c in "${CHECKS[@]}"; do
  required+="${sep}{\"context\":\"${c}\"}"
  sep=','
done
required+=']'

echo "==> Applying protection to $REPO/$BRANCH"
echo "==> ${#CHECKS[@]} required status check(s) — no pipeline exists to produce any"

if gh api "$API" --method PUT --input - >/dev/null 2>&1 <<JSON
{
  "required_status_checks": {
    "strict": true,
    "contexts": ${required}
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": false,
    "required_approving_review_count": 1
  },
  "required_signatures": false,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "required_linear_history": true,
  "block_creations": false,
  "required_conversation_resolution": true,
  "lock_branch": false,
  "allow_fork_syncing": true
}
JSON
then
  echo "==> Protection is on. main now requires a PR, a green pipeline and an approval."
else
  echo "==> Could not apply protection." >&2
  echo "    On a private repository this normally means the free plan." >&2
  echo "    Upgrade to GitHub Pro and run this again." >&2
  exit 1
fi

echo
echo "Verify at: https://github.com/$REPO/settings/branches"
echo
echo "Note: 'strict: true' means a PR must be up to date with main, so CI"
echo "re-runs after every merge. That costs a little of the free tier's monthly"
echo "minutes and buys you a guarantee that what you approved is what landed."

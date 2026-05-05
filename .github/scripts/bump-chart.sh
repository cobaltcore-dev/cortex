#!/usr/bin/env bash
# Bumps appVersion in a Helm Chart.yaml, skipping if a newer code commit already
# covers this component.
#
# Usage: bump-chart.sh <chart-yaml> <short-sha> <trigger-sha> [path-filter ...]
#
# path-filter args scope the freshness check to specific paths (e.g. postgres/).
# Omit them for an unconditional bump (the main cortex chart).
set -euo pipefail

CHART=$1; SHORT_SHA=$2; TRIGGER_SHA=$3; shift 3
PATHS=("$@")

git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git fetch origin main
git reset --hard origin/main

# Exclude bump commits ([skip ci]) so earlier steps in this same run don't
# falsely count as "newer code". Only real code commits trigger a skip.
if [ ${#PATHS[@]} -gt 0 ]; then
    NEWER=$(git log --oneline --invert-grep --grep='\[skip ci\]' "$TRIGGER_SHA..HEAD" -- "${PATHS[@]}")
else
    NEWER=$(git log --oneline --invert-grep --grep='\[skip ci\]' "$TRIGGER_SHA..HEAD")
fi

if [ -n "$NEWER" ]; then
    echo "Skipping $CHART: newer code commits exist on main for this component"
    exit 0
fi

CHART_NAME=$(basename "$(dirname "$CHART")")
sed -i 's/^\([ ]*appVersion:[ ]*\).*/\1"'"$SHORT_SHA"'"/' "$CHART"
git add "$CHART"
git diff --cached --quiet && { echo "No changes to commit for $CHART_NAME"; exit 0; }
git commit -m "Bump $CHART_NAME chart appVersions to $SHORT_SHA [skip ci]"
git push origin HEAD:main

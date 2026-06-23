#!/usr/bin/env bash
# Bumps appVersion in a Helm Chart.yaml to match the `latest` tag in GHCR.
#
# Usage: bump-chart.sh <chart-dir> <ghcr-package-name>
#
# Source of truth:
#   GHCR `latest` tag for the org `cobaltcore-dev`. We list package versions,
#   find the one tagged `latest`, and read its companion `sha-XXXXXXXX` tag.
#   That short SHA is what the image-build workflow tagged onto the most
#   recently published image, so chart appVersion should track it verbatim.
#
# Behaviour:
#   - If the on-disk appVersion already matches GHCR `latest`, the script is a
#     no-op (prints `noop`, exits 0).
#   - Otherwise it rewrites Chart.yaml in place via `yq` (prints `bumped`,
#     exits 0). It does not stage, commit, or push.
#   - Any error (missing GH_TOKEN, GHCR API failure, no `latest` tag, no
#     `sha-*` companion) exits non-zero and aborts the caller.
#
# Required env: GH_TOKEN (token with read:packages on cobaltcore-dev).
set -euo pipefail

CHART_DIR=$1
PKG=$2
CHART="$CHART_DIR/Chart.yaml"

if [ -z "${GH_TOKEN:-}" ]; then
    echo "GH_TOKEN is required" >&2
    exit 1
fi

if [ ! -f "$CHART" ]; then
    echo "Chart.yaml not found at $CHART" >&2
    exit 1
fi

resp=$(curl -fsSL \
    -H "Authorization: Bearer $GH_TOKEN" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/orgs/cobaltcore-dev/packages/container/$PKG/versions?per_page=100")

TARGET=$(jq -r '
    .[]
    | select(.metadata.container.tags | index("latest"))
    | .metadata.container.tags[]
    | select(test("^sha-[0-9a-f]{8}$"))
' <<<"$resp" | head -1)

if [ -z "$TARGET" ]; then
    echo "no sha-XXXXXXXX companion tag on the latest version of $PKG in GHCR" >&2
    exit 1
fi

CURRENT=$(yq '.appVersion' "$CHART")

if [ "$CURRENT" = "$TARGET" ]; then
    echo "noop $PKG ($CURRENT)"
    exit 0
fi

yq -i ".appVersion = \"$TARGET\"" "$CHART"
echo "bumped $PKG: $CURRENT -> $TARGET"

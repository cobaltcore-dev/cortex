#!/bin/bash

# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# Replace oci references in Chart.yaml with local file references.
# This script uses the `from: file://...` comments to identify which
# dependencies should be replaced with local file references instead of
# oci references.
#
# Example:
#   # from: file://../../library/cortex-alerts
#   - name: cortex-alerts
#     repository: oci://ghcr.io/cobaltcore-dev/cortex/charts
# Is converted to:
#   - name: cortex-alerts
#     repository: file://../../library/cortex-alerts
#
# Usage: ./replace-oci-refs.sh path/to/chart
# Output: Modified Chart.yaml content to stdout.

set -euo pipefail

# Check if path argument is provided
if [[ $# -ne 1 ]]; then
    echo "Usage: $0 path/to/chart" >&2
    echo "Example: $0 helm/bundles/cortex-manila" >&2
    exit 1
fi

CHART_PATH="$1"
CHART_YAML="${CHART_PATH}/Chart.yaml"

# Check if Chart.yaml exists
if [[ ! -f "$CHART_YAML" ]]; then
    echo "Error: Chart.yaml not found at $CHART_YAML" >&2
    exit 1
fi

# Process the Chart.yaml file using awk and output directly to stdout
awk '
BEGIN {
    file_ref = ""
    in_dependency = 0
}

# Look for "from: file://" comments
/^[[:space:]]*#[[:space:]]*from:[[:space:]]*file:\/\// {
    # Extract the file path from the comment
    file_ref = $0
    gsub(/^[[:space:]]*#[[:space:]]*from:[[:space:]]*/, "", file_ref)
    print $0
    next
}

# Check if we are entering a dependency block
/^[[:space:]]*-[[:space:]]*name:/ {
    in_dependency = 1
    print $0
    next
}

# If we have a file reference and this is a repository line in a dependency
/^[[:space:]]*repository:[[:space:]]*oci:\/\// && in_dependency == 1 && file_ref != "" {
    # Replace the oci repository with the file reference
    match($0, /^([[:space:]]*)repository:/)
    indent = substr($0, 1, RLENGTH - length("repository:"))
    print indent "repository: " file_ref
    file_ref = ""  # Reset after use
    in_dependency = 0
    next
}

# Reset dependency tracking when we encounter a new field at the same level
/^[[:space:]]*[a-zA-Z][^:]*:/ && !/^[[:space:]]*-[[:space:]]*name:/ && !/^[[:space:]]*repository:/ && !/^[[:space:]]*version:/ {
    in_dependency = 0
    file_ref = ""
}

# For version lines in dependencies, we stay in dependency mode
/^[[:space:]]*version:/ && in_dependency == 1 {
    print $0
    in_dependency = 0  # End of this dependency block
    next
}

# Print all other lines as-is
{
    print $0
}
' "$CHART_YAML"

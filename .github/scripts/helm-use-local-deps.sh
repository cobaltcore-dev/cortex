#!/usr/bin/env bash

# Copyright SAP SE
# SPDX-License-Identifier: Apache-2.0

# Rewrite OCI repository references in a Chart.yaml to local file:// paths,
# using the "# from: file://..." comments as the source of truth.
#
# Usage: helm-use-local-deps.sh path/to/chart [path/to/chart ...]

set -euo pipefail

for chart in "$@"; do
  CHART_YAML="$chart/Chart.yaml"
  if [ ! -f "$CHART_YAML" ]; then
    echo "Chart.yaml not found at $CHART_YAML" >&2
    continue
  fi

  echo "Rewriting OCI deps to local paths in $CHART_YAML"

  tmpfile=$(mktemp)
  awk '
  BEGIN { file_ref = ""; in_dependency = 0 }
  /^[[:space:]]*#[[:space:]]*from:[[:space:]]*file:\/\// {
      file_ref = $0
      gsub(/^[[:space:]]*#[[:space:]]*from:[[:space:]]*/, "", file_ref)
      print $0; next
  }
  /^[[:space:]]*-[[:space:]]*name:/ { in_dependency = 1; print $0; next }
  /^[[:space:]]*repository:[[:space:]]*oci:\/\// && in_dependency == 1 && file_ref != "" {
      match($0, /^([[:space:]]*)repository:/)
      indent = substr($0, 1, RLENGTH - length("repository:"))
      print indent "repository: " file_ref
      file_ref = ""; in_dependency = 0; next
  }
  /^[[:space:]]*[a-zA-Z][^:]*:/ && !/^[[:space:]]*-[[:space:]]*name:/ && !/^[[:space:]]*repository:/ && !/^[[:space:]]*version:/ {
      in_dependency = 0; file_ref = ""
  }
  /^[[:space:]]*version:/ && in_dependency == 1 { print $0; in_dependency = 0; next }
  { print $0 }
  ' "$CHART_YAML" > "$tmpfile"
  mv "$tmpfile" "$CHART_YAML"
done

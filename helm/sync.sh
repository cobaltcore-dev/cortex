#!/bin/bash

# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# Pull missing chart.tgz dependencies into a chart directory.
#
# This script checks based on the Chart.yaml file, which dependencies are
# required and checks for each of them, if the corresponding .tgz file
# exists in the charts directory. If not, it runs `helm pull <chart>
# -d path/to/chart/charts` to download the missing dependencies.
#
# Usage: ./sync.sh path/to/chart

# Exit on error
set -e

# Check if a directory parameter is provided
if [ -z "$1" ]; then
  echo "Usage: $0 path/to/chart"
  exit 1
fi

CHART_DIR="$1"
CHART_YAML_FILE="$CHART_DIR/Chart.yaml"
CHARTS_DIR="$CHART_DIR/charts"

# Check if Chart.yaml exists
if [ ! -f "$CHART_YAML_FILE" ]; then
  echo "Chart.yaml not found in $CHART_DIR"
  exit 1
fi

# Create charts directory if it doesn't exist
mkdir -p "$CHARTS_DIR"


# Extract dependencies (name and version) from Chart.yaml using yq
if ! command -v yq >/dev/null 2>&1; then
  echo "Error: 'yq' is required but not installed. Please install yq (https://mikefarah.gitbook.io/yq/)"
  exit 1
fi

# Get dependencies as comma-separated name,version,repository triples
DEPS=$(yq e '.dependencies[] | .name + "," + .version + "," + (.repository // "")' "$CHART_YAML_FILE")

if [ -z "$DEPS" ]; then
  echo "No dependencies found in $CHART_YAML_FILE"
  exit 0
fi

# For each dependency, check if the .tgz exists, if not, pull it
IFS=$'\n'
for dep in $DEPS; do
  NAME=$(echo "$dep" | cut -d',' -f1)
  VERSION=$(echo "$dep" | cut -d',' -f2)
  REPO=$(echo "$dep" | cut -d',' -f3)

  echo "Checking dependency: $NAME, version: $VERSION, repository: $REPO"
  # Check if the .tgz file exists
  TARBALL="$CHARTS_DIR/$NAME-$VERSION.tgz"
  if [ ! -f "$TARBALL" ]; then
    OLD_TARBALLS=$(ls "$CHARTS_DIR/$NAME-"*.tgz 2>/dev/null || true)
    if [ -n "$OLD_TARBALLS" ]; then
      echo "Removing old tarballs: $OLD_TARBALLS"
      rm -f $OLD_TARBALLS
    fi
    echo "Missing $TARBALL, pulling from repository..."
    if [ -z "$REPO" ]; then
      echo "No repository specified for $NAME, skipping pull."
      continue
    fi
    # Pull the chart
    helm pull "$REPO/$NAME" --version "$VERSION" -d "$CHARTS_DIR"
    if [ $? -ne 0 ]; then
      echo "Failed to pull $NAME from $REPO"
      exit 1
    fi
    echo "Pulled $NAME version $VERSION successfully."
  else
    echo "Dependency $NAME version $VERSION already exists at $TARBALL"
  fi
done

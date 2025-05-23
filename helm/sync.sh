#!/bin/bash

# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

# Usage: ./sync.sh path/to/chart

# Exit on error
set -e

# Check if a directory parameter is provided
if [ -z "$1" ]; then
  echo "Usage: $0 path/to/chart"
  exit 1
fi

CHART_DIR="$1"
CHART_LOCK_FILE="$CHART_DIR/Chart.lock"
CHART_YAML_FILE="$CHART_DIR/Chart.yaml"
CHARTS_DIR="$CHART_DIR/charts"

# Check if Chart.lock exists, if not create it from Chart.yaml
if [ ! -f "$CHART_LOCK_FILE" ]; then
  echo "Chart.lock not found. Creating it from Chart.yaml..."
  helm dependency build "$CHART_DIR"
fi

# Get all required .tgz files from Chart.lock
REQUIRED_TGZ_FILES=$(grep -oE 'name: [^ ]+' "$CHART_LOCK_FILE" | awk '{print $2}' | xargs -I {} echo "$CHARTS_DIR/{}-*.tgz")

# Check if all required .tgz files are present and up to date
ALL_UP_TO_DATE=true
for TGZ_FILE in $REQUIRED_TGZ_FILES; do
  if [ ! -f "$TGZ_FILE" ]; then
    ALL_UP_TO_DATE=false
    break
  fi
done

# Run helm dep up if not all .tgz files are up to date
if [ "$ALL_UP_TO_DATE" = false ]; then
  echo "Dependencies are not up to date. Running 'helm dependency update'..."
  helm dependency update "$CHART_DIR"
else
  echo "All dependencies for $CHART_DIR are up to date:"
  for TGZ_FILE in $REQUIRED_TGZ_FILES; do
      echo " - $(basename "$TGZ_FILE")"
  done
fi
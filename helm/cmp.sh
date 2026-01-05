#!/bin/bash

# Copyright SAP SE
# SPDX-License-Identifier: Apache-2.0

# Compares two chart.tgz files to each other.
#
# Explanation: charts can be packaged with `helm package` which will produce
# a <chart-name>-<version>.tgz file. However, these files cannot be directly
# compared, because the tarball sha256sums are different even when the contents
# are the same. Thus, this script unpackages the two .tgz files into temporary
# directories and compares their contents recursively. If any of the files
# differ, or if there are files in one directory that are not in the other,
# the script will echo "false", otherwise it will echo "true".
#
# Usage: ./cmp.sh path/to/chart_a-0.0.1.tgz path/to/chart_b-0.0.1.tgz

# Exit on error
set -e

# Check if exactly two arguments are provided
if [ $# -ne 2 ]; then
    echo "Usage: $0 <chart1.tgz> <chart2.tgz>"
    exit 1
fi

CHART1="$1"
CHART2="$2"

# Check if both files exist
if [ ! -f "$CHART1" ]; then
    echo "false"
    exit 0
fi

if [ ! -f "$CHART2" ]; then
    echo "false"
    exit 0
fi

# Create temporary directories
TEMP_DIR1=$(mktemp -d)
TEMP_DIR2=$(mktemp -d)

# Cleanup function
cleanup() {
    rm -rf "$TEMP_DIR1" "$TEMP_DIR2"
}

# Set trap to cleanup on exit
trap cleanup EXIT

# Extract both charts
tar -xzf "$CHART1" -C "$TEMP_DIR1"
tar -xzf "$CHART2" -C "$TEMP_DIR2"

# Compare the extracted contents
if diff -r "$TEMP_DIR1" "$TEMP_DIR2" > /dev/null 2>&1; then
    echo "true"
else
    echo "false"
fi

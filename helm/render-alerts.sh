#!/bin/bash

set -e

echo "Rendering alert templates for CI validation..."

core_chart_directory="helm/library/cortex-core"
templates_directory="$core_chart_directory/templates"
values_file="helm/ci/core-alert-values.yaml"

if [ -f "$templates_directory/alerts.yaml" ]; then
    echo "Processing cortex-core chart..."

    # Create output directory
    output_dir="$core_chart_directory/rendered"
    mkdir -p "$output_dir"

    # Render the alerts template and extract groups in one go
    helm template test-release "$core_chart_directory" \
        -f "$values_file" \
        --show-only templates/alerts.yaml | \
        yq eval '.spec.groups' - | \
        yq eval '{"groups": .}' - > "$output_dir/alerts.yaml"

    echo "Rendered alerts to $output_dir/alerts.yaml"
else
    echo "No alerts.yaml template found in cortex-core"
    exit 1
fi

echo "Done rendering cortex-core alert template"
#!/bin/bash

# In cortex-core, we use templated alerts to centralize alert definitions and avoid
# duplicating them across each deployment (nova, cinder, manila, etc.).
# However, templated alerts cannot be directly validated with promtool for syntax errors
# or typos in Prometheus queries.
# This script renders the alert templates with test values and formats the output
# for promtool validation in GitHub Actions CI.

set -e
set -o pipefail

echo "Rendering alert templates for CI validation..."

core_chart_directory="helm/library/cortex-core"
templates_directory="$core_chart_directory/templates"

if [ -f "$templates_directory/alerts.yaml" ]; then
    echo "Processing cortex-core chart..."

    # Create output directory
    output_dir="$core_chart_directory/rendered"
    mkdir -p "$output_dir"

    # Render the alerts template and extract groups in one go
    helm template test-release "$core_chart_directory" \
        --show-only templates/alerts.yaml | \
        yq eval '.spec.groups' - | \
        yq eval '{"groups": .}' - > "$output_dir/alerts.yaml"

    echo "Rendered alerts to $output_dir/alerts.yaml"
else
    echo "No alerts.yaml template found in cortex-core"
    exit 1
fi

echo "Done rendering cortex-core alert template"
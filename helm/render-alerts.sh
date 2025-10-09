#!/bin/bash

# In cortex-alerts, we use templated alerts to centralize alert definitions and avoid
# duplicating them across each deployment (nova, cinder, manila, etc.).
# However, templated alerts cannot be directly validated with promtool for syntax errors
# or typos in Prometheus queries.
# This script renders the alert templates with test values and formats the output
# for promtool validation in GitHub Actions CI.

set -e
set -o pipefail

echo "Rendering alert templates for CI validation..."

alert_chart_directory="helm/library/cortex-alerts"
templates_directory="$alert_chart_directory/templates"

if [ -f "$templates_directory/alerts.yaml" ]; then
    echo "Processing cortex-alerts chart..."

    # Create output directory
    output_dir="$alert_chart_directory/rendered"
    mkdir -p "$output_dir"

    # Render the alerts template and extract groups in one go
    helm template test-release "$alert_chart_directory" \
        --show-only templates/alerts.yaml | \
        yq eval '.spec.groups' - | \
        yq eval '{"groups": .}' - > "$output_dir/alerts.yaml"

    echo "Rendered alerts to $output_dir/alerts.yaml"
else
    echo "No alerts.yaml template found in cortex-alerts"
    exit 1
fi

echo "Done rendering cortex-alerts alert template"
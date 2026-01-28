// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

// Trigger executed when the prometheus metric with this alias has finished syncing.
func TriggerMetricAliasSynced(metricAlias string) string {
	return "triggers/sync/prometheus/alias/" + metricAlias
}

// Trigger executed when the prometheus metric with this type has finished syncing.
func TriggerMetricTypeSynced(metricType string) string {
	return "triggers/sync/prometheus/type/" + metricType
}

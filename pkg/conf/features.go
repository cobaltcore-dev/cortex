// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

// FeatureGates holds boolean toggles for features that are in active rollout
// or that are only relevant for certain deployment types. Set via conf.json
// under the "featureGates" key. All flags default to false (disabled) when omitted.
type FeatureGates struct {
	// CommittedResourceTracking enables committed resource integration: writing
	// placed VM UUIDs back into Reservation slots and classifying no-host-found
	// results by committed resource coverage. Enable only on deployments that
	// use committed resources.
	CommittedResourceTracking bool `json:"committedResourceTracking,omitempty"`
	// PessimisticBlocking enables blocking all candidate hosts at scheduling time
	// to prevent concurrent placements from violating capacity constraints. Enable
	// only on deployments that support pessimistic blocking.
	PessimisticBlocking bool `json:"pessimisticBlocking,omitempty"`
}

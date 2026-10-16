// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

// FeatureGates holds boolean toggles for Nova scheduling features that are in
// active rollout or only relevant for certain deployment types. Set via conf.json
// under the "featureGates" key. All flags default to false (disabled) when omitted.
type FeatureGates struct {
	// CommittedResourceTracking enables committed resource integration: writing
	// placed VM UUIDs back into Reservation slots and classifying no-host-found
	// results by committed resource coverage. Enable only on deployments that
	// use committed resources.
	CommittedResourceTracking bool `json:"committedResourceTracking,omitempty"`

	// VirtualMachineCreation enables the creation of VM resources based on the
	// scheduling spec for non-read-only requests.
	VirtualMachineCreation bool `json:"virtualMachineCreation,omitempty"`
	// VirtualMachineCreationMandatory must be enabled together with
	// the VirtualMachineCreation feature. If set, the pipeline will
	// require that a VM is created for each scheduling request.
	VirtualMachineCreationMandatory bool `json:"virtualMachineCreationMandatory,omitempty"`
}

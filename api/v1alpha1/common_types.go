// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// SchedulingDomain reflects the logical domain for scheduling.
type SchedulingDomain string

const (
	// SchedulingDomainNova indicates scheduling related to the
	// openstack Nova service, which is the compute service responsible for
	// managing virtual machines in an openstack cloud infrastructure.
	SchedulingDomainNova SchedulingDomain = "nova"
	// SchedulingDomainCinder indicates scheduling related to the
	// openstack Cinder service, which is the block storage service responsible
	// for managing volumes in an openstack cloud infrastructure.
	SchedulingDomainCinder SchedulingDomain = "cinder"
	// SchedulingDomainManila indicates scheduling related to the openstack
	// Manila service, which is the shared file system service responsible
	// for managing shared file systems in an openstack cloud infrastructure.
	SchedulingDomainManila SchedulingDomain = "manila"
	// SchedulingDomainMachines indicates scheduling related to the ironcore
	// machines, which are virtual machines managed by the ironcore platform.
	SchedulingDomainMachines SchedulingDomain = "machines"
	// SchedulingDomainPods indicates scheduling related to Kubernetes pods,
	// which are the smallest deployable units in a Kubernetes cluster.
	SchedulingDomainPods SchedulingDomain = "pods"
)

// Parameter is a struct that can be used to define parameters for filters,
// weighers, and other resources that need flexible configuration.
//
// It is strongly encouraged to use this type for configuration of resources
// at it provides a more strict schema and validation compared to using
// runtime.RawExtension directly.
type Parameter struct {
	// The key of the parameter.
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// +kubebuilder:validation:Optional
	StringValue *string `json:"stringValue,omitempty"`
	// +kubebuilder:validation:Optional
	BoolValue *bool `json:"boolValue,omitempty"`
	// +kubebuilder:validation:Optional
	IntValue *int64 `json:"intValue,omitempty"`
	// +kubebuilder:validation:Optional
	FloatValue *float64 `json:"floatValue,omitempty"`
	// +kubebuilder:validation:Optional
	StringListValue *[]string `json:"stringListValue,omitempty"`
	// +kubebuilder:validation:Optional
	FloatMapValue *map[string]float64 `json:"floatMapValue,omitempty"`
	// +kubebuilder:validation:Optional
	StringListMapValue *map[string][]string `json:"stringListMapValue,omitempty"`
}

// Parameters is a list of Parameter structs that can be used to define
// multiple parameters for filters, weighers, and other resources that need
// flexible but limited configuration.
type Parameters []Parameter

// InputSpec references a datasource or another knowledge whose objects are
// passed to a knowledge extractor or kpi under the given variable name.
// +kubebuilder:validation:XValidation:rule="has(self.datasource) != has(self.knowledge)",message="exactly one of datasource or knowledge must be set"
type InputSpec struct {
	// Name under which the input objects are passed, e.g. to an expression
	// or to the struct field tagged with `input:"<var>"` in a go extractor.
	// +kubebuilder:validation:Pattern=`^[a-zA-Z_][a-zA-Z0-9_]*$`
	Var string `json:"var"`
	// Name of the datasource providing the input objects.
	// +kubebuilder:validation:Optional
	Datasource string `json:"datasource,omitempty"`
	// Name of the knowledge providing its extracted features as input objects.
	// +kubebuilder:validation:Optional
	Knowledge string `json:"knowledge,omitempty"`
}

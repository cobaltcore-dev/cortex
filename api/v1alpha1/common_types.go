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

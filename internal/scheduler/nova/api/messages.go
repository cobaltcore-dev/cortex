// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import "log/slog"

// Host object from the Nova scheduler pipeline.
// See: https://github.com/sapcc/nova/blob/stable/xena-m3/nova/scheduler/host_manager.py class HostState
type ExternalSchedulerHost struct {
	// Name of the Nova compute host, e.g. nova-compute-bb123.
	ComputeHost string `json:"host"`
	// Name of the hypervisor hostname, e.g. domain-c123.<uuid>
	HypervisorHostname string `json:"hypervisor_hostname"`
}

// Request generated by the Nova scheduler when calling cortex.
// The request contains a spec of the VM to be scheduled, a list of hosts and
// their status, and a map of weights that were calculated by the Nova weigher
// pipeline. Some additional flags are also included.
type ExternalSchedulerRequest struct {
	Spec NovaObject[NovaSpec] `json:"spec"`

	// Request context from Nova that contains additional meta information.
	Context NovaRequestContext `json:"context"`

	// Whether the Nova scheduling request is a rebuild request.
	Rebuild bool `json:"rebuild"`
	// Whether the Nova scheduling request is a resize request.
	Resize bool `json:"resize"`
	// Whether the Nova scheduling request is a live migration.
	Live bool `json:"live"`
	// Whether the affected VM is a VMware VM.
	VMware bool `json:"vmware"`

	Hosts   []ExternalSchedulerHost `json:"hosts"`
	Weights map[string]float64      `json:"weights"`
}

// Conform to the PipelineRequest interface.

func (r ExternalSchedulerRequest) GetSubjects() []string {
	hosts := make([]string, len(r.Hosts))
	for i, host := range r.Hosts {
		hosts[i] = host.ComputeHost
	}
	return hosts
}
func (r ExternalSchedulerRequest) GetWeights() map[string]float64 {
	return r.Weights
}
func (r ExternalSchedulerRequest) GetTraceLogArgs() []slog.Attr {
	return []slog.Attr{
		slog.String("greq", r.Context.GlobalRequestID),
		slog.String("req", r.Context.RequestID),
		slog.String("user", r.Context.UserID),
		slog.String("project", r.Context.ProjectID),
	}
}

// Response generated by cortex for the Nova scheduler.
// Cortex returns an ordered list of hosts that the VM should be scheduled on.
type ExternalSchedulerResponse struct {
	Hosts []string `json:"hosts"`
}

// Wrapped Nova object. Nova returns objects in this format.
type NovaObject[V any] struct {
	Name      string   `json:"nova_object.name"`
	Namespace string   `json:"nova_object.namespace"`
	Version   string   `json:"nova_object.version"`
	Data      V        `json:"nova_object.data"`
	Changes   []string `json:"nova_object.changes"`
}

// Spec object from the Nova scheduler pipeline.
// See: https://github.com/sapcc/nova/blob/stable/xena-m3/nova/objects/request_spec.py
type NovaSpec struct {
	ProjectID        string                    `json:"project_id"`
	UserID           string                    `json:"user_id"`
	AvailabilityZone string                    `json:"availability_zone"`
	NInstances       int                       `json:"num_instances"`
	Image            NovaObject[NovaImageMeta] `json:"image"`
	Flavor           NovaObject[NovaFlavor]    `json:"flavor"`
}

// Nova image metadata for the specified VM.
type NovaImageMeta struct {
	Name    string `json:"name"`
	Size    int    `json:"size"`
	MinRAM  int    `json:"min_ram"`
	MinDisk int    `json:"min_disk"`
}

// Nova flavor metadata for the specified VM.
type NovaFlavor struct {
	Name            string            `json:"name"`
	MemoryMB        int               `json:"memory_mb"`
	VCPUs           int               `json:"vcpus"`
	RootDiskGB      int               `json:"root_gb"`
	EphemeralDiskGB int               `json:"ephemeral_gb"`
	FlavorID        string            `json:"flavorid"`
	Swap            int               `json:"swap"`
	RXTXFactor      float64           `json:"rxtx_factor"`
	VCPUsWeight     float64           `json:"vcpus_weight"`
	ExtraSpecs      map[string]string `json:"extra_specs"`
}

// Nova request context object. For the spec of this object, see:
//
// - This: https://github.com/sapcc/nova/blob/a56409/nova/context.py#L166
// - And: https://github.com/openstack/oslo.context/blob/db20dd/oslo_context/context.py#L329
//
// Some fields are omitted: "service_catalog", "read_deleted" (same as "show_deleted")
type NovaRequestContext struct {
	// Fields added by oslo.context

	UserID          string   `json:"user"`
	ProjectID       string   `json:"project_id"`
	SystemScope     string   `json:"system_scope"`
	DomainID        string   `json:"domain"`
	UserDomainID    string   `json:"user_domain"`
	ProjectDomainID string   `json:"project_domain"`
	IsAdmin         bool     `json:"is_admin"`
	ReadOnly        bool     `json:"read_only"`
	ShowDeleted     bool     `json:"show_deleted"`
	RequestID       string   `json:"request_id"`
	GlobalRequestID string   `json:"global_request_id"`
	ResourceUUID    string   `json:"resource_uuid"`
	Roles           []string `json:"roles"`
	UserIdentity    string   `json:"user_identity"`
	IsAdminProject  bool     `json:"is_admin_project"`

	// Fields added by the Nova scheduler

	RemoteAddress string `json:"remote_address"`
	Timestamp     string `json:"timestamp"`
	QuotaClass    string `json:"quota_class"`
	UserName      string `json:"user_name"`
	ProjectName   string `json:"project_name"`
}

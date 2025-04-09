// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

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
	AuthToken       string   `json:"auth_token"`
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

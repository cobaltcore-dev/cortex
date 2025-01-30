// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

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

type NovaImageMeta struct {
	Name    string `json:"name"`
	Size    int    `json:"size"`
	MinRAM  int    `json:"min_ram"`
	MinDisk int    `json:"min_disk"`
}

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

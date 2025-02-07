// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"time"
)

// One metric datapoint in the Prometheus timeline.
type PrometheusMetric interface {
	// Table name into which the metric should be stored.
	GetTableName() string
	// Name under which the metric is stored in Prometheus.
	GetName() string
	// Value of this metric datapoint.
	GetValue() float64
	// Set the time of this metric datapoint.
	SetTimestamp(time time.Time)
	// Set the value of this metric datapoint.
	SetValue(value float64)
}

// VROpsHostMetric represents a single metric value from Prometheus
// that was generated the VMware vROps exporter for a specific hostsystem.
// See: https://github.com/sapcc/vrops-exporter
type VROpsHostMetric struct {
	//lint:ignore U1000 Field is used by the ORM.
	tableName struct{} `pg:"vrops_host_metrics"`
	// The name of the metric.
	Name string `json:"__name__" pg:"name"`
	// Kubernetes cluster name in which the metrics exporter is running.
	Cluster string `json:"cluster" pg:"cluster"`
	// Kubernetes cluster type in which the metrics exporter is running.
	ClusterType string `json:"cluster_type" pg:"cluster_type"`
	// The name of the metrics collector.
	Collector string `json:"collector" pg:"collector"`
	// Datacenter / availability zone of the hostsystem.
	Datacenter string `json:"datacenter" pg:"datacenter"`
	// Host system name.
	// Note: this value does not necessarily correspond to the
	// hypervisor service host contained in OpenStack.
	HostSystem string `json:"hostsystem" pg:"hostsystem"`
	// Internal name of the hostsystem.
	InternalName string `json:"internal_name" pg:"internal_name"`
	// Exporter job name (usually "vrops-exporter").
	Job string `json:"job" pg:"job"`
	// Prometheus instance from which the metric was fetched.
	Prometheus string `json:"prometheus" pg:"prometheus"`
	// Datacenter region (one level above availability zone).
	Region string `json:"region" pg:"region"`
	// VMware vCenter cluster name in which the hostsystem is running.
	VCCluster string `json:"vccluster" pg:"vccluster"`
	// VMware vCenter name in which the hostsystem is running.
	VCenter string `json:"vcenter" pg:"vcenter"`
	// Timestamp of the metric value.
	Timestamp time.Time `json:"timestamp" pg:"timestamp"`
	// The value of the metric.
	Value float64 `json:"value" pg:"value"`
}

func (m *VROpsHostMetric) GetTableName() string     { return "vrops_host_metrics" }
func (m *VROpsHostMetric) GetName() string          { return m.Name }
func (m *VROpsHostMetric) GetTimestamp() time.Time  { return m.Timestamp }
func (m *VROpsHostMetric) SetTimestamp(t time.Time) { m.Timestamp = t }
func (m *VROpsHostMetric) GetValue() float64        { return m.Value }
func (m *VROpsHostMetric) SetValue(v float64)       { m.Value = v }

// VROpsVMMetric represents a single metric value from Prometheus
// that was generated the VMware vROps exporter for a specific virtual machine.
// See: https://github.com/sapcc/vrops-exporter
type VROpsVMMetric struct {
	//lint:ignore U1000 Field is used by the ORM.
	tableName struct{} `pg:"vrops_vm_metrics"`
	// The name of the metric.
	Name string `json:"__name__" pg:"name"`
	// Kubernetes cluster name in which the metrics exporter is running.
	Cluster string `json:"cluster" pg:"cluster"`
	// Kubernetes cluster type in which the metrics exporter is running.
	ClusterType string `json:"cluster_type" pg:"cluster_type"`
	// The name of the metrics collector.
	Collector string `json:"collector" pg:"collector"`
	// Datacenter / availability zone of the virtual machine.
	Datacenter string `json:"datacenter" pg:"datacenter"`
	// Host system of the virtual machine.
	// Note: this value does not necessarily correspond to the
	// hypervisor service host contained in OpenStack.
	HostSystem string `json:"hostsystem" pg:"hostsystem"`
	// Internal name of the virtual machine.
	InternalName string `json:"internal_name" pg:"internal_name"`
	// Exporter job name (usually "vrops-exporter").
	Job string `json:"job" pg:"job"`
	// OpenStack project ID of the virtual machine.
	Project string `json:"project" pg:"project"`
	// Prometheus instance from which the metric was fetched.
	Prometheus string `json:"prometheus" pg:"prometheus"`
	// Datacenter region (one level above availability zone).
	Region string `json:"region" pg:"region"`
	// VMware vCenter cluster name in which the virtual machine is running.
	VCCluster string `json:"vccluster" pg:"vccluster"`
	// VMware vCenter name in which the virtual machine is running.
	VCenter string `json:"vcenter" pg:"vcenter"`
	// Name of the virtual machine specified by the OpenStack user.
	VirtualMachine string `json:"virtualmachine" pg:"virtualmachine"`
	// OpenStack UUID of the virtual machine instance.
	// Note: not all instances may be seen in the current OpenStack environment.
	InstanceUUID string `json:"instance_uuid" pg:"instance_uuid"`
	// Timestamp of the metric value.
	Timestamp time.Time `json:"timestamp" pg:"timestamp"`
	// The value of the metric.
	Value float64 `json:"value" pg:"value"`
}

func (m *VROpsVMMetric) GetTableName() string     { return "vrops_vm_metrics" }
func (m *VROpsVMMetric) GetName() string          { return m.Name }
func (m *VROpsVMMetric) GetTimestamp() time.Time  { return m.Timestamp }
func (m *VROpsVMMetric) SetTimestamp(t time.Time) { m.Timestamp = t }
func (m *VROpsVMMetric) GetValue() float64        { return m.Value }
func (m *VROpsVMMetric) SetValue(v float64)       { m.Value = v }

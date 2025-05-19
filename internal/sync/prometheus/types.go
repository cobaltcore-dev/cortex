// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"time"

	"github.com/cobaltcore-dev/cortex/internal/db"
)

// One metric datapoint in the Prometheus timeline.
type PrometheusMetric interface {
	db.Table
	// Name of the metric.
	GetName() string
	// Value of this metric datapoint.
	GetValue() float64
	// Timestamp of this metric datapoint.
	GetTimestamp() time.Time
	// Create a new instance of this metric with time and value set
	// from a prometheus range metric query. Also pass a name attribute
	// so that multiple different metrics stored in the same table can
	// be distinguished.
	With(name string, time time.Time, value float64) PrometheusMetric
}

// VROpsHostMetric represents a single metric value from Prometheus
// that was generated the VMware vROps exporter for a specific hostsystem.
// See: https://github.com/sapcc/vrops-exporter
type VROpsHostMetric struct {
	// The name of the metric.
	Name string `db:"name"`
	// Kubernetes cluster name in which the metrics exporter is running.
	Cluster string `json:"cluster" db:"cluster"`
	// Kubernetes cluster type in which the metrics exporter is running.
	ClusterType string `json:"cluster_type" db:"cluster_type"`
	// The name of the metrics collector.
	Collector string `json:"collector" db:"collector"`
	// Datacenter / availability zone of the hostsystem.
	Datacenter string `json:"datacenter" db:"datacenter"`
	// Host system name.
	// Note: this value does not necessarily correspond to the
	// hypervisor service host contained in OpenStack.
	HostSystem string `json:"hostsystem" db:"hostsystem"`
	// Internal name of the hostsystem.
	InternalName string `json:"internal_name" db:"internal_name"`
	// Exporter job name (usually "vrops-exporter").
	Job string `json:"job" db:"job"`
	// Prometheus instance from which the metric was fetched.
	Prometheus string `json:"prometheus" db:"prometheus"`
	// Datacenter region (one level above availability zone).
	Region string `json:"region" db:"region"`
	// VMware vCenter cluster name in which the hostsystem is running.
	VCCluster string `json:"vccluster" db:"vccluster"`
	// VMware vCenter name in which the hostsystem is running.
	VCenter string `json:"vcenter" db:"vcenter"`
	// Timestamp of the metric value.
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	// The value of the metric.
	Value float64 `json:"value" db:"value"`
}

func (m VROpsHostMetric) TableName() string       { return "vrops_host_metrics" }
func (m VROpsHostMetric) Indexes() []db.Index     { return nil }
func (m VROpsHostMetric) GetName() string         { return m.Name }
func (m VROpsHostMetric) GetTimestamp() time.Time { return m.Timestamp }
func (m VROpsHostMetric) GetValue() float64       { return m.Value }
func (m VROpsHostMetric) With(n string, t time.Time, v float64) PrometheusMetric {
	m.Name = n
	m.Timestamp = t
	m.Value = v
	return m
}

// VROpsVMMetric represents a single metric value from Prometheus
// that was generated the VMware vROps exporter for a specific virtual machine.
// See: https://github.com/sapcc/vrops-exporter
type VROpsVMMetric struct {
	// The name of the metric.
	Name string `db:"name"`
	// Kubernetes cluster name in which the metrics exporter is running.
	Cluster string `json:"cluster" db:"cluster"`
	// Kubernetes cluster type in which the metrics exporter is running.
	ClusterType string `json:"cluster_type" db:"cluster_type"`
	// The name of the metrics collector.
	Collector string `json:"collector" db:"collector"`
	// Datacenter / availability zone of the virtual machine.
	Datacenter string `json:"datacenter" db:"datacenter"`
	// Host system of the virtual machine.
	// Note: this value does not necessarily correspond to the
	// hypervisor service host contained in OpenStack.
	HostSystem string `json:"hostsystem" db:"hostsystem"`
	// Internal name of the virtual machine.
	InternalName string `json:"internal_name" db:"internal_name"`
	// Exporter job name (usually "vrops-exporter").
	Job string `json:"job" db:"job"`
	// OpenStack project ID of the virtual machine.
	Project string `json:"project" db:"project"`
	// Prometheus instance from which the metric was fetched.
	Prometheus string `json:"prometheus" db:"prometheus"`
	// Datacenter region (one level above availability zone).
	Region string `json:"region" db:"region"`
	// VMware vCenter cluster name in which the virtual machine is running.
	VCCluster string `json:"vccluster" db:"vccluster"`
	// VMware vCenter name in which the virtual machine is running.
	VCenter string `json:"vcenter" db:"vcenter"`
	// Name of the virtual machine specified by the OpenStack user.
	VirtualMachine string `json:"virtualmachine" db:"virtualmachine"`
	// OpenStack UUID of the virtual machine instance.
	// Note: not all instances may be seen in the current OpenStack environment.
	InstanceUUID string `json:"instance_uuid" db:"instance_uuid"`
	// Timestamp of the metric value.
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	// The value of the metric.
	Value float64 `json:"value" db:"value"`
}

func (m VROpsVMMetric) TableName() string       { return "vrops_vm_metrics" }
func (m VROpsVMMetric) Indexes() []db.Index     { return nil }
func (m VROpsVMMetric) GetName() string         { return m.Name }
func (m VROpsVMMetric) GetTimestamp() time.Time { return m.Timestamp }
func (m VROpsVMMetric) GetValue() float64       { return m.Value }
func (m VROpsVMMetric) With(n string, t time.Time, v float64) PrometheusMetric {
	m.Name = n
	m.Timestamp = t
	m.Value = v
	return m
}

// Metric exported by node exporter.
// See: https://github.com/prometheus/node_exporter
type NodeExporterMetric struct {
	// The name of the metric.
	Name string `db:"name"`
	// Name of the kubernetes node.
	Node string `json:"node" db:"node"`
	// Timestamp of the metric value.
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	// The value of the metric.
	Value float64 `json:"value" db:"value"`
}

func (m NodeExporterMetric) TableName() string       { return "node_exporter_metrics" }
func (m NodeExporterMetric) Indexes() []db.Index     { return nil }
func (m NodeExporterMetric) GetName() string         { return m.Name }
func (m NodeExporterMetric) GetTimestamp() time.Time { return m.Timestamp }
func (m NodeExporterMetric) GetValue() float64       { return m.Value }
func (m NodeExporterMetric) With(n string, t time.Time, v float64) PrometheusMetric {
	m.Name = n
	m.Timestamp = t
	m.Value = v
	return m
}

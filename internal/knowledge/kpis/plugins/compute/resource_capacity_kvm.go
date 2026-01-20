// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// Assuming hypervisor names are in the format nodeXXX-bbYY
func getBuildingBlock(hostName string) string {
	parts := strings.Split(hostName, "-")
	if len(parts) > 1 {
		return parts[1]
	}
	return "unknown"
}

type KVMResourceCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config
	utilizedCapacityPerHost   *prometheus.Desc
	paygCapacityPerHost       *prometheus.Desc
	failoverCapacityPerHost   *prometheus.Desc
	reservedCapacityPerHost   *prometheus.Desc
	totalCapacityPerHost      *prometheus.Desc
}

func (KVMResourceCapacityKPI) GetName() string {
	return "kvm_host_capacity_kpi"
}

func (k *KVMResourceCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.utilizedCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_utilized",
		"Utilized resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"maintenance",
		},
		nil,
	)
	k.paygCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_payg",
		"PAYG resources available on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"maintenance",
		},
		nil,
	)
	k.reservedCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_reserved",
		"Reserved resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"maintenance",
		},
		nil,
	)
	k.failoverCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_failover",
		"Failover resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"maintenance",
		},
		nil,
	)
	k.totalCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_total",
		"Total resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"maintenance",
		},
		nil,
	)
	return nil
}

func (k *KVMResourceCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.utilizedCapacityPerHost
	ch <- k.paygCapacityPerHost
	ch <- k.reservedCapacityPerHost
	ch <- k.failoverCapacityPerHost
	ch <- k.totalCapacityPerHost
}

func (k *KVMResourceCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	// The hypervisor resource auto-discovers its current utilization.
	// We can use the hypervisor status to calculate the total capacity
	// and then subtract the actual resource allocation from virtual machines.
	hvs := &hv1.HypervisorList{}
	if err := k.Client.List(context.Background(), hvs); err != nil {
		slog.Error("failed to list hypervisors", "error", err)
		return
	}

	for _, hypervisor := range hvs.Items {
		cpuTotal, hasCPUTotal := hypervisor.Status.Capacity["cpu"]
		ramTotal, hasRAMTotal := hypervisor.Status.Capacity["memory"]

		if !hasCPUTotal || !hasRAMTotal {
			slog.Error("hypervisor missing cpu or ram total capacity", "hypervisor", hypervisor.Name)
			continue
		}

		cpuUsed, hasCPUUtilized := hypervisor.Status.Allocation["cpu"]
		if !hasCPUUtilized {
			cpuUsed = resource.MustParse("0")
		}

		ramUsed, hasRAMUtilized := hypervisor.Status.Allocation["memory"]
		if !hasRAMUtilized {
			ramUsed = resource.MustParse("0")
		}

		exportCapacityMetricKVM(ch, k.totalCapacityPerHost, "cpu", cpuTotal.AsApproximateFloat64(), hypervisor)
		exportCapacityMetricKVM(ch, k.totalCapacityPerHost, "ram", ramTotal.AsApproximateFloat64(), hypervisor)

		exportCapacityMetricKVM(ch, k.utilizedCapacityPerHost, "cpu", cpuUsed.AsApproximateFloat64(), hypervisor)
		exportCapacityMetricKVM(ch, k.utilizedCapacityPerHost, "ram", ramUsed.AsApproximateFloat64(), hypervisor)

		// WARNING: Using dummy data for now.
		// TODO Replace with actual data from reservations capacity CRDs
		cpuReserved := resource.MustParse("100")
		ramReserved := resource.MustParse("1Gi")

		exportCapacityMetricKVM(ch, k.reservedCapacityPerHost, "cpu", cpuReserved.AsApproximateFloat64(), hypervisor)
		exportCapacityMetricKVM(ch, k.reservedCapacityPerHost, "ram", ramReserved.AsApproximateFloat64(), hypervisor)

		// WARNING: Using dummy data for now.
		// TODO Replace with actual data from failover capacity CRDs
		cpuFailover := resource.MustParse("100")
		ramFailover := resource.MustParse("1Gi")

		exportCapacityMetricKVM(ch, k.failoverCapacityPerHost, "cpu", cpuFailover.AsApproximateFloat64(), hypervisor)
		exportCapacityMetricKVM(ch, k.failoverCapacityPerHost, "ram", ramFailover.AsApproximateFloat64(), hypervisor)

		// Calculate PAYG capacity
		paygCPU := cpuTotal.DeepCopy()
		paygCPU.Sub(cpuUsed)
		paygCPU.Sub(cpuReserved)
		paygCPU.Sub(cpuFailover)

		paygRAM := ramTotal.DeepCopy()
		paygRAM.Sub(ramUsed)
		paygRAM.Sub(ramReserved)
		paygRAM.Sub(ramFailover)

		exportCapacityMetricKVM(ch, k.paygCapacityPerHost, "cpu", paygCPU.AsApproximateFloat64(), hypervisor)
		exportCapacityMetricKVM(ch, k.paygCapacityPerHost, "ram", paygRAM.AsApproximateFloat64(), hypervisor)
	}
}

func exportCapacityMetricKVM(ch chan<- prometheus.Metric, metric *prometheus.Desc, resource string, value float64, hypervisor hv1.Hypervisor) {
	bb := getBuildingBlock(hypervisor.Name)

	availabilityZone := hypervisor.Labels["topology.kubernetes.io/zone"]

	enabled := true
	decommissioned := false
	externalCustomer := false
	maintenance := false

	workloadType := "general-purpose"
	cpuArchitecture := "cascade-lake"

	for _, trait := range hypervisor.Status.Traits {
		switch trait {
		case "CUSTOM_HW_SAPPHIRE_RAPIDS":
			cpuArchitecture = "sapphire-rapids"
		case "CUSTOM_HANA_EXCLUSIVE_HOST":
			workloadType = "hana"
		case "CUSTOM_DECOMMISSIONING":
			decommissioned = true
		case "CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED":
			externalCustomer = true
		}
	}

	ch <- prometheus.MustNewConstMetric(
		metric,
		prometheus.GaugeValue,
		value,
		hypervisor.Name,
		resource,
		availabilityZone,
		bb,
		cpuArchitecture,
		workloadType,
		strconv.FormatBool(enabled),
		strconv.FormatBool(decommissioned),
		strconv.FormatBool(externalCustomer),
		strconv.FormatBool(maintenance),
	)
}

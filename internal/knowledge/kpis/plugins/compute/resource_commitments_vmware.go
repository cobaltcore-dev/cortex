// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VMwareResourceCommitmentsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	unusedInstanceCommitments *prometheus.Desc
}

func (VMwareResourceCommitmentsKPI) GetName() string {
	return "vmware_commitments_kpi"
}

func (k *VMwareResourceCommitmentsKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.unusedInstanceCommitments = prometheus.NewDesc(
		"cortex_vmware_hana_unused_instance_commitments",
		"Unused instance commitment capacity summed across all projects (vcpus / ram_mb / disk_gb).",
		[]string{
			"resource", // "cpu", "ram", "disk"
			"availability_zone",
			"cpu_architecture", // "sapphire-rapids" (_v2 suffix) or "cascade-lake"
		},
		nil,
	)
	return nil
}

func (k *VMwareResourceCommitmentsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.unusedInstanceCommitments
}

func (k *VMwareResourceCommitmentsKPI) Collect(ch chan<- prometheus.Metric) {
	k.collectUnusedCommitments(ch)
}

func (k *VMwareResourceCommitmentsKPI) collectUnusedCommitments(ch chan<- prometheus.Metric) {
	if k.DB == nil {
		return
	}
	// Load confirmed/guaranteed instance commitments.
	var commitments []limes.Commitment
	if _, err := k.DB.Select(&commitments, `
		SELECT * FROM `+limes.Commitment{}.TableName()+`
		WHERE service_type = 'compute'
		  AND resource_name LIKE 'instances_%'
		  AND status IN ('confirmed', 'guaranteed')
	`); err != nil {
		slog.Error("unused_commitments: failed to load commitments", "err", err)
		return
	}

	// Load flavors for capacity lookup.
	var flavors []nova.Flavor
	if _, err := k.DB.Select(&flavors, "SELECT * FROM "+nova.Flavor{}.TableName()); err != nil {
		slog.Error("unused_commitments: failed to load flavors", "err", err)
		return
	}
	flavorsByName := make(map[string]nova.Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}

	// Load running HANA servers (non-deleted, non-error).
	var servers []nova.Server
	if _, err := k.DB.Select(&servers, `
		SELECT * FROM `+nova.Server{}.TableName()+`
		WHERE flavor_name LIKE 'hana_%'
		  AND status NOT IN ('DELETED', 'ERROR')
	`); err != nil {
		slog.Error("unused_commitments: failed to load servers", "err", err)
		return
	}
	// runningCount: (project_id, flavor_name, az) -> count
	type serverKey struct{ projectID, flavorName, az string }
	runningCount := make(map[serverKey]uint64)
	for _, server := range servers {
		key := serverKey{server.TenantID, server.FlavorName, server.OSEXTAvailabilityZone}
		runningCount[key]++
	}

	// committed: (project_id, flavor_name, az, cpuArchitecture) -> total committed amount
	type commitKey struct{ projectID, flavorName, az, cpuArchitecture string }
	committed := make(map[commitKey]uint64)
	for _, c := range commitments {
		flavorName := strings.TrimPrefix(c.ResourceName, "instances_")
		if !strings.HasPrefix(flavorName, "hana_") {
			continue
		}
		if strings.HasPrefix(flavorName, "hana_k_") {
			slog.Info("unused_commitments: skipping hana kvm commitment", "flavor", flavorName, "project_id", c.ProjectID)
			continue
		}
		cpuArchitecture := "cascade-lake"
		if strings.HasSuffix(flavorName, "_v2") {
			cpuArchitecture = "sapphire-rapids"
		}
		key := commitKey{c.ProjectID, flavorName, c.AvailabilityZone, cpuArchitecture}
		committed[key] += c.Amount
	}

	// For each (project, flavor, az, arch): unused = max(0, committed - running).
	// Accumulate capacity into sumByResource: (resource, az, arch) -> value.
	type resourceKey struct{ resource, az, arch string }
	sumByResource := make(map[resourceKey]float64)
	for ck, total := range committed {
		sk := serverKey{ck.projectID, ck.flavorName, ck.az}
		running := runningCount[sk]

		if running >= total {
			continue
		}
		unused := total - running
		flavor, ok := flavorsByName[ck.flavorName]
		if !ok {
			slog.Warn("unused_commitments: flavor not found in flavor table", "flavor", ck.flavorName)
			continue
		}
		sumByResource[resourceKey{"cpu", ck.az, ck.cpuArchitecture}] += float64(unused) * float64(flavor.VCPUs)
		sumByResource[resourceKey{"ram", ck.az, ck.cpuArchitecture}] += float64(unused) * float64(flavor.RAM)
		sumByResource[resourceKey{"disk", ck.az, ck.cpuArchitecture}] += float64(unused) * float64(flavor.Disk)
	}

	for rk, value := range sumByResource {
		ch <- prometheus.MustNewConstMetric(
			k.unusedInstanceCommitments,
			prometheus.GaugeValue,
			value,
			rk.resource,
			rk.az,
			rk.arch,
		)
	}
}

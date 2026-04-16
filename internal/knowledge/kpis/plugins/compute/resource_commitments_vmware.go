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

// getRunningHANAServers loads all running HANA servers from the database. We consider a server "running" if its status is not DELETED or ERROR.
func (k *VMwareResourceCommitmentsKPI) getRunningHANAServers() ([]nova.Server, error) {
	// Load running HANA servers (non-deleted, non-error).
	var servers []nova.Server
	if _, err := k.DB.Select(&servers, `
		SELECT * FROM `+nova.Server{}.TableName()+`
		WHERE flavor_name LIKE 'hana_%'
		  AND status NOT IN ('DELETED', 'ERROR')
	`); err != nil {
		return nil, err
	}
	return servers, nil
}

// getFlavorsByName loads all flavors from the database and returns a map of flavor name to flavor struct for easy lookup.
func (k *VMwareResourceCommitmentsKPI) getFlavorsByName() (map[string]nova.Flavor, error) {
	var flavors []nova.Flavor
	if _, err := k.DB.Select(&flavors, "SELECT * FROM "+nova.Flavor{}.TableName()); err != nil {
		return nil, err
	}
	flavorsByName := make(map[string]nova.Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}
	return flavorsByName, nil
}

// getInstanceCommitments loads all confirmed or guaranteed instance commitments from the database.
func (k *VMwareResourceCommitmentsKPI) getInstanceCommitments() ([]limes.Commitment, error) {
	var commitments []limes.Commitment
	if _, err := k.DB.Select(&commitments, `
		SELECT * FROM `+limes.Commitment{}.TableName()+`
		WHERE service_type = 'compute'
		  AND resource_name LIKE 'instances_%'
		  AND status IN ('confirmed', 'guaranteed')
	`); err != nil {
		return nil, err
	}
	return commitments, nil
}

// cpuArchitectureForFlavor returns the CPU architecture label for a HANA flavor name.
// Flavors with a "_v2" suffix run on sapphire-rapids; all others are cascade-lake.
func cpuArchitectureForFlavor(flavorName string) string {
	if strings.HasSuffix(flavorName, "_v2") {
		return "sapphire-rapids"
	}
	return "cascade-lake"
}

// resourceKey identifies an aggregated capacity bucket by (resource, az, architecture).
type resourceKey struct{ resource, az, architecture string }

// calculateUnusedInstanceCapacity computes per-(resource, az, architecture) capacity sums for unused
// HANA VMware commitments. It filters out non-HANA and KVM (hana_k_) commitments, then for each
// (project, flavor, az, architecture) bucket subtracts running servers from committed amount; over-used
// buckets are clamped to zero and omitted from the result.
func calculateUnusedInstanceCapacity(
	commitments []limes.Commitment,
	servers []nova.Server,
	flavorsByName map[string]nova.Flavor,
) map[resourceKey]float64 {
	// running: (project_id, flavor_name, az) -> count of non-deleted/non-error servers.
	type serverCountKey struct{ projectID, flavorName, az string }
	running := make(map[serverCountKey]uint64, len(servers))
	for _, s := range servers {
		running[serverCountKey{s.TenantID, s.FlavorName, s.OSEXTAvailabilityZone}]++
	}

	// committed: (project_id, flavor_name, az, cpuArchitecture) -> total committed amount.
	type commitmentKey struct{ projectID, flavorName, az, cpuArchitecture string }
	committed := make(map[commitmentKey]uint64)
	for _, c := range commitments {
		flavorName := strings.TrimPrefix(c.ResourceName, "instances_")
		if !strings.HasPrefix(flavorName, "hana_") {
			continue
		}
		if strings.HasPrefix(flavorName, "hana_k_") {
			slog.Debug("unused_commitments: skipping hana kvm commitment", "flavor", flavorName, "project_id", c.ProjectID)
			continue
		}
		key := commitmentKey{c.ProjectID, flavorName, c.AvailabilityZone, cpuArchitectureForFlavor(flavorName)}
		committed[key] += c.Amount
	}

	sum := make(map[resourceKey]float64)
	for ck, total := range committed {
		run := running[serverCountKey{ck.projectID, ck.flavorName, ck.az}]
		if run >= total {
			continue
		}
		unused := total - run
		flavor, ok := flavorsByName[ck.flavorName]
		if !ok {
			slog.Warn("unused_commitments: flavor not found in flavor table", "flavor", ck.flavorName)
			continue
		}
		sum[resourceKey{"cpu", ck.az, ck.cpuArchitecture}] += float64(unused) * float64(flavor.VCPUs)
		sum[resourceKey{"ram", ck.az, ck.cpuArchitecture}] += float64(unused) * float64(flavor.RAM)
		sum[resourceKey{"disk", ck.az, ck.cpuArchitecture}] += float64(unused) * float64(flavor.Disk)
	}
	return sum
}

func (k *VMwareResourceCommitmentsKPI) collectUnusedCommitments(ch chan<- prometheus.Metric) {
	if k.DB == nil {
		return
	}

	// Load confirmed/guaranteed instance commitments.
	commitments, err := k.getInstanceCommitments()
	if err != nil {
		slog.Error("unused_commitments: failed to load commitments", "err", err)
		return
	}

	// Load flavors for capacity lookup.
	flavorsByName, err := k.getFlavorsByName()
	if err != nil {
		slog.Error("unused_commitments: failed to load flavors", "err", err)
		return
	}

	// Load running HANA servers.
	servers, err := k.getRunningHANAServers()
	if err != nil {
		slog.Error("unused_commitments: failed to get running HANA servers", "err", err)
		return
	}

	sumByResource := calculateUnusedInstanceCapacity(commitments, servers, flavorsByName)

	for rk, value := range sumByResource {
		ch <- prometheus.MustNewConstMetric(
			k.unusedInstanceCommitments,
			prometheus.GaugeValue,
			value,
			rk.resource,
			rk.az,
			rk.architecture,
		)
	}
}

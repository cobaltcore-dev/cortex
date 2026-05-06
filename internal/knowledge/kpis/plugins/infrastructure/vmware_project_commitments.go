// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This KPI tracks committed resources in the VMware environment, based on commitments provided by Limes.
// For KVM we can map a commitment to a reservation on a specific host. In VMware this is not possible.
// For general purpose workload customer can specific amounts of resources.
// For HANA workloads customers commit a certain number of HANA instances (based on flavor).
// Like this it is possible to determine the workload type of a commitment.
// For general purpose workloads its not possible to differentiate the cpu architecture. To avoid weird behavior in a dashboard we don't export this label for the metric.
// For HANA flavors the cpu architecture is part of the flavor name (_v2 suffix for sapphire rapids, without suffix for cascade lake).
// For both types of workload however we can not determine on which host the commitment is fulfilled.
type VMwareProjectCommitmentsKPI struct {
	// BaseKPI provides common fields and methods for all KPIs, such as database connection and Kubernetes client.
	plugins.BaseKPI[struct{}]

	unusedGeneralPurposeCommitmentsPerProject *prometheus.Desc
	unusedHanaCommittedResourcesPerProject    *prometheus.Desc
}

func (k *VMwareProjectCommitmentsKPI) GetName() string {
	return "vmware_project_commitments_kpi"
}

func (k *VMwareProjectCommitmentsKPI) Init(dbConn *db.DB, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(dbConn, c, opts); err != nil {
		return err
	}

	k.unusedGeneralPurposeCommitmentsPerProject = prometheus.NewDesc(
		"cortex_vmware_commitments_general_purpose",
		"Committed general purpose resources that are currently unused. CPU (resource=cpu) in vCPUs, memory (resource=ram) in bytes.",
		[]string{"availability_zone", "resource", "project_id", "project_name", "domain_id", "domain_name"}, nil,
	)
	k.unusedHanaCommittedResourcesPerProject = prometheus.NewDesc(
		"cortex_vmware_commitments_hana_resources",
		"Total committed HANA instances capacity that is currently unused, translated to resources. CPU in vCPUs, memory and disk in bytes.",
		[]string{"availability_zone", "cpu_architecture", "resource", "project_id", "project_name", "domain_id", "domain_name"}, nil,
	)
	return nil
}

func (k *VMwareProjectCommitmentsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.unusedGeneralPurposeCommitmentsPerProject
	ch <- k.unusedHanaCommittedResourcesPerProject
}

func (k *VMwareProjectCommitmentsKPI) Collect(ch chan<- prometheus.Metric) {
	if k.DB == nil {
		return
	}

	flavorsByName, err := k.getFlavorsByName()
	if err != nil {
		slog.Error("vmware_project_commitments: failed to load flavors", "err", err)
		return
	}

	projects, err := k.getProjectsWithDomains()
	if err != nil {
		slog.Error("vmware_project_commitments: failed to load projects with domains", "err", err)
		return
	}

	k.collectGeneralPurpose(ch, flavorsByName, projects)
	k.collectHana(ch, flavorsByName, projects)
}

// getFlavorsByName loads all flavors and returns them keyed by name.
func (k *VMwareProjectCommitmentsKPI) getFlavorsByName() (map[string]nova.Flavor, error) {
	var flavors []nova.Flavor
	if _, err := k.DB.Select(&flavors, "SELECT * FROM "+nova.Flavor{}.TableName()); err != nil {
		return nil, err
	}
	byName := make(map[string]nova.Flavor, len(flavors))
	for _, f := range flavors {
		byName[f.Name] = f
	}
	return byName, nil
}

// getGeneralPurposeCommitments loads confirmed/guaranteed cores and ram commitments.
func (k *VMwareProjectCommitmentsKPI) getGeneralPurposeCommitments() ([]limes.Commitment, error) {
	var commitments []limes.Commitment
	if _, err := k.DB.Select(&commitments, `
		SELECT * FROM `+limes.Commitment{}.TableName()+`
		WHERE service_type = 'compute'
		  AND resource_name IN ('cores', 'ram')
		  AND status IN ('confirmed', 'guaranteed')
	`); err != nil {
		return nil, err
	}
	return commitments, nil
}

// getGeneralPurposeServers loads running non-HANA servers for general purpose usage accounting.
// KVM-specific flavors are filtered out in Go since SQL LIKE cannot express the segment-exact pattern.
func (k *VMwareProjectCommitmentsKPI) getGeneralPurposeServers() ([]nova.Server, error) {
	var servers []nova.Server
	if _, err := k.DB.Select(&servers, `
		SELECT * FROM `+nova.Server{}.TableName()+`
		WHERE status NOT IN ('DELETED', 'ERROR')
		  AND flavor_name NOT LIKE 'hana_%'
	`); err != nil {
		return nil, err
	}
	result := make([]nova.Server, 0, len(servers))
	for _, s := range servers {
		if !isKVMFlavor(s.FlavorName) {
			result = append(result, s)
		}
	}
	return result, nil
}

// getHanaInstanceCommitments loads confirmed/guaranteed HANA instance commitments.
func (k *VMwareProjectCommitmentsKPI) getHanaInstanceCommitments() ([]limes.Commitment, error) {
	var commitments []limes.Commitment
	if _, err := k.DB.Select(&commitments, `
		SELECT * FROM `+limes.Commitment{}.TableName()+`
		WHERE service_type = 'compute'
		  AND resource_name LIKE 'instances_hana_%'
		  AND status IN ('confirmed', 'guaranteed')
	`); err != nil {
		return nil, err
	}
	return commitments, nil
}

// getRunningHanaServers loads all running HANA VMware servers (KVM HANA flavors excluded in Go).
func (k *VMwareProjectCommitmentsKPI) getRunningHanaServers() ([]nova.Server, error) {
	var servers []nova.Server
	if _, err := k.DB.Select(&servers, `
		SELECT * FROM `+nova.Server{}.TableName()+`
		WHERE status NOT IN ('DELETED', 'ERROR')
		  AND flavor_name LIKE 'hana_%'
	`); err != nil {
		return nil, err
	}
	result := make([]nova.Server, 0, len(servers))
	for _, s := range servers {
		if !isKVMFlavor(s.FlavorName) {
			result = append(result, s)
		}
	}
	return result, nil
}

// collectGeneralPurpose computes and emits unused general purpose committed resources per project.
// Unused = committed - in-use (clamped to zero; zero values are not emitted).
func (k *VMwareProjectCommitmentsKPI) collectGeneralPurpose(ch chan<- prometheus.Metric, flavorsByName map[string]nova.Flavor, projects map[string]projectWithDomain) {
	commitments, err := k.getGeneralPurposeCommitments()
	if err != nil {
		slog.Error("vmware_project_commitments: failed to load gp commitments", "err", err)
		return
	}
	servers, err := k.getGeneralPurposeServers()
	if err != nil {
		slog.Error("vmware_project_commitments: failed to load gp servers", "err", err)
		return
	}

	type gpKey struct{ projectID, az, resource string }

	committed := make(map[gpKey]float64)
	for _, c := range commitments {
		switch c.ResourceName {
		case "cores":
			committed[gpKey{c.ProjectID, c.AvailabilityZone, "cpu"}] += float64(c.Amount)
		case "ram":
			bytes, err := bytesFromUnit(float64(c.Amount), c.Unit)
			if err != nil {
				slog.Warn("vmware_project_commitments: unknown ram unit", "unit", c.Unit, "err", err)
				continue
			}
			committed[gpKey{c.ProjectID, c.AvailabilityZone, "ram"}] += bytes
		}
	}

	used := make(map[gpKey]float64)
	for _, s := range servers {
		flavor, ok := flavorsByName[s.FlavorName]
		if !ok {
			slog.Warn("vmware_project_commitments: gp flavor not found", "flavor", s.FlavorName)
			continue
		}
		used[gpKey{s.TenantID, s.OSEXTAvailabilityZone, "cpu"}] += float64(flavor.VCPUs)
		used[gpKey{s.TenantID, s.OSEXTAvailabilityZone, "ram"}] += float64(flavor.RAM) * 1024 * 1024
	}

	for key, committedAmt := range committed {
		unused := committedAmt - used[key]
		if unused <= 0 {
			continue
		}
		project := projects[key.projectID]
		ch <- prometheus.MustNewConstMetric(
			k.unusedGeneralPurposeCommitmentsPerProject,
			prometheus.GaugeValue,
			unused,
			key.az, key.resource, key.projectID, project.ProjectName, project.DomainID, project.DomainName,
		)
	}
}

// collectHana computes and emits unused committed HANA instance resources per project.
// Each HANA instance commitment is compared against running servers; the remainder is
// translated to cpu/ram/disk capacity using the flavor spec.
func (k *VMwareProjectCommitmentsKPI) collectHana(ch chan<- prometheus.Metric, flavorsByName map[string]nova.Flavor, projects map[string]projectWithDomain) {
	commitments, err := k.getHanaInstanceCommitments()
	if err != nil {
		slog.Error("vmware_resource_commitments: failed to load hana commitments", "err", err)
		return
	}
	servers, err := k.getRunningHanaServers()
	if err != nil {
		slog.Error("vmware_resource_commitments: failed to load hana servers", "err", err)
		return
	}

	type serverKey struct{ projectID, flavorName, az string }
	running := make(map[serverKey]uint64, len(servers))
	for _, s := range servers {
		running[serverKey{s.TenantID, s.FlavorName, s.OSEXTAvailabilityZone}]++
	}

	type commitKey struct{ projectID, flavorName, az, cpuArch string }
	committedInstances := make(map[commitKey]uint64)
	for _, c := range commitments {
		flavorName := strings.TrimPrefix(c.ResourceName, "instances_")
		if isKVMFlavor(flavorName) {
			continue
		}
		key := commitKey{c.ProjectID, flavorName, c.AvailabilityZone, flavorCPUArchitecture(flavorName)}
		committedInstances[key] += c.Amount
	}

	type resourceKey struct{ projectID, az, cpuArch, resource string }
	totals := make(map[resourceKey]float64)
	for ck, total := range committedInstances {
		run := running[serverKey{ck.projectID, ck.flavorName, ck.az}]
		if run >= total {
			continue
		}
		unused := total - run
		flavor, ok := flavorsByName[ck.flavorName]
		if !ok {
			slog.Warn("vmware_resource_commitments: hana flavor not found", "flavor", ck.flavorName)
			continue
		}
		totals[resourceKey{ck.projectID, ck.az, ck.cpuArch, "cpu"}] += float64(unused) * float64(flavor.VCPUs)
		totals[resourceKey{ck.projectID, ck.az, ck.cpuArch, "ram"}] += float64(unused) * float64(flavor.RAM) * 1024 * 1024
		totals[resourceKey{ck.projectID, ck.az, ck.cpuArch, "disk"}] += float64(unused) * float64(flavor.Disk) * 1024 * 1024 * 1024
	}

	for key, value := range totals {
		project := projects[key.projectID]
		ch <- prometheus.MustNewConstMetric(
			k.unusedHanaCommittedResourcesPerProject,
			prometheus.GaugeValue,
			value,
			key.az, key.cpuArch, key.resource, key.projectID, project.ProjectName, project.DomainID, project.DomainName,
		)
	}
}

type projectWithDomain struct {
	ProjectID   string `db:"project_id"`
	ProjectName string `db:"project_name"`
	DomainID    string `db:"domain_id"`
	DomainName  string `db:"domain_name"`
}

func (k *VMwareProjectCommitmentsKPI) getProjectsWithDomains() (map[string]projectWithDomain, error) {
	var projects []projectWithDomain
	if _, err := k.DB.Select(&projects, `
		SELECT p.id AS project_id, p.name AS project_name, COALESCE(d.id, '') AS domain_id, COALESCE(d.name, '') AS domain_name
		FROM `+identity.Project{}.TableName()+` p
		LEFT JOIN `+identity.Domain{}.TableName()+` d ON p.domain_id = d.id
	`); err != nil {
		return nil, err
	}

	projectMap := make(map[string]projectWithDomain, len(projects))
	for _, p := range projects {
		projectMap[p.ProjectID] = p
	}
	return projectMap, nil
}

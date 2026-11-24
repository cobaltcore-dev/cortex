// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/limes"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
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

func (k *VMwareProjectCommitmentsKPI) Init(inputs plugins.Inputs, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(inputs, c, opts); err != nil {
		return err
	}

	k.unusedGeneralPurposeCommitmentsPerProject = prometheus.NewDesc(
		"cortex_vmware_unused_commitments_general_purpose",
		"Committed general purpose resources that are currently unused. CPU (resource=cpu) in vCPUs, memory (resource=ram) in bytes.",
		[]string{"availability_zone", "resource", "project_id", "project_name", "domain_id", "domain_name"}, nil,
	)
	k.unusedHanaCommittedResourcesPerProject = prometheus.NewDesc(
		"cortex_vmware_unused_commitments_hana_resources",
		"Total committed HANA instances capacity that is currently unused, translated to resources. CPU in vCPUs, memory and disk in bytes.",
		[]string{"availability_zone", "cpu_architecture", "resource", "project_id", "project_name", "domain_id", "domain_name"}, nil,
	)
	return nil
}

func (k *VMwareProjectCommitmentsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.unusedGeneralPurposeCommitmentsPerProject
	ch <- k.unusedHanaCommittedResourcesPerProject
}

// Inputs of the vmware project commitments kpi.
type VMwareProjectCommitmentsInputs struct {
	Servers     []nova.Server      `input:"servers"`
	Flavors     []nova.Flavor      `input:"flavors"`
	Commitments []limes.Commitment `input:"commitments"`
	Projects    []identity.Project `input:"projects"`
	Domains     []identity.Domain  `input:"domains"`
}

func (k *VMwareProjectCommitmentsKPI) Collect(ch chan<- prometheus.Metric) {
	var in VMwareProjectCommitmentsInputs
	if err := k.ResolveInputs(context.Background(), &in); err != nil {
		log.Error(err, "vmware_project_commitments: failed to resolve inputs")
		return
	}
	flavorsByName := make(map[string]nova.Flavor, len(in.Flavors))
	for _, f := range in.Flavors {
		flavorsByName[f.Name] = f
	}
	projects := getProjectsWithDomains(in.Projects, in.Domains)
	k.collectGeneralPurpose(ch, in, flavorsByName, projects)
	k.collectHana(ch, in, flavorsByName, projects)
}

// Whether the commitment is a confirmed or guaranteed compute commitment.
func isActiveComputeCommitment(c limes.Commitment) bool {
	return c.ServiceType == "compute" && (c.Status == "confirmed" || c.Status == "guaranteed")
}

// getGeneralPurposeCommitments filters confirmed/guaranteed cores and ram commitments.
func getGeneralPurposeCommitments(commitments []limes.Commitment) []limes.Commitment {
	var result []limes.Commitment
	for _, c := range commitments {
		if isActiveComputeCommitment(c) && (c.ResourceName == "cores" || c.ResourceName == "ram") {
			result = append(result, c)
		}
	}
	return result
}

// getGeneralPurposeServers filters running non-HANA VMware servers for
// general purpose usage accounting.
func getGeneralPurposeServers(servers []nova.Server) []nova.Server {
	var result []nova.Server
	for _, s := range servers {
		if isActiveServer(s) && !strings.HasPrefix(s.FlavorName, "hana_") && !isKVMFlavor(s.FlavorName) {
			result = append(result, s)
		}
	}
	return result
}

// getHanaInstanceCommitments filters confirmed/guaranteed HANA instance commitments.
func getHanaInstanceCommitments(commitments []limes.Commitment) []limes.Commitment {
	var result []limes.Commitment
	for _, c := range commitments {
		if isActiveComputeCommitment(c) && strings.HasPrefix(c.ResourceName, "instances_hana_") {
			result = append(result, c)
		}
	}
	return result
}

// getRunningHanaServers filters running HANA VMware servers.
func getRunningHanaServers(servers []nova.Server) []nova.Server {
	var result []nova.Server
	for _, s := range servers {
		if isActiveServer(s) && strings.HasPrefix(s.FlavorName, "hana_") && !isKVMFlavor(s.FlavorName) {
			result = append(result, s)
		}
	}
	return result
}

// collectGeneralPurpose computes and emits unused general purpose committed resources per project.
// Unused = committed - in-use (clamped to zero; zero values are not emitted).
func (k *VMwareProjectCommitmentsKPI) collectGeneralPurpose(ch chan<- prometheus.Metric, in VMwareProjectCommitmentsInputs, flavorsByName map[string]nova.Flavor, projects map[string]projectWithDomain) {
	commitments := getGeneralPurposeCommitments(in.Commitments)
	servers := getGeneralPurposeServers(in.Servers)

	type gpKey struct{ projectID, az, resource string }

	committed := make(map[gpKey]float64)
	for _, c := range commitments {
		switch c.ResourceName {
		case "cores":
			committed[gpKey{c.ProjectID, c.AvailabilityZone, "cpu"}] += float64(c.Amount)
		case "ram":
			bytes, err := bytesFromUnit(float64(c.Amount), c.Unit)
			if err != nil {
				log.Error(err, "vmware_project_commitments: unknown ram unit", "unit", c.Unit)
				continue
			}
			committed[gpKey{c.ProjectID, c.AvailabilityZone, "ram"}] += bytes
		}
	}

	used := make(map[gpKey]float64)
	for _, s := range servers {
		flavor, ok := flavorsByName[s.FlavorName]
		if !ok {
			log.V(1).Info("vmware_project_commitments: gp flavor not found", "flavor", s.FlavorName)
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
func (k *VMwareProjectCommitmentsKPI) collectHana(ch chan<- prometheus.Metric, in VMwareProjectCommitmentsInputs, flavorsByName map[string]nova.Flavor, projects map[string]projectWithDomain) {
	commitments := getHanaInstanceCommitments(in.Commitments)
	servers := getRunningHanaServers(in.Servers)

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
			log.V(1).Info("vmware_resource_commitments: hana flavor not found", "flavor", ck.flavorName)
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
	ProjectID   string
	ProjectName string
	DomainID    string
	DomainName  string
}

// Look up projects with the id and name of their domain, which are empty if
// the domain is unknown.
func getProjectsWithDomains(projects []identity.Project, domains []identity.Domain) map[string]projectWithDomain {
	domainsByID := make(map[string]identity.Domain, len(domains))
	for _, d := range domains {
		domainsByID[d.ID] = d
	}
	projectMap := make(map[string]projectWithDomain, len(projects))
	for _, p := range projects {
		pd := projectWithDomain{ProjectID: p.ID, ProjectName: p.Name}
		if d, ok := domainsByID[p.DomainID]; ok {
			pd.DomainID, pd.DomainName = d.ID, d.Name
		}
		projectMap[p.ID] = pd
	}
	return projectMap
}

func init() {
	plugins.Index["vmware_project_commitments_kpi"] = func() plugins.KPI { return &VMwareProjectCommitmentsKPI{} }
}

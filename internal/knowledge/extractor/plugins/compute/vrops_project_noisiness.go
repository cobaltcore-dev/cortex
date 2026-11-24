// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"cmp"
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// Feature that maps the average cpu usage of a project to the compute hosts
// on which the project's vms are running.
type VROpsProjectNoisiness struct {
	Project         string  `db:"project"`
	ComputeHost     string  `db:"compute_host"`
	AvgCPUOfProject float64 `db:"avg_cpu_of_project"`
}

// Inputs of the vROps project noisiness extractor.
type VROpsProjectNoisinessInputs struct {
	// The vROps virtual machine cpu demand ratio metrics.
	Metrics     []prometheus.VROpsVMMetric `input:"metrics"`
	Servers     []nova.Server              `input:"servers"`
	Hypervisors []nova.Hypervisor          `input:"hypervisors"`
}

// Extractor that extracts the noisiness of projects.
type VROpsProjectNoisinessExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract the noisiness of each project with the following steps:
//  1. Get the average cpu usage of each project through the vROps metrics.
//  2. Find on which hosts the projects are currently running through the
//     OpenStack servers with metrics and the hypervisors.
//  3. Map the average cpu usage of the project to each of these hosts.
//
// This feature can then be used to draw new VMs away from VMs of the same
// project in case this project is known to cause high cpu usage.
func (e *VROpsProjectNoisinessExtractor) Extract(_ context.Context, in VROpsProjectNoisinessInputs) ([]VROpsProjectNoisiness, error) {
	type sum struct {
		total float64
		n     int
	}
	sumByProject := make(map[string]sum)
	serversWithMetrics := make(map[string]struct{})
	for _, m := range in.Metrics {
		s := sumByProject[m.Project]
		sumByProject[m.Project] = sum{s.total + m.Value, s.n + 1}
		serversWithMetrics[m.InstanceUUID] = struct{}{}
	}
	hostsByHostname := make(map[string][]string)
	for _, h := range in.Hypervisors {
		hostsByHostname[h.Hostname] = append(hostsByHostname[h.Hostname], h.ServiceHost)
	}
	type key struct{ project, computeHost string }
	noisiness := make(map[key]float64)
	for _, s := range in.Servers {
		if _, ok := serversWithMetrics[s.ID]; !ok {
			continue
		}
		projectSum, ok := sumByProject[s.TenantID]
		if !ok {
			continue
		}
		for _, computeHost := range hostsByHostname[s.OSEXTSRVATTRHypervisorHostname] {
			noisiness[key{s.TenantID, computeHost}] = projectSum.total / float64(projectSum.n)
		}
	}
	features := make([]VROpsProjectNoisiness, 0, len(noisiness))
	for k, avg := range noisiness {
		features = append(features, VROpsProjectNoisiness{Project: k.project, ComputeHost: k.computeHost, AvgCPUOfProject: avg})
	}
	slices.SortFunc(features, func(a, b VROpsProjectNoisiness) int {
		return cmp.Or(cmp.Compare(a.Project, b.Project), cmp.Compare(a.ComputeHost, b.ComputeHost))
	})
	return features, nil
}

func init() {
	extractor.Register("vrops_project_noisiness_extractor", func() extractor.Extractor[VROpsProjectNoisinessInputs, VROpsProjectNoisiness] {
		return &VROpsProjectNoisinessExtractor{}
	})
}

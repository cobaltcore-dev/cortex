// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
)

// Simulate the scheduling of a VM that belongs to a noisy project.
// This function fetches the noisy projects from the DB and sends a
// scheduling request for the most noisy project.
func SimulateNoisyVMScheduling() {
	db := db.NewDB()
	db.Init()
	defer db.Close()

	// Get noisy projects from the DB.
	var noisyProjects []vmware.VROpsProjectNoisiness
	err := db.Get().Model(&noisyProjects).Order("avg_cpu_of_project DESC").Select()
	if err != nil {
		logging.Log.Error("failed to get noisy projects", "error", err)
		return
	}
	if len(noisyProjects) == 0 {
		logging.Log.Info("no noisy projects found")
		return
	}

	// Get all hosts from the DB.
	var hypervisors []openstack.OpenStackHypervisor
	if err := db.Get().Model(&hypervisors).Select(); err != nil {
		logging.Log.Error("failed to get hosts", "error", err)
		return
	}

	// Make a scheduling request for a random noisy project.
	project := noisyProjects[0].Project
	logging.Log.Info("scheduling request", "project", project)

	spec := scheduler.NovaObject[scheduler.NovaSpec]{
		Data: scheduler.NovaSpec{
			ProjectID:  project,
			NInstances: 1,
		},
	}
	hosts := make([]scheduler.APINovaExternalSchedulerRequestHost, len(hypervisors))
	weights := make(map[string]float64)
	for i, hypervisor := range hypervisors {
		hosts[i] = scheduler.APINovaExternalSchedulerRequestHost{
			ComputeHost:        hypervisor.ServiceHost,
			HypervisorHostname: hypervisor.Hostname,
		}
		weights[hypervisor.ServiceHost] = 1.0
	}
	request := scheduler.APINovaExternalSchedulerRequest{
		Spec:    spec,
		Rebuild: false,
		VMware:  true,
		Hosts:   hosts,
		Weights: weights,
	}

	url := "http://localhost:8080/scheduler/nova/external"
	logging.Log.Info("sending POST request", "url", url)
	requestBody, err := json.Marshal(request)
	if err != nil {
		logging.Log.Error("failed to marshal request", "error", err)
		return
	}
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(requestBody))
	if err != nil {
		logging.Log.Error("failed to create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logging.Log.Error("failed to send POST request", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logging.Log.Error("received non-OK response", "status", resp.StatusCode)
		return
	}

	// Print out response json (without unmarshalling it)
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		logging.Log.Error("failed to read response", "error", err)
		return
	}
	logging.Log.Info("received response", "body", buf.String())
}

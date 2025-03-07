// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
)

// Simulate the scheduling of a VM.
func SimulateVMScheduling() {
	db := db.NewPostgresDB(conf.DBConfig{
		Host:     "localhost",
		Port:     "5432",
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	})
	defer db.Close()

	// Get all hosts from the DB.
	var hypervisors []openstack.Hypervisor
	if _, err := db.Select(&hypervisors, `SELECT * FROM openstack_hypervisors`); err != nil {
		slog.Error("failed to get hosts", "error", err)
		return
	}

	spec := scheduler.NovaObject[scheduler.NovaSpec]{
		Data: scheduler.NovaSpec{
			ProjectID:  "my-project",
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
	slog.Info("sending POST request", "url", url)
	requestBody, err := json.Marshal(request)
	if err != nil {
		slog.Error("failed to marshal request", "error", err)
		return
	}
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(requestBody))
	if err != nil {
		slog.Error("failed to create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("failed to send POST request", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("received non-OK response", "status", resp.StatusCode)
		return
	}

	// Print out response json (without unmarshalling it)
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		slog.Error("failed to read response", "error", err)
		return
	}
	slog.Info("received response", "body", buf.String())
}

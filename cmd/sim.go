package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/cobaltcore-dev/cortex/internal/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
)

func simulateNoisyVMScheduling() {
	db.Init()
	defer db.DB.Close()

	// Get noisy projects from the DB.
	var noisyProjects []features.ProjectNoisiness
	err := db.DB.Model(&noisyProjects).Order("avg_cpu_of_project DESC").Select()
	if err != nil {
		logging.Log.Error("failed to get noisy projects", "error", err)
		return
	}

	// Get all hosts from the DB.
	var hypervisors []openstack.OpenStackHypervisor
	if err := db.DB.Model(&hypervisors).Select(); err != nil {
		logging.Log.Error("failed to get hosts", "error", err)
		return
	}

	// Make a scheduling request for a random noisy project.
	project := noisyProjects[0].Project
	logging.Log.Info("scheduling request", "project", project)

	spec := scheduler.APINovaExternalSchedulerRequestSpec{
		ProjectId:  project,
		NInstances: 1,
	}
	hosts := make([]scheduler.APINovaExternalSchedulerRequestHost, len(hypervisors))
	weights := make(map[string]float64)
	for i, hypervisor := range hypervisors {
		hosts[i] = scheduler.APINovaExternalSchedulerRequestHost{
			Name:   hypervisor.ServiceHost,
			Status: hypervisor.Status,
		}
		weights[hypervisor.ServiceHost] = 1.0
	}
	request := scheduler.APINovaExternalSchedulerRequest{
		Spec:    spec,
		Rebuild: false,
		Hosts:   hosts,
		Weights: weights,
	}

	url := fmt.Sprintf("http://localhost:8080%s", scheduler.APINovaExternalSchedulerURL)
	logging.Log.Info("sending POST request", "url", url)

	// Send the POST request
	requestBody, err := json.Marshal(request)
	if err != nil {
		logging.Log.Error("failed to marshal request", "error", err)
		return
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		logging.Log.Error("failed to send POST request", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logging.Log.Error("received non-OK response", "status", resp.StatusCode)
		return
	}

	var response scheduler.APINovaExternalSchedulerResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logging.Log.Error("failed to decode response", "error", err)
		return
	}

	logging.Log.Info("received response", "hosts", len(response.Hosts))
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		logging.Log.Error("usage: sim [--noisy]")
		os.Exit(1)
	}
	if args[0] == "--noisy" {
		simulateNoisyVMScheduling()
		os.Exit(0)
	}
}

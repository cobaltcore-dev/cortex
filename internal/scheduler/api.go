// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/logging"
)

var (
	// NovaExternalSchedulerURL is the URL of the Nova external scheduler
	APINovaExternalSchedulerURL = "/scheduler/nova/external"
)

type apiNovaExternalSchedulerRequest struct {
	Spec struct { // Note: Not all fields are modeled here.
		ProjectId  string `json:"project_id"`
		NInstances int    `json:"num_instances"`
	}
	Rebuild bool `json:"rebuild"`
	Hosts   []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"hosts"`
	Weights map[string]float64 `json:"weights"`
}

type apiNovaExternalSchedulerResponse struct {
	Hosts []string `json:"hosts"`
}

func canRunScheduler(requestData apiNovaExternalSchedulerRequest) (bool, string) {
	if requestData.Rebuild {
		return false, "rebuild is not supported"
	}
	if requestData.Spec.NInstances > 1 {
		return false, "only one instance is supported"
	}
	return true, ""
}

func APINovaExternalSchedulerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	var requestData apiNovaExternalSchedulerRequest
	err := json.NewDecoder(r.Body).Decode(&requestData)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	logging.Log.Info("handling POST request", "url", APINovaExternalSchedulerURL, "requestData", requestData)

	if ok, reason := canRunScheduler(requestData); !ok {
		fmt.Printf("Cannot run scheduler: %s\n", reason)
		http.Error(w, reason, http.StatusBadRequest)
		return
	}

	// Create the pipeline context from the request data.
	state := pipelineState{}
	state.Spec.ProjectId = requestData.Spec.ProjectId
	for _, host := range requestData.Hosts {
		state.Hosts = append(state.Hosts, struct {
			Name   string
			Status string
		}{
			Name:   host.Name,
			Status: host.Status,
		})
	}
	state.Weights = requestData.Weights

	hosts, err := evaluatePipeline(state)
	if err != nil {
		http.Error(w, "Failed to evaluate pipeline", http.StatusInternalServerError)
		return
	}

	response := apiNovaExternalSchedulerResponse{
		Hosts: hosts,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

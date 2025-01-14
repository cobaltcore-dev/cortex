// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"encoding/json"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/logging"
)

var (
	// NovaExternalSchedulerURL is the URL of the Nova external scheduler
	APINovaExternalSchedulerURL = "/scheduler/nova/external"
)

type APINovaExternalSchedulerRequestSpec struct {
	ProjectID  string `json:"project_id"`
	NInstances int    `json:"num_instances"`
}

type APINovaExternalSchedulerRequestHost struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type APINovaExternalSchedulerRequest struct {
	Spec    APINovaExternalSchedulerRequestSpec   `json:"spec"`
	Rebuild bool                                  `json:"rebuild"`
	Hosts   []APINovaExternalSchedulerRequestHost `json:"hosts"`
	Weights map[string]float64                    `json:"weights"`
}

type APINovaExternalSchedulerResponse struct {
	Hosts []string `json:"hosts"`
}

func canRunScheduler(requestData APINovaExternalSchedulerRequest) (ok bool, reason string) {
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
		logging.Log.Error("invalid request method", "method", r.Method)
		http.Error(w, "invalid request method", http.StatusMethodNotAllowed)
		return
	}
	var requestData APINovaExternalSchedulerRequest
	err := json.NewDecoder(r.Body).Decode(&requestData)
	if err != nil {
		logging.Log.Error("failed to decode request", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	logging.Log.Info(
		"handling POST request",
		"url", APINovaExternalSchedulerURL,
		"rebuild", requestData.Rebuild,
		"hosts", len(requestData.Hosts),
		"spec", requestData.Spec,
	)

	if ok, reason := canRunScheduler(requestData); !ok {
		logging.Log.Error("cannot run scheduler", "reason", reason)
		http.Error(w, reason, http.StatusBadRequest)
		return
	}

	// Create the pipeline context from the request data.
	state := pipelineState{}
	state.Spec.ProjectID = requestData.Spec.ProjectID
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
		logging.Log.Error("failed to evaluate pipeline", "error", err)
		http.Error(w, "failed to evaluate pipeline", http.StatusInternalServerError)
		return
	}

	response := APINovaExternalSchedulerResponse{
		Hosts: hosts,
	}
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		logging.Log.Error("failed to encode response", "error", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
}

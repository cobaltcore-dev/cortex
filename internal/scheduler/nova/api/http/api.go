// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	novaScheduler "github.com/cobaltcore-dev/cortex/internal/scheduler/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
)

type HTTPAPI interface {
	// Bind the server handlers.
	Init(*http.ServeMux)
}

type httpAPI struct {
	pipelines map[string]scheduler.Pipeline[api.ExternalSchedulerRequest]
	config    conf.SchedulerAPIConfig
	monitor   scheduler.APIMonitor
}

func NewAPI(config conf.SchedulerConfig, registry *monitoring.Registry, db db.DB, mqttClient mqtt.Client) HTTPAPI {
	monitor := scheduler.NewPipelineMonitor(registry)
	pipelines := make(map[string]scheduler.Pipeline[api.ExternalSchedulerRequest])
	for _, pipelineConf := range config.Nova.Pipelines {
		if _, exists := pipelines[pipelineConf.Name]; exists {
			panic("duplicate nova pipeline name: " + pipelineConf.Name)
		}
		pipelines[pipelineConf.Name] = novaScheduler.NewPipeline(
			pipelineConf, db, monitor.SubPipeline("nova-"+pipelineConf.Name), mqttClient,
		)
	}
	return &httpAPI{
		pipelines: pipelines,
		config:    config.API,
		monitor:   scheduler.NewSchedulerMonitor(registry),
	}
}

// Init the API mux and bind the handlers.
func (httpAPI *httpAPI) Init(mux *http.ServeMux) {
	// Check that we have at least one pipeline with the name "default"
	if _, ok := httpAPI.pipelines["default"]; !ok {
		panic("no default nova pipeline configured")
	}
	mux.HandleFunc("/scheduler/nova/external", httpAPI.NovaExternalScheduler)
}

// Check if the scheduler can run based on the request data.
// Note: messages returned here are user-facing and should not contain internal details.
func (httpAPI *httpAPI) canRunScheduler(requestData api.ExternalSchedulerRequest) (ok bool, reason string) {
	if requestData.Resize {
		return false, "resizing instances is not supported"
	}

	// Check that all hosts have a weight.
	for _, host := range requestData.Hosts {
		if _, ok := requestData.Weights[host.ComputeHost]; !ok {
			return false, "missing weight for host"
		}
	}
	// Check that all weights are assigned to a host in the request.
	computeHostNames := make(map[string]bool)
	for _, host := range requestData.Hosts {
		computeHostNames[host.ComputeHost] = true
	}
	for computeHost := range requestData.Weights {
		if _, ok := computeHostNames[computeHost]; !ok {
			return false, "weight assigned to unknown host"
		}
	}
	// Cortex doesn't support baremetal flavors.
	// See: https://github.com/sapcc/nova/blob/5fcb125/nova/utils.py#L1234
	// And: https://github.com/sapcc/nova/pull/570/files
	extraSpecs := requestData.Spec.Data.Flavor.Data.ExtraSpecs
	if _, ok := extraSpecs["capabilities:cpu_arch"]; ok {
		return false, "baremetal flavors are not supported"
	}
	return true, ""
}

// Handle the POST request from the Nova scheduler.
// The request contains a spec of the VM to be scheduled, a list of hosts and
// their status, and a map of weights that were calculated by the Nova weigher
// pipeline. Some additional flags are also included.
// The response contains an ordered list of hosts that the VM should be scheduled on.
func (httpAPI *httpAPI) NovaExternalScheduler(w http.ResponseWriter, r *http.Request) {
	callback := httpAPI.monitor.Callback(w, r, "/scheduler/nova/external")

	// Exit early if the request method is not POST.
	if r.Method != http.MethodPost {
		internalErr := fmt.Errorf("invalid request method: %s", r.Method)
		callback.Respond(http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Ensure body is closed after reading.
	defer r.Body.Close()

	// If configured, log out the complete request body.
	if httpAPI.config.LogRequestBodies {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			callback.Respond(http.StatusInternalServerError, err, "failed to read request body")
			return
		}
		slog.Info("request body", "body", string(body))
		r.Body = io.NopCloser(bytes.NewBuffer(body)) // Restore the body for further processing
	}

	var requestData api.ExternalSchedulerRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		callback.Respond(http.StatusBadRequest, err, "failed to decode request body")
		return
	}
	slog.Info(
		"handling POST request",
		"url", "/scheduler/nova/external",
		"rebuild", requestData.Rebuild,
		"resize", requestData.Resize,
		"hosts", len(requestData.Hosts),
		"spec", requestData.Spec,
	)

	if ok, reason := httpAPI.canRunScheduler(requestData); !ok {
		internalErr := fmt.Errorf("cannot run scheduler: %s", reason)
		callback.Respond(http.StatusBadRequest, internalErr, reason)
		return
	}

	// Find the requested pipeline.
	var pipelineName string
	if requestData.Pipeline == "" {
		pipelineName = "default"
	} else {
		pipelineName = requestData.Pipeline
	}
	pipeline, ok := httpAPI.pipelines[pipelineName]
	if !ok {
		internalErr := fmt.Errorf("unknown pipeline: %s", pipelineName)
		callback.Respond(http.StatusBadRequest, internalErr, "unknown pipeline")
		return
	}

	// Evaluate the pipeline and return the ordered list of hosts.
	hosts, err := pipeline.Run(requestData)
	if err != nil {
		callback.Respond(http.StatusInternalServerError, err, "failed to evaluate pipeline")
		return
	}
	response := api.ExternalSchedulerResponse{Hosts: hosts}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(response); err != nil {
		callback.Respond(http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	callback.Respond(http.StatusOK, nil, "Success")
}

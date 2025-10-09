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

	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/monitoring"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/cinder"
	cinderScheduler "github.com/cobaltcore-dev/cortex/scheduler/internal/cinder"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/cinder/api"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
)

type HTTPAPI interface {
	// Bind the server handlers.
	Init(*http.ServeMux)
}

type httpAPI struct {
	pipelines map[string]lib.Pipeline[api.PipelineRequest]
	config    conf.SchedulerAPIConfig
	monitor   lib.APIMonitor
}

func NewAPI(config conf.SchedulerConfig, registry *monitoring.Registry, db db.DB, mqttClient mqtt.Client) HTTPAPI {
	monitor := lib.NewPipelineMonitor(registry)
	pipelines := make(map[string]lib.Pipeline[api.PipelineRequest])
	for _, pipelineConf := range config.Cinder.Pipelines {
		if _, exists := pipelines[pipelineConf.Name]; exists {
			panic("duplicate cinder pipeline name: " + pipelineConf.Name)
		}
		pipelines[pipelineConf.Name] = cinderScheduler.NewPipeline(
			pipelineConf, db, monitor.SubPipeline("cinder-"+pipelineConf.Name), mqttClient,
		)
	}
	return &httpAPI{
		pipelines: pipelines,
		config:    config.API,
		monitor:   lib.NewSchedulerMonitor(registry),
	}
}

// Init the API mux and bind the handlers.
func (httpAPI *httpAPI) Init(mux *http.ServeMux) {
	// Check that we have at least one pipeline with the name "default"
	if _, ok := httpAPI.pipelines["default"]; !ok {
		panic("no default cinder pipeline configured")
	}
	mux.HandleFunc("/scheduler/cinder/external", httpAPI.CinderExternalScheduler)
}

// Check if the scheduler can run based on the request data.
// Note: messages returned here are user-facing and should not contain internal details.
func (httpAPI *httpAPI) canRunScheduler(requestData api.PipelineRequest) (ok bool, reason string) {
	// Check that all hosts have a weight.
	for _, host := range requestData.Hosts {
		if _, ok := requestData.Weights[host.VolumeHost]; !ok {
			return false, "missing weight for host"
		}
	}
	// Check that all weights are assigned to a host in the request.
	volumeHostNames := make(map[string]bool)
	for _, host := range requestData.Hosts {
		volumeHostNames[host.VolumeHost] = true
	}
	for volumeHost := range requestData.Weights {
		if _, ok := volumeHostNames[volumeHost]; !ok {
			return false, "weight assigned to unknown host"
		}
	}
	return true, ""
}

// Handle the POST request from the Cinder scheduler.
// The request contains a spec of the volume to be scheduled, a list of hosts,
// and a map of weights that were calculated by the Cinder weigher pipeline.
// The response contains an ordered list of hosts that the volume should be
// scheduled on.
func (httpAPI *httpAPI) CinderExternalScheduler(w http.ResponseWriter, r *http.Request) {
	c := httpAPI.monitor.Callback(w, r, "/scheduler/cinder/external")

	// Exit early if the request method is not POST.
	if r.Method != http.MethodPost {
		internalErr := fmt.Errorf("invalid request method: %s", r.Method)
		c.Respond(http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Ensure body is closed after reading.
	defer r.Body.Close()

	// If configured, log out the complete request body.
	if httpAPI.config.LogRequestBodies {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			c.Respond(http.StatusInternalServerError, err, "failed to read request body")
			return
		}
		slog.Info("request body", "body", string(body))
		r.Body = io.NopCloser(bytes.NewBuffer(body)) // Restore the body for further processing
	}

	var requestData api.PipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		c.Respond(http.StatusBadRequest, err, "failed to decode request body")
		return
	}
	slog.Info(
		"handling POST request", "url", "/scheduler/cinder/external",
		"hosts", len(requestData.Hosts), "spec", requestData.Spec,
	)

	if ok, reason := httpAPI.canRunScheduler(requestData); !ok {
		internalErr := fmt.Errorf("cannot run scheduler: %s", reason)
		c.Respond(http.StatusBadRequest, internalErr, reason)
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
		c.Respond(http.StatusBadRequest, internalErr, "unknown pipeline")
		return
	}

	// Evaluate the pipeline and return the ordered list of hosts.
	hosts, err := pipeline.Run(requestData)
	if err != nil {
		c.Respond(http.StatusInternalServerError, err, "failed to evaluate pipeline")
		return
	}
	response := delegationAPI.ExternalSchedulerResponse{Hosts: hosts}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(response); err != nil {
		c.Respond(http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	c.Respond(http.StatusOK, nil, "Success")
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	api "github.com/cobaltcore-dev/cortex/api/external/cinder"

	scheduling "github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var log = logf.Log.WithName("cinder-scheduler-api")

// Scheduler runs a cinder pipeline for a request and returns the result and a
// human-readable explanation. *FilterWeigherPipelineController satisfies it.
type Scheduler interface {
	Schedule(ctx context.Context, pipelineName string, request api.ExternalSchedulerRequest) (scheduling.PipelineResult, string, error)
}

type HTTPAPI interface {
	// Bind the server handlers.
	Init(*http.ServeMux)
}

type httpAPI struct {
	monitor   scheduling.APIMonitor
	scheduler Scheduler
}

func NewAPI(scheduler Scheduler) HTTPAPI {
	return &httpAPI{
		monitor:   scheduling.NewSchedulerMonitor(),
		scheduler: scheduler,
	}
}

// Init the API mux and bind the handlers.
func (httpAPI *httpAPI) Init(mux *http.ServeMux) {
	metrics.Registry.MustRegister(&httpAPI.monitor)
	mux.HandleFunc("/scheduler/cinder/external", httpAPI.CinderExternalScheduler)
}

// Check if the scheduler can run based on the request data.
// Note: messages returned here are user-facing and should not contain internal details.
func (httpAPI *httpAPI) canRunScheduler(requestData api.ExternalSchedulerRequest) (ok bool, reason string) {
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

// Infer the pipeline name based on the request data.
// Note that the pipelines provided here need to be created in the cluster.
// See also the helm/cortex-cinder bundle.
func (httpAPI *httpAPI) inferPipelineName(_ api.ExternalSchedulerRequest) (string, error) {
	// For now, we just return a default pipeline.
	return "cinder-external-scheduler", nil
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
		c.Respond(logr.Logger{}, http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Ensure body is closed after reading.
	defer r.Body.Close()

	// If configured, log out the complete request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		c.Respond(logr.Logger{}, http.StatusInternalServerError, err, "failed to read request body")
		return
	}
	var requestData api.ExternalSchedulerRequest
	// Copy the raw body to a io.Reader for json deserialization.
	cp := body
	reader := bytes.NewReader(cp)
	if err := json.NewDecoder(reader).Decode(&requestData); err != nil {
		c.Respond(logr.Logger{}, http.StatusBadRequest, err, "failed to decode request body")
		return
	}
	logger := log.WithValues(requestData.GetTraceLogArgs()...)
	logger.Info("handling POST request", "url", "/scheduler/cinder/external", "body", string(body))

	if ok, reason := httpAPI.canRunScheduler(requestData); !ok {
		internalErr := fmt.Errorf("cannot run scheduler: %s", reason)
		c.Respond(logger, http.StatusBadRequest, internalErr, reason)
		return
	}

	// If the pipeline name is not set, set it to a default value.
	if requestData.Pipeline == "" {
		var err error
		requestData.Pipeline, err = httpAPI.inferPipelineName(requestData)
		if err != nil {
			c.Respond(logger, http.StatusBadRequest, err, err.Error())
			return
		}
		logger.V(1).Info("inferred pipeline name", "pipeline", requestData.Pipeline)
	}

	result, _, err := httpAPI.scheduler.Schedule(r.Context(), requestData.Pipeline, requestData)
	if err != nil {
		c.Respond(logger, http.StatusInternalServerError, err, fmt.Sprintf("failed to process scheduling decision: %v", err))
		return
	}
	hosts := result.OrderedHosts
	// Ensure we always return an array, never null. The schema requires hosts
	// to be an array, and a nil slice would serialize to null.
	if hosts == nil {
		hosts = []string{}
	}
	response := api.ExternalSchedulerResponse{Hosts: hosts}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(response); err != nil {
		c.Respond(logger, http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	c.Respond(logger, http.StatusOK, nil, "Success")
}

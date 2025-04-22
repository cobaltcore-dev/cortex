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
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
)

type HTTPAPI interface {
	// Bind the server handlers.
	Init(*http.ServeMux)
}

type httpAPI struct {
	Pipeline api.Pipeline
	config   conf.SchedulerAPIConfig
	monitor  Monitor
}

func NewAPI(config conf.SchedulerAPIConfig, pipeline api.Pipeline, m Monitor) HTTPAPI {
	return &httpAPI{
		Pipeline: pipeline,
		config:   config,
		monitor:  m,
	}
}

// Init the API mux and bind the handlers.
func (httpAPI *httpAPI) Init(mux *http.ServeMux) {
	mux.HandleFunc("/scheduler/nova/external", httpAPI.NovaExternalScheduler)
}

// Check if the scheduler can run based on the request data.
// Note: messages returned here are user-facing and should not contain internal details.
func (httpAPI *httpAPI) canRunScheduler(requestData ExternalSchedulerRequest) (ok bool, reason string) {
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
	return true, ""
}

// Helper to respond to the request with the given code and error.
// Also adds monitoring for the time it took to handle the request.
type httpAPIhelper struct {
	httpAPI *httpAPI
	w       http.ResponseWriter
	r       *http.Request
	pattern string
	t       time.Time
}

func (httpAPI *httpAPI) newHelper(w http.ResponseWriter, r *http.Request, pattern string) httpAPIhelper {
	return httpAPIhelper{httpAPI: httpAPI, w: w, r: r, pattern: pattern, t: time.Now()}
}

// Respond to the request with the given code and error.
// Also log the time it took to handle the request.
func (h httpAPIhelper) respond(code int, err error, text string) {
	if h.httpAPI.monitor.apiRequestsTimer != nil {
		observer := h.httpAPI.monitor.apiRequestsTimer.WithLabelValues(
			h.r.Method,
			h.pattern,
			strconv.Itoa(code),
			text, // Internal error messages should not face the monitor.
		)
		observer.Observe(time.Since(h.t).Seconds())
	}
	if err != nil {
		slog.Error("failed to handle request", "error", err)
		http.Error(h.w, text, code)
		return
	}
	// If there was no error, nothing else to do.
}

// Handle the POST request from the Nova scheduler.
// The request contains a spec of the VM to be scheduled, a list of hosts and
// their status, and a map of weights that were calculated by the Nova weigher
// pipeline. Some additional flags are also included.
// The response contains an ordered list of hosts that the VM should be scheduled on.
func (httpAPI *httpAPI) NovaExternalScheduler(w http.ResponseWriter, r *http.Request) {
	h := httpAPI.newHelper(w, r, "/scheduler/nova/external")

	// Exit early if the request method is not POST.
	if r.Method != http.MethodPost {
		internalErr := fmt.Errorf("invalid request method: %s", r.Method)
		h.respond(http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Ensure body is closed after reading.
	defer r.Body.Close()

	// If configured, log out the complete request body.
	if httpAPI.config.LogRequestBodies {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			h.respond(http.StatusInternalServerError, err, "failed to read request body")
			return
		}
		slog.Info("request body", "body", string(body))
		r.Body = io.NopCloser(bytes.NewBuffer(body)) // Restore the body for further processing
	}

	var requestData ExternalSchedulerRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		h.respond(http.StatusBadRequest, err, "failed to decode request body")
		return
	}
	slog.Info(
		"handling POST request",
		"url", "/scheduler/nova/external", "rebuild", requestData.Rebuild,
		"hosts", len(requestData.Hosts), "spec", requestData.Spec,
	)

	if ok, reason := httpAPI.canRunScheduler(requestData); !ok {
		internalErr := fmt.Errorf("cannot run scheduler: %s", reason)
		h.respond(http.StatusBadRequest, internalErr, reason)
		return
	}

	// Evaluate the pipeline and return the ordered list of hosts.
	hosts, err := httpAPI.Pipeline.Run(&requestData)
	if err != nil {
		h.respond(http.StatusInternalServerError, err, "failed to evaluate pipeline")
		return
	}
	response := ExternalSchedulerResponse{Hosts: hosts}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(response); err != nil {
		h.respond(http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	h.respond(http.StatusOK, nil, "Success")
}

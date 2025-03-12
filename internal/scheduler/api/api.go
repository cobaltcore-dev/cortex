// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/sapcc/go-bits/httpext"
)

type API interface {
	// Init the API mux and bind the handlers.
	Init(context.Context)
}

type api struct {
	Pipeline scheduler.Pipeline
	config   conf.SchedulerAPIConfig
	monitor  Monitor
}

func NewAPI(config conf.SchedulerAPIConfig, pipeline scheduler.Pipeline, m Monitor) API {
	return &api{
		Pipeline: pipeline,
		config:   config,
		monitor:  m,
	}
}

// Init the API mux and bind the handlers.
func (api *api) Init(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/up", api.Up)
	mux.HandleFunc("/scheduler/nova/external", api.NovaExternalScheduler)
	slog.Info("api listening on", "port", api.config.Port)
	addr := fmt.Sprintf(":%d", api.config.Port)
	if err := httpext.ListenAndServeContext(ctx, addr, mux); err != nil {
		panic(err)
	}
}

// Check if the scheduler can run based on the request data.
// Note: messages returned here are user-facing and should not contain internal details.
func (api *api) canRunScheduler(requestData NovaExternalSchedulerRequest) (ok bool, reason string) {
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
type apihelper struct {
	api     *api
	w       http.ResponseWriter
	r       *http.Request
	pattern string
	t       time.Time
}

func (api *api) newHelper(w http.ResponseWriter, r *http.Request, pattern string) apihelper {
	return apihelper{api: api, w: w, r: r, pattern: pattern, t: time.Now()}
}

// Respond to the request with the given code and error.
// Also log the time it took to handle the request.
func (h apihelper) respond(code int, err error, text string) {
	if h.api.monitor.apiRequestsTimer != nil {
		observer := h.api.monitor.apiRequestsTimer.WithLabelValues(
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

// Handle the GET request to check if the API is up.
func (api *api) Up(w http.ResponseWriter, r *http.Request) {
	h := api.newHelper(w, r, "/up")
	h.respond(http.StatusOK, nil, "Success")
}

// Handle the POST request from the Nova scheduler.
// The request contains a spec of the VM to be scheduled, a list of hosts and
// their status, and a map of weights that were calculated by the Nova weigher
// pipeline. Some additional flags are also included.
// The response contains an ordered list of hosts that the VM should be scheduled on.
func (api *api) NovaExternalScheduler(w http.ResponseWriter, r *http.Request) {
	h := api.newHelper(w, r, "/scheduler/nova/external")

	// Exit early if the request method is not POST.
	if r.Method != http.MethodPost {
		internalErr := fmt.Errorf("invalid request method: %s", r.Method)
		h.respond(http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Ensure body is closed after reading.
	defer r.Body.Close()

	// If configured, log out the complete request body.
	if api.config.LogRequestBodies {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			h.respond(http.StatusInternalServerError, err, "failed to read request body")
			return
		}
		slog.Info("request body", "body", string(body))
		r.Body = io.NopCloser(bytes.NewBuffer(body)) // Restore the body for further processing
	}

	var requestData NovaExternalSchedulerRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		h.respond(http.StatusBadRequest, err, "failed to decode request body")
		return
	}
	slog.Info(
		"handling POST request",
		"url", "/scheduler/nova/external", "rebuild", requestData.Rebuild,
		"hosts", len(requestData.Hosts), "spec", requestData.Spec,
	)

	if ok, reason := api.canRunScheduler(requestData); !ok {
		internalErr := fmt.Errorf("cannot run scheduler: %s", reason)
		h.respond(http.StatusBadRequest, internalErr, reason)
		return
	}

	// Evaluate the pipeline and return the ordered list of hosts.
	hosts, err := api.Pipeline.Run(&requestData, requestData.Weights)
	if err != nil {
		h.respond(http.StatusInternalServerError, err, "failed to evaluate pipeline")
		return
	}
	response := NovaExternalSchedulerResponse{Hosts: hosts}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(response); err != nil {
		h.respond(http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	h.respond(http.StatusOK, nil, "Success")
}

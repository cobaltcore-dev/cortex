// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	novaScheduler "github.com/cobaltcore-dev/cortex/internal/scheduler/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
	"github.com/sapcc/go-bits/jobloop"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ctrl "sigs.k8s.io/controller-runtime"
)

type HTTPAPI interface {
	// Bind the server handlers.
	Init(*http.ServeMux)
}

type httpAPI struct {
	pipelines map[string]scheduler.Pipeline[api.ExternalSchedulerRequest]
	config    conf.SchedulerConfig // General API config for all schedulers.
	monitor   scheduler.APIMonitor

	// Database connection to load specific objects during the scheduling process.
	DB db.DB

	// Kubernetes client
	Client client.Client
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

	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		panic(err)
	}
	clientConfig, err := ctrl.GetConfig()
	if err != nil {
		panic(err)
	}
	cl, err := client.New(clientConfig, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}

	return &httpAPI{
		pipelines: pipelines,
		config:    config,
		monitor:   scheduler.NewSchedulerMonitor(registry),
		DB:        db,
		Client:    cl, // TODO
	}
}

// Init the API mux and bind the handlers.
func (httpAPI *httpAPI) Init(mux *http.ServeMux) {
	// Check that we have at least one pipeline with the name "default"
	if _, ok := httpAPI.pipelines["default"]; !ok {
		panic("no default nova pipeline configured")
	}
	mux.HandleFunc("/scheduler/nova/external", httpAPI.NovaExternalScheduler)
	mux.HandleFunc("/scheduler/nova/commitments/change", httpAPI.HandleCommitmentChangeRequest)
	mux.HandleFunc("/scheduler/nova/scheduling-decisions", httpAPI.HandleListSchedulingDecisions)
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
	if httpAPI.config.API.LogRequestBodies {
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

// Limes handles commitments for customers and can accept/reject them.
//
// However, limes doesn't know exactly which commitments can be currently placed
// in the infrastructure, since this depends on scheduling logic. Therefore, limes
// can ask cortex to approve/reject commitment changes via this endpoint.
//
// This handler will check for each commitment change if we have space for it.
// Note that this is only possible for flavor-based (i.e. instance) commitments.
// Other commitment types will be accepted without further checks.
//
// If one commitment is encountered that cannot be placed, the whole request will
// be rejected.
func (httpAPI *httpAPI) HandleCommitmentChangeRequest(w http.ResponseWriter, r *http.Request) {
	callback := httpAPI.monitor.Callback(w, r, "/scheduler/nova/commitment-change")

	// Exit early if the request method is not POST.
	if r.Method != http.MethodPost {
		internalErr := fmt.Errorf("invalid request method: %s", r.Method)
		callback.Respond(http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Ensure body is closed after reading.
	defer r.Body.Close()

	// If configured, log out the complete request body.
	if httpAPI.config.API.LogRequestBodies {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			callback.Respond(http.StatusInternalServerError, err, "failed to read request body")
			return
		}
		slog.Info("request body", "body", string(body))
		r.Body = io.NopCloser(bytes.NewBuffer(body)) // Restore the body for further processing
	}

	// When we check which commitments can be placed, we'll run a pipeline that
	// preselects all hosts and has all filters for the supported hypervisor
	// types enabled. Currently, this is the reservations pipeline.
	pipeline, ok := httpAPI.pipelines["reservations"]
	if !ok {
		slog.Error("no reservations pipeline configured, cannot check commitment change")
		callback.Respond(http.StatusInternalServerError, errors.New("no reservations pipeline configured"), "internal error")
		return
	}

	var requestData liquid.CommitmentChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		callback.Respond(http.StatusBadRequest, err, "failed to decode request body")
		return
	}
	reqLog := slog.With("req", requestData, "url", "/scheduler/nova/commitment-change")
	reqLog.Info("handling POST request")

	response := liquid.CommitmentChangeResponse{}
	w.Header().Set("Content-Type", "application/json")

	// Ensure we don't reject commitments if they don't require confirmation.
	if !requestData.RequiresConfirmation() {
		reqLog.Info("commitment change doesn't require confirmation, accepting")
		response.RejectionReason = "" // Just to make it explicit.
		if err := json.NewEncoder(w).Encode(response); err != nil {
			callback.Respond(http.StatusInternalServerError, err, "failed to encode response")
			return
		}
		callback.Respond(http.StatusOK, nil, "")
		return
	}

	// We'll call this function when we encounter something that requires us to
	// reject the commitment change. If retry is true, limes will retry after a
	// minute, otherwise the rejection is indefinite.
	reject := func(log *slog.Logger, reason string, retry bool) {
		response.RejectionReason = reason
		if !retry {
			response.RetryAt = option.None[time.Time]()
		} else {
			response.RetryAt = option.Some(time.Now().Add(jobloop.DefaultJitter(time.Minute)))
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			callback.Respond(http.StatusInternalServerError, err, "failed to encode response")
			return
		}
		callback.Respond(http.StatusOK, nil, "")
		log.Info("rejected commitment change", "reason", reason)
	}

	// Get all available flavors.
	var flavors []nova.Flavor
	table := nova.Flavor{}.TableName()
	if _, err := httpAPI.DB.Select(&flavors, "SELECT * FROM "+table); err != nil {
		reqLog.Error("failed to load flavors", "err", err)
		return
	}
	if len(flavors) == 0 {
		// Assume sync isn't finished yet and retry in a minute.
		reject(reqLog, "cortex has no flavor information yet, please retry later", true)
		return
	}
	flavorsByName := make(map[string]nova.Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}

	for _, commitmentChangeset := range requestData.ByProject {
		// Find which new flavors would need to be placed from the commitment changeset.
		// Note that there are also commitment conversions from one flavor to another.
		// For conversions we check if there is enough space to place the new reservation,
		// because the old reservation will need to stay until the commitment is updated.
		for resourceName, resourceCommitmentChangeset := range commitmentChangeset.ByResource {
			specificLog := reqLog.With("resourceName", resourceName) // Better traceability in logs.
			resourceName := string(resourceName)
			if !strings.HasPrefix(resourceName, "instances_") {
				// For non-instance commitments we don't know where to place them,
				// therefore we can't check anything and continue searching.
				specificLog.Debug("won't reject non-instance commitment change")
				continue
			}

			// Safely convert uint64 to int to avoid integer overflow.
			totalAfter := resourceCommitmentChangeset.TotalConfirmedAfter
			totalBefore := resourceCommitmentChangeset.TotalConfirmedBefore
			// Check for potential overflow when converting uint64 to int
			const maxInt = int64(^uint64(0) >> 1) // Maximum value for int on this platform
			if totalAfter > uint64(maxInt) || totalBefore > uint64(maxInt) {
				specificLog.Error("commitment values too large for safe conversion",
					"totalAfter", totalAfter, "totalBefore", totalBefore, "maxInt", maxInt)
				reject(specificLog, "commitment values exceed system limits", false)
				return
			}
			nNewVMs := int64(totalAfter) - int64(totalBefore)
			if nNewVMs <= 0 {
				specificLog.Debug("won't reject commitment conversion/shrinkage", "nNewVMs", nNewVMs)
				continue
			}

			flavorName := strings.TrimPrefix(resourceName, "instances_")
			flavor, ok := flavorsByName[flavorName]
			if !ok {
				// The only case in which this is expected to happen is a) when
				// cortex hasn't synced flavors yet (handled above) or b) when
				// the flavor was deleted in nova after the commitment was made.
				// Case b) indicates a disagreement between nova and limes and
				// doesn't fall into our responsibility to handle it.
				msg := "possible inconsistency between nova and limes, flavor not found: " + flavorName
				reject(specificLog, msg, false)
				return
			}
			var flavorExtraSpecs map[string]string
			if err := json.Unmarshal([]byte(flavor.ExtraSpecs), &flavorExtraSpecs); err != nil {
				// The flavor extra specs should always be valid JSON.
				// If they're not, this should be fixed in their nova representation.
				msg := "misconfigured flavor: invalid extra specs for flavor: " + flavorName
				reject(specificLog, msg, false)
				return
			}

			hvType, ok := flavorExtraSpecs["capabilities:hypervisor_type"]
			if !ok {
				// All flavors should have a hypervisor type set. Otherwise, this
				// is an inconsistency that should be fixed in nova.
				msg := "misconfigured flavor: missing capabilities:hypervisor_type for flavor: " + flavorName
				reject(specificLog, msg, false)
				return
			}
			if !slices.Contains(httpAPI.config.Nova.LiquidAPI.Hypervisors, hvType) {
				specificLog.Info("won't reject because hypervisor type is not checked", "hypervisorType", hvType)
				continue
			}
			novaFlavorSpec := api.NovaFlavor{
				// Relevant flavor fields for scheduling decisions.
				FlavorID:    flavor.ID,
				Name:        flavor.Name,
				MemoryMB:    flavor.RAM,
				VCPUs:       flavor.VCPUs,
				RootGB:      flavor.Disk,
				EphemeralGB: flavor.Ephemeral,
				RXTXFactor:  flavor.RxTxFactor,
				IsPublic:    flavor.IsPublic,
				ExtraSpecs:  flavorExtraSpecs,
			}
			projectMeta := commitmentChangeset.ProjectMetadata.
				UnwrapOr(liquid.ProjectMetadata{})
			novaSpec := api.NovaSpec{
				Flavor:           api.NovaObject[api.NovaFlavor]{Data: novaFlavorSpec},
				ProjectID:        projectMeta.UUID,
				AvailabilityZone: string(requestData.AZ),
				NumInstances:     uint64(nNewVMs), // Guaranteed non-negative.
			}
			novaRequestContext := api.NovaRequestContext{
				ProjectID:       projectMeta.UUID,
				ProjectDomainID: projectMeta.Domain.UUID,
			}
			hosts, err := pipeline.Run(api.ExternalSchedulerRequest{
				Spec:    api.NovaObject[api.NovaSpec]{Data: novaSpec},
				Context: novaRequestContext,
				// Act as if we would newly place the vm.
				Rebuild: false, Resize: false, Live: false,
				VMware: hvType == conf.NovaHypervisorTypeVMware,
				// No need to provide hosts/weights, the pipeline will preselect all hosts.
				Hosts: nil, Weights: nil,
			})
			if err != nil {
				// Maybe the DB is down or something happened that is recoverable.
				reject(specificLog, "cortex pipeline failed to execute, please try again", true)
				return
			}
			if len(hosts) == 0 {
				// Reject indefinitely if there is no space.
				reject(specificLog, "no space for this commitment", false)
				return
			}
			specificLog.Info(
				"success - found possible hosts for commitment change",
				"req", requestData, "hosts", hosts,
			)
		}
	}

	// If nothing is found we'll return no rejection reason.
	if err := json.NewEncoder(w).Encode(response); err != nil {
		callback.Respond(http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	callback.Respond(http.StatusOK, nil, "")
}

// List all scheduling decisions.
func (httpAPI *httpAPI) HandleListSchedulingDecisions(w http.ResponseWriter, r *http.Request) {
	callback := httpAPI.monitor.Callback(w, r, "/scheduler/nova/scheduling-decisions")

	// Exit early if the request method is not GET.
	if r.Method != http.MethodGet {
		internalErr := fmt.Errorf("invalid request method: %s", r.Method)
		callback.Respond(http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Check if a specific vm id is requested.
	vmID := r.URL.Query().Get("vm_id")

	// If no specific vm id is requested, list all scheduling decisions.
	if vmID == "" {
		var decisions v1alpha1.SchedulingDecisionList
		if err := httpAPI.Client.List(r.Context(), &decisions); err != nil {
			callback.Respond(http.StatusInternalServerError, err, "failed to list scheduling decisions")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(decisions); err != nil {
			callback.Respond(http.StatusInternalServerError, err, "failed to encode response")
			return
		}
		return
	}

	var decision v1alpha1.SchedulingDecision
	nn := client.ObjectKey{Name: vmID}
	if err := httpAPI.Client.Get(r.Context(), nn, &decision); err != nil {
		if client.IgnoreNotFound(err) != nil {
			callback.Respond(http.StatusInternalServerError, err, "failed to get scheduling decision")
			return
		}
		// Not found
		callback.Respond(http.StatusNotFound, err, "scheduling decision not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(decision); err != nil {
		callback.Respond(http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	callback.Respond(http.StatusOK, nil, "Success")
}

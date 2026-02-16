// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	api "github.com/cobaltcore-dev/cortex/api/external/cinder"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	scheduling "github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type HTTPAPIDelegate interface {
	// Process the decision from the API. Should create and return the updated decision.
	ProcessNewDecisionFromAPI(ctx context.Context, decision *v1alpha1.Decision) error
}

type HTTPAPI interface {
	// Bind the server handlers.
	Init(*http.ServeMux)
}

type httpAPI struct {
	monitor  scheduling.APIMonitor
	delegate HTTPAPIDelegate
}

func NewAPI(delegate HTTPAPIDelegate) HTTPAPI {
	return &httpAPI{
		monitor:  scheduling.NewSchedulerMonitor(),
		delegate: delegate,
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
		c.Respond(http.StatusMethodNotAllowed, internalErr, "invalid request method")
		return
	}

	// Ensure body is closed after reading.
	defer r.Body.Close()

	// If configured, log out the complete request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		c.Respond(http.StatusInternalServerError, err, "failed to read request body")
		return
	}
	raw := runtime.RawExtension{Raw: body}
	var requestData api.ExternalSchedulerRequest
	// Copy the raw body to a io.Reader for json deserialization.
	cp := body
	reader := bytes.NewReader(cp)
	if err := json.NewDecoder(reader).Decode(&requestData); err != nil {
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

	// If the pipeline name is not set, set it to a default value.
	if requestData.Pipeline == "" {
		var err error
		requestData.Pipeline, err = httpAPI.inferPipelineName(requestData)
		if err != nil {
			c.Respond(http.StatusBadRequest, err, err.Error())
			return
		}
		slog.Info("inferred pipeline name", "pipeline", requestData.Pipeline)
	}

	// Create the decision object in kubernetes.
	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cinder-",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainCinder,
			PipelineRef: corev1.ObjectReference{
				Name: requestData.Pipeline,
			},
			ResourceID: "", // TODO
			CinderRaw:  &raw,
		},
	}
	ctx := r.Context()
	if err := httpAPI.delegate.ProcessNewDecisionFromAPI(ctx, decision); err != nil {
		c.Respond(http.StatusInternalServerError, err, "failed to process scheduling decision")
		return
	}
	// Check if the decision contains status conditions indicating an error.
	if meta.IsStatusConditionFalse(decision.Status.Conditions, v1alpha1.DecisionConditionReady) {
		c.Respond(http.StatusInternalServerError, errors.New("decision contains error condition"), "decision failed")
		return
	}
	if decision.Status.Result == nil {
		c.Respond(http.StatusInternalServerError, errors.New("decision didn't produce a result"), "decision failed")
		return
	}
	hosts := decision.Status.Result.OrderedHosts
	response := api.ExternalSchedulerResponse{Hosts: hosts}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(response); err != nil {
		c.Respond(http.StatusInternalServerError, err, "failed to encode response")
		return
	}
	c.Respond(http.StatusOK, nil, "Success")
}

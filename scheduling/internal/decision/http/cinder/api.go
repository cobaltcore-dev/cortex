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

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/cinder"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type HTTPAPI interface {
	// Bind the server handlers.
	Init(*http.ServeMux)
}

type httpAPI struct {
	config  conf.Config
	monitor scheduling.APIMonitor
	client  *dynamic.DynamicClient
}

func NewAPI(config conf.Config) HTTPAPI {
	return &httpAPI{
		config:  config,
		monitor: scheduling.NewSchedulerMonitor(),
	}
}

// Init the API mux and bind the handlers.
func (httpAPI *httpAPI) Init(mux *http.ServeMux) {
	metrics.Registry.MustRegister(&httpAPI.monitor)
	mux.HandleFunc("/scheduler/cinder/external", httpAPI.CinderExternalScheduler)
	restConfig := ctrl.GetConfigOrDie()
	httpAPI.client = dynamic.NewForConfigOrDie(restConfig)
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
	copy := body
	reader := bytes.NewReader(copy)
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

	// Create the decision object in kubernetes.
	unstructuredDecision, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			// TODO: smart naming (initial placement, migration, ...) based on the volume id.
			GenerateName: "cinder-",
		},
		Spec: v1alpha1.DecisionSpec{
			Operator:   httpAPI.config.Operator,
			SourceHost: "", // TODO model out the spec or pass this info from the external scheduler call
			PipelineRef: corev1.ObjectReference{
				Name: requestData.Pipeline,
			},
			ResourceID: "", // TODO
			Type:       v1alpha1.DecisionTypeCinder,
			CinderRaw:  &raw,
		},
	})
	if err != nil {
		c.Respond(http.StatusInternalServerError, err, "failed to convert decision to unstructured")
		return
	}
	resource := v1alpha1.GroupVersion.WithResource("decisions")
	obj, err := httpAPI.client.
		Resource(resource).
		Create(r.Context(), &unstructured.Unstructured{Object: unstructuredDecision}, metav1.CreateOptions{})
	if err != nil {
		c.Respond(http.StatusInternalServerError, err, "failed to create decision resource")
		return
	}

	// Make an informer for the decision resource.
	resultChan := make(chan *v1alpha1.Decision, 1)
	informer := dynamicinformer.
		NewFilteredDynamicSharedInformerFactory(httpAPI.client, 0, metav1.NamespaceAll, nil).
		ForResource(resource).
		Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			unstructuredObj, ok := newObj.(*unstructured.Unstructured)
			if !ok {
				slog.Error("failed to convert to unstructured object")
				return
			}

			updated := &v1alpha1.Decision{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, updated); err != nil {
				slog.Error("failed to convert decision object", "error", err)
				return
			}
			// Only process updates for our specific decision resource
			if updated.Name != obj.GetName() || updated.Spec.Type != v1alpha1.DecisionTypeCinder {
				return
			}
			if updated.Status.Error != "" || updated.Status.Cinder != nil {
				resultChan <- updated
				return
			}
		},
	})
	stopCh := make(chan struct{})
	defer close(stopCh)
	go informer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		c.Respond(http.StatusInternalServerError, fmt.Errorf("failed to sync cache"), "failed to sync cache")
		return
	}

	// Wait for the scheduling decision to be processed and return the result
	select {
	case result := <-resultChan:
		if result.Status.Error != "" || result.Status.Cinder == nil {
			c.Respond(http.StatusInternalServerError, fmt.Errorf(result.Status.Error), "decision failed")
			return
		}
		hosts := (*result.Status.Cinder).StoragePools
		response := delegationAPI.ExternalSchedulerResponse{Hosts: hosts}
		w.Header().Set("Content-Type", "application/json")
		if err = json.NewEncoder(w).Encode(response); err != nil {
			c.Respond(http.StatusInternalServerError, err, "failed to encode response")
			return
		}
		c.Respond(http.StatusOK, nil, "Success")
	case <-r.Context().Done():
		c.Respond(http.StatusRequestTimeout, r.Context().Err(), "request timeout")
		return
	}
}

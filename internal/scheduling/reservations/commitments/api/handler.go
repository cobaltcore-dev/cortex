// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"strings"
	"sync"

	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var apiLog = ctrl.Log.WithName("committed-resource")

// HTTPAPI implements Limes LIQUID commitment validation endpoints.
type HTTPAPI struct {
	client          client.Client
	config          commitments.Config
	usageDB         commitments.UsageDBClient
	monitor         ChangeCommitmentsAPIMonitor
	usageMonitor    ReportUsageAPIMonitor
	capacityMonitor ReportCapacityAPIMonitor
	infoMonitor     InfoAPIMonitor
	// Mutex to serialize change-commitments requests
	changeMutex sync.Mutex
}

func NewAPI(client client.Client) *HTTPAPI {
	return NewAPIWithConfig(client, commitments.DefaultConfig(), nil)
}

// NewAPIWithConfig creates an HTTPAPI with the given config and optional usageDB client.
func NewAPIWithConfig(k8sClient client.Client, config commitments.Config, usageDB commitments.UsageDBClient) *HTTPAPI {
	return &HTTPAPI{
		client:          k8sClient,
		config:          config,
		usageDB:         usageDB,
		monitor:         NewChangeCommitmentsAPIMonitor(),
		usageMonitor:    NewReportUsageAPIMonitor(),
		capacityMonitor: NewReportCapacityAPIMonitor(),
		infoMonitor:     NewInfoAPIMonitor(),
	}
}

func (api *HTTPAPI) Init(mux *http.ServeMux, registry prometheus.Registerer, log logr.Logger) {
	registry.MustRegister(&api.monitor)
	registry.MustRegister(&api.usageMonitor)
	registry.MustRegister(&api.capacityMonitor)
	registry.MustRegister(&api.infoMonitor)
	mux.HandleFunc("/commitments/v1/change-commitments", api.HandleChangeCommitments)
	mux.HandleFunc("/commitments/v1/report-capacity", api.HandleReportCapacity)
	mux.HandleFunc("/commitments/v1/info", api.HandleInfo)
	mux.HandleFunc("/commitments/v1/projects/", api.handleProjectEndpoint) // routes to report-usage or quota

	log.Info("commitments API initialized",
		"changeCommitmentsEnabled", api.config.EnableChangeCommitmentsAPI,
		"reportUsageEnabled", api.config.EnableReportUsageAPI,
		"reportCapacityEnabled", api.config.EnableReportCapacityAPI)
}

// handleProjectEndpoint routes /commitments/v1/projects/:project_id/... requests to the appropriate handler.
func (api *HTTPAPI) handleProjectEndpoint(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/report-usage"):
		api.HandleReportUsage(w, r)
	case strings.HasSuffix(path, "/quota"):
		api.HandleQuota(w, r)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

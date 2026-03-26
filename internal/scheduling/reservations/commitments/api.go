// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UsageNovaClient is a minimal interface for the Nova client needed by the usage API.
// This allows for easy mocking in tests without implementing the full NovaClient interface.
type UsageNovaClient interface {
	ListProjectServers(ctx context.Context, projectID string) ([]nova.ServerDetail, error)
}

// CRMutex serializes CR state changes between the syncer and change-commitments API.
// This ensures that the syncer's Limes state snapshot is applied atomically without
// interference from concurrent change-commitments API calls. The Lock and Unlock
// methods are no-ops if the receiver is nil, allowing safe use when either component
// is disabled.
// TODO: If the syncer and API are moved to separate pods, replace with a K8s
// distributed lock (e.g., Lease-based coordination).
type CRMutex struct {
	mu sync.Mutex
}

// Lock acquires the mutex. No-op if receiver is nil.
func (m *CRMutex) Lock() {
	if m != nil {
		m.mu.Lock()
	}
}

// Unlock releases the mutex. No-op if receiver is nil.
func (m *CRMutex) Unlock() {
	if m != nil {
		m.mu.Unlock()
	}
}

// HTTPAPI implements Limes LIQUID commitment validation endpoints.
type HTTPAPI struct {
	client          client.Client
	config          Config
	novaClient      UsageNovaClient
	monitor         ChangeCommitmentsAPIMonitor
	usageMonitor    ReportUsageAPIMonitor
	capacityMonitor ReportCapacityAPIMonitor
	infoMonitor     InfoAPIMonitor
	// Shared mutex to serialize CR state changes with the syncer
	crMutex *CRMutex
}

func NewAPI(client client.Client) *HTTPAPI {
	return NewAPIWithConfig(client, DefaultConfig(), nil, nil)
}

func NewAPIWithConfig(client client.Client, config Config, novaClient UsageNovaClient, crMutex *CRMutex) *HTTPAPI {
	// If no shared mutex provided, create a local one (for backwards compatibility in tests)
	if crMutex == nil {
		crMutex = &CRMutex{}
	}
	return &HTTPAPI{
		client:          client,
		config:          config,
		novaClient:      novaClient,
		monitor:         NewChangeCommitmentsAPIMonitor(),
		usageMonitor:    NewReportUsageAPIMonitor(),
		capacityMonitor: NewReportCapacityAPIMonitor(),
		infoMonitor:     NewInfoAPIMonitor(),
		crMutex:         crMutex,
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

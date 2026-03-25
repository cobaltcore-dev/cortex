// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"net/http"
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

// HTTPAPI implements Limes LIQUID commitment validation endpoints.
type HTTPAPI struct {
	client          client.Client
	config          Config
	novaClient      UsageNovaClient
	monitor         ChangeCommitmentsAPIMonitor
	usageMonitor    ReportUsageAPIMonitor
	capacityMonitor ReportCapacityAPIMonitor
	infoMonitor     InfoAPIMonitor
	// Mutex to serialize change-commitments requests
	changeMutex sync.Mutex
}

func NewAPI(client client.Client) *HTTPAPI {
	return NewAPIWithConfig(client, DefaultConfig(), nil)
}

func NewAPIWithConfig(client client.Client, config Config, novaClient UsageNovaClient) *HTTPAPI {
	return &HTTPAPI{
		client:          client,
		config:          config,
		novaClient:      novaClient,
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
	mux.HandleFunc("/v1/commitments/change-commitments", api.HandleChangeCommitments)
	mux.HandleFunc("/v1/commitments/report-capacity", api.HandleReportCapacity)
	mux.HandleFunc("/v1/commitments/info", api.HandleInfo)
	mux.HandleFunc("/v1/commitments/projects/", api.HandleReportUsage) // matches /v1/commitments/projects/:project_id/report-usage

	log.Info("commitments API initialized",
		"changeCommitmentsEnabled", api.config.EnableChangeCommitmentsAPI,
		"reportUsageEnabled", api.config.EnableReportUsageAPI,
		"reportCapacityEnabled", api.config.EnableReportCapacityAPI)
}

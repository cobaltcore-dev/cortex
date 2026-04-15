// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/external"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UsageDBClient is the minimal interface for querying VM usage data from Postgres.
type UsageDBClient interface {
	// ListProjectVMs returns all VMs for a project with their flavor data.
	ListProjectVMs(ctx context.Context, projectID string) ([]VMRow, error)
}

// VMRow is the result of a joined server+flavor query from Postgres.
type VMRow struct {
	ID           string
	Name         string
	Status       string
	Created      string
	AZ           string
	Hypervisor   string
	FlavorName   string
	FlavorRAM    uint64
	FlavorVCPUs  uint64
	FlavorDisk   uint64
	FlavorExtras string // JSON string of flavor extra_specs
}

// HTTPAPI implements Limes LIQUID commitment validation endpoints.
type HTTPAPI struct {
	client          client.Client
	config          Config
	usageDB         UsageDBClient
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

// NewAPIWithConfig creates an HTTPAPI. If usageDB is nil and config.DatabaseSecretRef
// is set, a lazy-connecting PostgresReader-backed client is created automatically.
func NewAPIWithConfig(k8sClient client.Client, config Config, usageDB UsageDBClient) *HTTPAPI {
	if usageDB == nil && config.DatabaseSecretRef != nil {
		reader := &external.PostgresReader{
			Client:            k8sClient,
			DatabaseSecretRef: *config.DatabaseSecretRef,
		}
		usageDB = NewDBUsageClient(reader)
	}
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

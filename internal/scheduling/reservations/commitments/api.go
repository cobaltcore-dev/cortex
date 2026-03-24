// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HTTPAPI implements Limes LIQUID commitment validation endpoints.
type HTTPAPI struct {
	client  client.Client
	config  Config
	monitor ChangeCommitmentsAPIMonitor
	// Mutex to serialize change-commitments requests
	changeMutex sync.Mutex
}

func NewAPI(client client.Client) *HTTPAPI {
	return NewAPIWithConfig(client, DefaultConfig())
}

func NewAPIWithConfig(client client.Client, config Config) *HTTPAPI {
	return &HTTPAPI{
		client:  client,
		config:  config,
		monitor: NewChangeCommitmentsAPIMonitor(),
	}
}

func (api *HTTPAPI) Init(mux *http.ServeMux, registry prometheus.Registerer) {
	registry.MustRegister(&api.monitor)
	mux.HandleFunc("/v1/commitments/change-commitments", api.HandleChangeCommitments)
	mux.HandleFunc("/v1/commitments/report-capacity", api.HandleReportCapacity)
	mux.HandleFunc("/v1/commitments/info", api.HandleInfo)
}

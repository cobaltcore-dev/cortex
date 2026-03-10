// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"net/http"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HTTPAPI implements Limes LIQUID commitment validation endpoints.
type HTTPAPI struct {
	client client.Client
	// Mutex to serialize change-commitments requests
	changeMutex sync.Mutex
}

func NewAPI(client client.Client) *HTTPAPI {
	return &HTTPAPI{
		client: client,
	}
}

func (api *HTTPAPI) Init(mux *http.ServeMux) {
	mux.HandleFunc("/v1/change-commitments", api.HandleChangeCommitments)
	mux.HandleFunc("/v1/report-capacity", api.HandleReportCapacity)
	mux.HandleFunc("/v1/info", api.HandleInfo)
}

var commitmentApiLog = ctrl.Log.WithName("commitment_api")

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package visualizer

import (
	"encoding/json"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
)

type API struct {
	DB db.DB
}

func NewAPI(database db.DB) *API {
	return &API{DB: database}
}

// Init registers the visualization endpoints to the mux.
func (api *API) Init(mux *http.ServeMux) {
	mux.HandleFunc("/visualizer/capacity", api.CapacityHandler)
}

func (api *API) CapacityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var hostUtilization []shared.HostUtilization
	tableName := shared.HostUtilization{}.TableName()
	if _, err := api.DB.Select(&hostUtilization, "SELECT * FROM "+tableName); err != nil {
		http.Error(w, "database query failed", http.StatusInternalServerError)
		return
	}

	// TODO

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(hostUtilization); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

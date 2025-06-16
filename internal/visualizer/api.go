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
	var hostUtilization []shared.HostUtilization
	tableName := shared.HostUtilization{}.TableName()
	if _, err := api.DB.Select(&hostUtilization, "SELECT * FROM "+tableName); err != nil {
		http.Error(w, "database query failed", http.StatusInternalServerError)
		return
	}

	// TODO

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hostUtilization)
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"net/http"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

// Index of all supported Prometheus metric syncers, keyed by metric type.
// Each metric registers itself here through a func init() in types.go.
var Index = map[string]func(
	ds v1alpha1.Datasource,
	st store.Store,
	authenticatedHTTP *http.Client,
	prometheusURL string,
	monitor datasources.Monitor,
) typedSyncer{}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestAllocations exercises GET /allocations/{consumer_uuid} as a
// passthrough to upstream placement, confirming the shim forwards the request.
// Upstream returns 200 with an empty allocations object for unknown consumers.
func e2eTestAllocations(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running allocations endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	consumer := "00000000-0000-0000-0000-0000000000e2"
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocations/"+consumer, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "allocations", run: e2eTestAllocations})
}

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

// e2eTestAllocationCandidates exercises GET /allocation_candidates as a
// passthrough to upstream placement, confirming the shim forwards the request
// and returns a 2xx status.
func e2eTestAllocationCandidates(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running allocation_candidates endpoint e2e test")
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
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/allocation_candidates?resources=VCPU:1", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.10")
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
	e2eTests = append(e2eTests, e2eTest{name: "allocation_candidates", run: e2eTestAllocationCandidates})
}

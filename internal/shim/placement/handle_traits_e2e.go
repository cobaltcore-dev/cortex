// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestListTraits is a test that sends a request to the /traits endpoint of
// the placement shim, which should return a 200 OK response with a list of traits.
func e2eTestListTraits(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running traits endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for traits e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for traits e2e test")
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/traits", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for traits endpoint")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.29") // No "X-"!
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to traits endpoint")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "traits endpoint returned an error")
		return err
	}
	dump, err := httputil.DumpResponse(resp, true)
	if err != nil {
		log.Error(err, "failed to dump response for traits endpoint")
		return err
	}
	log.Info("Placement shim traits endpoint response", "dump", string(dump))
	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "traits", run: e2eTestListTraits})
}

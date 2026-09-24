// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceProviderInventories is a passthrough scaffold for the
// /resource_providers/{uuid}/inventories endpoints. Exercising them requires a
// live resource provider; that fixture setup is left for the follow-up that
// rebuilds the endpoint logic. For now the scaffold only authenticates against
// the shim.
func e2eTestResourceProviderInventories(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource_provider_inventories endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	if _, err := makeE2EServiceClient(ctx, config); err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_inventories", run: e2eTestResourceProviderInventories})
}

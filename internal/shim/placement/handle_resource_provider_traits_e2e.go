// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import "context"

func init() {
	e2eTests = append(e2eTests, e2eTest{
		name: "resource_provider_traits",
		run:  func(ctx context.Context) {},
	})
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
)

func TestToSchedulerRequestNumInstancesPassthrough(t *testing.T) {
	cases := []struct {
		name string
		in   uint64
	}{
		{"omitted stays zero (defaulted later by the request)", 0},
		{"explicit value kept", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wire := api.ExternalSchedulerRequest{}
			wire.Spec.Data.NumInstances = c.in
			req := ToSchedulerRequest(wire, "", "p")
			if req.NumInstances != c.in {
				t.Errorf("NumInstances = %d, want %d", req.NumInstances, c.in)
			}
		})
	}
}

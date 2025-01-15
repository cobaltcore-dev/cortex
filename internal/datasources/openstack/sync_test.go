// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/testlib"
)

func TestMain(m *testing.M) {
	testlib.WithMockDB(m, 5)
}

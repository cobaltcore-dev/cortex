// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"

// Type aliases so existing code in this package compiles unchanged.
// New code should import from reservations directly.
type VM = reservations.VM
type VMSource = reservations.VMSource
type DeletedVMInfo = reservations.DeletedVMInfo
type DBVMSource = reservations.DBVMSource

var NewDBVMSource = reservations.NewDBVMSource

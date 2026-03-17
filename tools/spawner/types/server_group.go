// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package types

// Not supported by gophercloud.
type ServerGroup struct {
	ID     string `json:"id"`
	Policy string `json:"policy"`
	Name   string `json:"name"`
}

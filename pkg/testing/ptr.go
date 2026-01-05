// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package testlib

// Init something as a pointer.
func Ptr[T any](v T) *T { return &v }

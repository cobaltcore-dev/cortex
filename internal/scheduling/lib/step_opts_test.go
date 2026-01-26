// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

type MockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o MockOptions) Validate() error {
	return nil
}

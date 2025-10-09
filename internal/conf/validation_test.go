// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import "testing"

func TestValidConf(t *testing.T) {
	content := `
{}
`
	rawConf, err := readRawConfigFromBytes([]byte(content))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}
	conf := newConfigFromMaps[*SharedConfig](rawConf, nil)
	if err := conf.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

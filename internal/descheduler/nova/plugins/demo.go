// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

type DemoStepOpts struct {
	// A name of a virtual machine to de-schedule.
	VMName string `json:"vmName"`
}

type DemoStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	BaseStep[DemoStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *DemoStep) GetName() string {
	return "demo"
}

func (s *DemoStep) Run() ([]string, error) {
	// Get VMs matching the VMName option.
	var ids []string
	q := "SELECT id FROM " + nova.Server{}.TableName()
	q += " WHERE name = :name"
	if _, err := s.DB.Select(&ids, q, map[string]any{
		"name": s.Options.VMName,
	}); err != nil {
		return nil, err
	}
	return ids, nil
}

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

type StepResult struct {
	// The activations calculated by this step.
	Activations map[string]float64

	// Step statistics like:
	//
	//	{
	//	  "max cpu contention": {
	//	     "unit": "cpu contention [%]",
	//	     "hosts": { "share host 1": 10, "share host 2": 10 }
	//	   },
	//     ...
	//	}
	//
	// These statistics are used to display the step's effect on the hosts.
	// For example: max cpu contention: before [ 100%, 50%, 40% ], after [ 40%, 50%, 100% ]
	Statistics map[string]StepStatistics
}

type StepStatistics struct {
	// The unit of the statistic.
	Unit string
	// The hosts and their values.
	Hosts map[string]float64
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

type FilterWeigherPipelineStepResult struct {
	// The activations calculated by this step.
	Activations map[string]float64

	// Step statistics like:
	//
	//	{
	//	  "max cpu contention": {
	//	     "unit": "cpu contention [%]",
	//	     "hosts": { "host 1": 10, "host 2": 10 }
	//	   },
	//	  "noisy projects": {
	//	     "unit": "vms of this project running on host [#]",
	//	     "hosts": { "host 1": 1, "host 2": 0 }
	//	   }
	//	}
	//
	// These statistics are used to display the step's effect on the hosts.
	// For example: max cpu contention: before [ 100%, 50%, 40% ], after [ 40%, 50%, 100% ]
	Statistics map[string]FilterWeigherPipelineStepStatistics
}

type FilterWeigherPipelineStepStatistics struct {
	// The unit of the statistic.
	Unit string
	// The subjects and their values.
	Subjects map[string]float64
}

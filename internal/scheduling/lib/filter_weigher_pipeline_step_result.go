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

	// Named events reported by the step during its run, e.g.
	// "hypervisor_type_undetermined". Each event is counted by the pipeline
	// monitor as cortex_filter_weigher_pipeline_step_events_total, labeled by
	// pipeline, step, event and any additional labels provided by the step.
	// Use this to expose noteworthy step conditions (skipped filtering, missing
	// data, ...) as Prometheus metrics.
	Events []FilterWeigherPipelineStepEvent
}

// FilterWeigherPipelineStepEvent is a named event reported by a scheduling
// pipeline step. Additional labels can be attached to the event to enrich the
// exported Prometheus metric.
type FilterWeigherPipelineStepEvent struct {
	// Name of the event, e.g. "image_properties_hv_type_undetermined".
	Name string
	// Additional labels to attach to the exported metric. Label names must be
	// valid Prometheus label names.
	Labels map[string]string
}

type FilterWeigherPipelineStepStatistics struct {
	// The unit of the statistic.
	Unit string
	// The hosts and their values.
	Hosts map[string]float64
}

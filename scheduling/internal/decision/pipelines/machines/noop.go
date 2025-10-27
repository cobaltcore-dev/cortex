package machines

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	v1alpha1 "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
)

// Just a filter that does nothing and lets all candidates through.
type NoopFilter struct {
	Alias string
}

// Get the name of this step.
// The name is used to identify the step in metrics, config, logs, and more.
// Should be something like: "my_cool_scheduler_step".
func (NoopFilter) GetName() string { return "noop" }

func (f *NoopFilter) Init(db db.DB, opts conf.RawOpts) error {
	return nil
}

// Run this step of the scheduling pipeline.
// Return a map of keys to activation values. Important: keys that are
// not in the map are considered as filtered out.
// Provide a traceLog that contains the global request id and should
// be used to log the step's execution.
func (NoopFilter) Run(traceLog *slog.Logger, request v1alpha1.MachinePipelineRequest) (*lib.StepResult, error) {
	activations := make(map[string]float64, len(request.Pools))
	stats := make(map[string]lib.StepStatistics)
	// Usually you would do some filtering here, or adjust the weights.
	for _, pool := range request.Pools {
		activations[pool.Name] = 1.0
	}
	return &lib.StepResult{Activations: activations, Statistics: stats}, nil
}

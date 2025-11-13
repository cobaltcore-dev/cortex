package machines

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Just a filter that does nothing and lets all candidates through.
type NoopFilter struct {
	Alias string
}

func (f *NoopFilter) Init(ctx context.Context, client client.Client, step v1alpha1.Step) error {
	return nil
}

// Run this step of the scheduling pipeline.
// Return a map of keys to activation values. Important: keys that are
// not in the map are considered as filtered out.
// Provide a traceLog that contains the global request id and should
// be used to log the step's execution.
func (NoopFilter) Run(traceLog *slog.Logger, request ironcore.MachinePipelineRequest) (*lib.StepResult, error) {
	activations := make(map[string]float64, len(request.Pools))
	stats := make(map[string]lib.StepStatistics)
	// Usually you would do some filtering here, or adjust the weights.
	for _, pool := range request.Pools {
		activations[pool.Name] = 1.0
	}
	return &lib.StepResult{Activations: activations, Statistics: stats}, nil
}

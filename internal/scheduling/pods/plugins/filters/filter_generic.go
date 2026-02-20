package filters

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GenericFilterStepOpts struct {
	Knowledge string `json:"knowledge"`
}

func (o GenericFilterStepOpts) Validate() error {
	if o.Knowledge == "" {
		return fmt.Errorf("knowledge name must be provided")
	}
	return nil
}

type GenericFilterStep struct {
	lib.BaseFilter[pods.PodPipelineRequest, GenericFilterStepOpts]
	knowledgeRef types.NamespacedName
}

func (f *GenericFilterStep) Init(ctx context.Context, client client.Client, spec v1alpha1.FilterSpec) error {
	if err := f.BaseFilter.Init(ctx, client, spec); err != nil {
		return err
	}
	knowledgeRef := types.NamespacedName{Name: f.Options.Knowledge}
	if err := f.CheckKnowledges(ctx, knowledgeRef); err != nil {
		return err
	}
	f.knowledgeRef = knowledgeRef

	return nil
}

func (f *GenericFilterStep) Run(ctx context.Context, _ *slog.Logger, req pods.PodPipelineRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	knowledge := v1alpha1.Knowledge{}
	if err := f.Client.Get(ctx, f.knowledgeRef, &knowledge); err != nil {
		return nil, err
	}

	nodeFeatures, err := v1alpha1.
		UnboxFeatureList[plugins.Generic](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}

	result := f.IncludeAllHostsFromRequest(req)
	for _, nodeFeature := range nodeFeatures {
		host := nodeFeature.Host
		if _, exists := result.Activations[host]; !exists {
			continue
		}
		if nodeFeature.Value == 1.0 {
			delete(result.Activations, host)
		}
	}

	return result, nil
}

func init() {
	Index["generic"] = func() PodFilter { return &GenericFilterStep{} }
}

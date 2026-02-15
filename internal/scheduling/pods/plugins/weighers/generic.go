package weighers

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

type GenericWeigherStepOpts struct {
	Knowledge string `json:"knowledge"`
}

func (o GenericWeigherStepOpts) Validate() error {
	if o.Knowledge == "" {
		return fmt.Errorf("knowledge name must be provided")
	}
	return nil
}

type GenericWeigherStep struct {
	lib.BaseWeigher[pods.PodPipelineRequest, GenericWeigherStepOpts]
	knowledgeRef types.NamespacedName
}

func (w *GenericWeigherStep) Init(ctx context.Context, client client.Client, spec v1alpha1.WeigherSpec) error {
	if err := w.BaseWeigher.Init(ctx, client, spec); err != nil {
		return err
	}
	knowledgeRef := types.NamespacedName{Name: w.Options.Knowledge}
	if err := w.CheckKnowledges(ctx, knowledgeRef); err != nil {
		return err
	}
	w.knowledgeRef = knowledgeRef

	return nil
}

func (w *GenericWeigherStep) Run(ctx context.Context, _ *slog.Logger, req pods.PodPipelineRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	knowledge := v1alpha1.Knowledge{}
	if err := w.Client.Get(ctx, w.knowledgeRef, &knowledge); err != nil {
		return nil, err
	}

	nodeFeatures, err := v1alpha1.
		UnboxFeatureList[plugins.Generic](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}

	result := w.IncludeAllHostsFromRequest(req)
	for _, nodeFeature := range nodeFeatures {
		host := nodeFeature.Host
		if _, exists := result.Activations[host]; !exists {
			continue
		}
		result.Activations[host] = nodeFeature.Value
	}

	return result, nil
}

func init() {
	Index["generic"] = func() PodWeigher { return &GenericWeigherStep{} }
}

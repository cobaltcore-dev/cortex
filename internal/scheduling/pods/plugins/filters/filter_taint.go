// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type TaintFilter struct {
	Alias string
}

func (f *TaintFilter) Init(ctx context.Context, client client.Client, step v1alpha1.FilterSpec) error {
	return nil
}

func (f *TaintFilter) Validate(ctx context.Context, params runtime.RawExtension) error {
	return nil
}

func (TaintFilter) Run(traceLog *slog.Logger, request pods.PodPipelineRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	activations := make(map[string]float64)
	stats := make(map[string]lib.FilterWeigherPipelineStepStatistics)

	for _, node := range request.Nodes {
		if canScheduleOnNode(node, request.Pod) {
			activations[node.Name] = 0.0
		}
	}

	return &lib.FilterWeigherPipelineStepResult{Activations: activations, Statistics: stats}, nil
}

func canScheduleOnNode(node corev1.Node, pod corev1.Pod) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule {
			if !hasToleration(pod, taint) {
				return false
			}
		}
	}
	return true
}

func hasToleration(pod corev1.Pod, taint corev1.Taint) bool {
	for _, toleration := range pod.Spec.Tolerations {
		if toleration.Key == taint.Key {
			if toleration.Operator == corev1.TolerationOpExists {
				return true
			}
			if toleration.Operator == corev1.TolerationOpEqual && toleration.Value == taint.Value {
				return true
			}
		}
	}
	return false
}

func init() {
	Index["taint"] = func() PodFilter { return &TaintFilter{} }
}

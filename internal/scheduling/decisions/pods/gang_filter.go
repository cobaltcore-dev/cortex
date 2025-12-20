package pods

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GangFilter ensures that pods belonging to a PodGroup are only scheduled
// if the PodGroup resource exists.
type GangFilter struct {
	client client.Client
}

func (f *GangFilter) Init(ctx context.Context, client client.Client, step v1alpha1.Step) error {
	f.client = client
	return nil
}

func (f *GangFilter) Run(traceLog *slog.Logger, request pods.PodPipelineRequest) (*lib.StepResult, error) {
	activations := make(map[string]float64, len(request.Nodes))
	stats := make(map[string]lib.StepStatistics)

	pod := request.Pod
	if pod == nil {
		traceLog.Warn("gang-filter: pod is nil in request")
		return nil, fmt.Errorf("pod is nil in request")
	}

	// Check for Workload API
	// Fetch the full pod object to inspect new fields if they are not in the struct
	workloadName := ""
	// Note: We cannot access pod.Spec.WorkloadRef directly if the struct is old.
	// Use unstructured to attempt to find it.
	uPod := &unstructured.Unstructured{}
	uPod.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
	if err := f.client.Get(context.Background(), client.ObjectKey{Name: pod.Name, Namespace: pod.Namespace}, uPod); err == nil {
		val, found, _ := unstructured.NestedString(uPod.Object, "spec", "workloadRef", "name")
		if found {
			workloadName = val
		}
	}

	if workloadName != "" {
		traceLog.Info("gang-filter: checking for workload", "workloadName", workloadName)
		workload := &unstructured.Unstructured{}
		workload.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "scheduling.k8s.io",
			Version: "v1alpha1",
			Kind:    "Workload",
		})
		if err := f.client.Get(context.Background(), client.ObjectKey{Name: workloadName, Namespace: pod.Namespace}, workload); err != nil {
			traceLog.Error("gang-filter: failed to fetch workload", "error", err)
			// Deny all nodes if the gang resource is missing or cannot be fetched.
			return &lib.StepResult{Activations: activations, Statistics: stats}, nil
		}
		traceLog.Info("gang-filter: workload found, allowing scheduling")
		for _, node := range request.Nodes {
			activations[node.Name] = 1.0
		}
		return &lib.StepResult{Activations: activations, Statistics: stats}, nil
	}

	// Fallback: Check if the pod belongs to a gang via Label
	// We use the label "pod-group.scheduling.k8s.io/name" which is standard for gang scheduling.
	gangName, ok := pod.Labels["pod-group.scheduling.k8s.io/name"]
	if !ok {
		// Not a gang pod, allow it.
		for _, node := range request.Nodes {
			activations[node.Name] = 1.0
		}
		return &lib.StepResult{Activations: activations, Statistics: stats}, nil
	}

	traceLog.Info("gang-filter: checking for pod group", "gangName", gangName)

	// Fetch the PodGroup.
	// We use Unstructured because the PodGroup CRD might not be compiled into this binary.
	// We assume the group is scheduling.k8s.io
	podGroup := &unstructured.Unstructured{}
	podGroup.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "scheduling.k8s.io",
		Version: "v1alpha1",
		Kind:    "PodGroup",
	})

	err := f.client.Get(context.Background(), client.ObjectKey{
		Name:      gangName,
		Namespace: pod.Namespace,
	}, podGroup)

	if err != nil {
		traceLog.Error("gang-filter: failed to fetch pod group", "error", err)
		// Deny all nodes if the gang resource is missing or cannot be fetched.
		return &lib.StepResult{Activations: activations, Statistics: stats}, nil
	}

	// If we found the PodGroup, we currently allow scheduling.
	// In a full implementation, we would check 'minMember' and other status fields here.
	traceLog.Info("gang-filter: pod group found, allowing scheduling")
	for _, node := range request.Nodes {
		activations[node.Name] = 1.0
	}

	return &lib.StepResult{Activations: activations, Statistics: stats}, nil
}

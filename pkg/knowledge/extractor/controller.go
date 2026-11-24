// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/inputs"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type KnowledgeReconcilerConfig struct {
	// The controller will only touch resources with this scheduling domain.
	SchedulingDomain v1alpha1.SchedulingDomain `json:"schedulingDomain"`
	// Maximum duration of an expression evaluation in seconds (default 60).
	ExpressionTimeoutSeconds int `json:"expressionTimeoutSeconds,omitempty"`
	// Memory budget of an expression evaluation, counted by expr in
	// allocated elements such as list items and map entries (default 1e8).
	ExpressionMemoryBudget uint `json:"expressionMemoryBudget,omitempty"`
}

type KnowledgeReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme
	// Monitor to use for tracking the reconciler.
	Monitor Monitor
	// Config for the reconciler.
	Conf KnowledgeReconcilerConfig

	// Evaluates expression knowledges.
	expressions expressionEvaluator
}

// Error during extraction with the reason to set on the ready condition.
type extractionError struct {
	reason string
	err    error
	// Whether retrying the extraction without spec changes may succeed.
	retry bool
}

func (e *extractionError) Error() string { return e.err.Error() }
func (e *extractionError) Unwrap() error { return e.err }

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *KnowledgeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	knowledge := &v1alpha1.Knowledge{}
	if err := r.Get(ctx, req.NamespacedName, knowledge); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.expressions.forget(req.Name)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Sanity checks.
	lastExtracted := knowledge.Status.LastExtracted.Time
	recency := knowledge.Spec.Recency.Duration
	if lastExtracted.Add(recency).After(time.Now()) {
		waitFor := time.Until(lastExtracted.Add(recency))
		log.V(1).Info("skipping knowledge extraction, not yet time", "name", knowledge.Name, "waitFor", waitFor)
		return ctrl.Result{RequeueAfter: waitFor}, nil
	}

	features, err := r.extract(ctx, knowledge)
	var extractionErr *extractionError
	if errors.As(err, &extractionErr) {
		log.Error(err, "failed to extract knowledge", "name", knowledge.Name, "reason", extractionErr.reason)
		if err := r.setNotReady(ctx, knowledge, extractionErr.reason, err); err != nil {
			return ctrl.Result{}, err
		}
		if extractionErr.retry {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Update the knowledge status.
	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		log.Error(err, "failed to marshal extracted features", "name", knowledge.Name)
		if err := r.setNotReady(ctx, knowledge, "FeatureMarshalingFailed", err); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}
	old := knowledge.DeepCopy()
	meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.KnowledgeConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "KnowledgeExtracted",
		Message: "knowledge extracted successfully",
	})
	contentChanged := featuresChanged(knowledge.Status.Raw.Raw, raw.Raw)
	knowledge.Status.Raw = raw
	knowledge.Status.LastExtracted = metav1.NewTime(time.Now())
	knowledge.Status.RawLength = len(features)
	if contentChanged {
		log.Info("content of knowledge has changed", "name", knowledge.Name)
		knowledge.Status.LastContentChange = metav1.NewTime(time.Now())
	}
	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, knowledge, patch); err != nil {
		log.Error(err, "failed to patch knowledge status")
		return ctrl.Result{}, err
	}
	log.V(1).Info("successfully extracted knowledge", "name", knowledge.Name)
	return ctrl.Result{}, nil
}

// Extract the features of the knowledge with its go extractor or expression.
func (r *KnowledgeReconciler) extract(ctx context.Context, knowledge *v1alpha1.Knowledge) ([]Feature, error) {
	resolver := inputs.Resolver{Client: r.Client}
	spec := knowledge.Spec
	if spec.Expression != nil {
		env, err := resolver.ResolveAll(ctx, spec.Inputs, FeatureTypeOf)
		if err != nil {
			return nil, inputError(err)
		}
		timeout := defaultExpressionTimeout
		if r.Conf.ExpressionTimeoutSeconds > 0 {
			timeout = time.Duration(r.Conf.ExpressionTimeoutSeconds) * time.Second
		}
		memoryBudget := defaultExpressionMemoryBudget
		if r.Conf.ExpressionMemoryBudget > 0 {
			memoryBudget = r.Conf.ExpressionMemoryBudget
		}
		start := time.Now()
		features, err := r.expressions.evaluate(ctx, knowledge.Name, *spec.Expression, env, timeout, memoryBudget)
		switch {
		case errors.Is(err, errUnknownOutputType):
			return nil, &extractionError{reason: "UnknownOutputType", err: err}
		case errors.Is(err, errExpressionCompile):
			return nil, &extractionError{reason: "ExpressionCompilationFailed", err: err}
		case errors.Is(err, errExpressionOutput):
			return nil, &extractionError{reason: "ExpressionOutputInvalid", err: err}
		case err != nil:
			return nil, &extractionError{reason: "ExpressionEvaluationFailed", err: err, retry: true}
		}
		r.Monitor.observe("expression_"+knowledge.Name, time.Since(start), len(features))
		return features, nil
	}
	if spec.Extractor == nil {
		return nil, &extractionError{reason: "MissingExtractor", err: errors.New("neither extractor nor expression is set")}
	}
	newExtractor, ok := Index[spec.Extractor.Name]
	if !ok {
		return nil, &extractionError{
			reason: "UnsupportedExtractor",
			err:    errors.New("unsupported extractor name: " + spec.Extractor.Name),
		}
	}
	extractor := monitorFeatureExtractor(spec.Extractor.Name, newExtractor(), r.Monitor)
	if err := extractor.Init(r.Client, spec); err != nil {
		return nil, &extractionError{reason: "FeatureExtractorInitializationFailed", err: err, retry: true}
	}
	in := extractor.NewInputs()
	if err := resolver.Resolve(ctx, spec.Inputs, in); err != nil {
		return nil, inputError(err)
	}
	features, err := extractor.Extract(ctx, in)
	if err != nil {
		return nil, &extractionError{reason: "FeatureExtractionFailed", err: err, retry: true}
	}
	return features, nil
}

// Map an input resolution error to an extraction error.
func inputError(err error) error {
	switch {
	case errors.Is(err, inputs.ErrMissing):
		return &extractionError{reason: "InputMissing", err: err}
	case errors.Is(err, inputs.ErrUnused):
		return &extractionError{reason: "InputUnused", err: err}
	case errors.Is(err, inputs.ErrTypeMismatch):
		return &extractionError{reason: "InputTypeMismatch", err: err}
	default:
		return &extractionError{reason: "InputFetchFailed", err: err, retry: true}
	}
}

// Set the ready condition of the knowledge to false with the given reason.
func (r *KnowledgeReconciler) setNotReady(ctx context.Context, knowledge *v1alpha1.Knowledge, reason string, err error) error {
	old := knowledge.DeepCopy()
	meta.SetStatusCondition(&knowledge.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.KnowledgeConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
	if err := r.Status().Patch(ctx, knowledge, client.MergeFrom(old)); err != nil {
		logf.FromContext(ctx).Error(err, "failed to patch knowledge status")
		return err
	}
	return nil
}

// Check whether the extracted features changed, ignoring their order and
// json serialization differences such as map key ordering.
func featuresChanged(oldRaw, newRaw []byte) bool {
	canonical := func(raw []byte) ([]string, bool) {
		var boxed struct {
			Features []any `json:"features"`
		}
		if err := json.Unmarshal(raw, &boxed); err != nil {
			return nil, false
		}
		out := make([]string, len(boxed.Features))
		for i, f := range boxed.Features {
			b, err := json.Marshal(f)
			if err != nil {
				return nil, false
			}
			out[i] = string(b)
		}
		slices.Sort(out)
		return out, true
	}
	if len(oldRaw) == 0 {
		return true
	}
	oldFeatures, ok := canonical(oldRaw)
	if !ok {
		return true
	}
	newFeatures, ok := canonical(newRaw)
	if !ok {
		return true
	}
	return !slices.Equal(oldFeatures, newFeatures)
}

func (r *KnowledgeReconciler) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	bldr := multicluster.BuildController(mcl, mgr)
	// Watch knowledge changes across all clusters.
	bldr, err := bldr.WatchesMulticluster(
		&v1alpha1.Knowledge{},
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			// Only react to datasources matching the operator.
			ds := obj.(*v1alpha1.Knowledge)
			return ds.Spec.SchedulingDomain == r.Conf.SchedulingDomain
		}),
	)
	if err != nil {
		return err
	}
	return bldr.Named("cortex-knowledge").
		Complete(r)
}

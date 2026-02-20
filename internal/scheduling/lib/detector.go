// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Detection interface {
	// Get the ID of the detected resource.
	GetResource() string
	// Get the host on which this resource is currently located.
	GetHost() string
	// Get the reason for the detection.
	GetReason() string
	// Set the reason for the detection.
	WithReason(reason string) Detection
}

type Detector[DetectionType Detection] interface {
	// Detect resources such as VMs on their current hosts that should be
	// considered for descheduling.
	Run() ([]DetectionType, error)

	// Initialize the step.
	Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error

	// Validate that the given config is valid for this step. This is used in
	// the pipeline validation to check if the pipeline configuration is valid
	// without actually initializing the step.
	Validate(ctx context.Context, params runtime.RawExtension) error
}

// Common base for all descheduler steps that provides some functionality
// that would otherwise be duplicated across all steps.
type BaseDetector[Opts DetectionStepOpts] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The kubernetes client to use.
	Client client.Client
}

// Init the step.
func (d *BaseDetector[Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	d.Client = client

	opts := conf.NewRawOptsBytes(step.Params.Raw)
	if err := d.Load(opts); err != nil {
		return err
	}
	return nil
}

// Validate that the given config is valid for this step. This is used in
// the pipeline validation to check if the pipeline configuration is valid
// without actually initializing the step.
func (d *BaseDetector[Opts]) Validate(ctx context.Context, params runtime.RawExtension) error {
	opts := conf.NewRawOptsBytes(params.Raw)
	if err := d.Load(opts); err != nil {
		return err
	}
	if err := d.Options.Validate(); err != nil {
		return err
	}
	return nil
}

// Check if all knowledges are ready, and if not, return an error indicating why not.
func (d *BaseDetector[Opts]) CheckKnowledges(ctx context.Context, kns ...corev1.ObjectReference) error {
	if d.Client == nil {
		return errors.New("kubernetes client not initialized")
	}
	for _, objRef := range kns {
		knowledge := &v1alpha1.Knowledge{}
		if err := d.Client.Get(ctx, client.ObjectKey{
			Name:      objRef.Name,
			Namespace: objRef.Namespace,
		}, knowledge); err != nil {
			return fmt.Errorf("failed to get knowledge %s: %w", objRef.Name, err)
		}
		// Check if the knowledge status conditions indicate an error.
		if meta.IsStatusConditionFalse(knowledge.Status.Conditions, v1alpha1.KnowledgeConditionReady) {
			return fmt.Errorf("knowledge %s not ready", objRef.Name)
		}
		if knowledge.Status.RawLength == 0 {
			return fmt.Errorf("knowledge %s not ready, no data available", objRef.Name)
		}
	}
	return nil
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for all descheduler steps that provides some functionality
// that would otherwise be duplicated across all steps.
type Detector[Opts any] struct {
	// Options to pass via yaml to this step.
	conf.JsonOpts[Opts]
	// The kubernetes client to use.
	Client client.Client
}

// Init the step with the database and options.
func (d *Detector[Opts]) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	d.Client = client

	opts := conf.NewRawOptsBytes(step.Params.Raw)
	if err := d.Load(opts); err != nil {
		return err
	}
	return nil
}

// Check if all knowledges are ready, and if not, return an error indicating why not.
func (d *Detector[PipelineType]) CheckKnowledges(ctx context.Context, kns ...corev1.ObjectReference) error {
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

type Decision struct {
	// Get the VM ID for which this decision applies.
	VMID string
	// Get a human-readable reason for this decision.
	Reason string
	// Get the compute host where the vm should be migrated away from.
	Host string
}

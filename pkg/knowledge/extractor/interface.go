// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"reflect"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Extractor is implemented by go feature extractors.
//
// Inputs is a struct whose fields tagged with `input:"<var>"` are filled with
// the objects of the knowledge inputs with that var, e.g.
//
//	type MyInputs struct {
//		Hypervisors []nova.Hypervisor `input:"hypervisors"`
//	}
//
// F is the type of a single extracted feature, which consumers can use to
// unbox the knowledge with v1alpha1.UnboxFeatureList.
type Extractor[Inputs, F any] interface {
	// Init the extractor with a kubernetes client and the knowledge spec.
	Init(client client.Client, spec v1alpha1.KnowledgeSpec) error
	// Extract features from the given inputs.
	Extract(ctx context.Context, in Inputs) ([]F, error)
}

// Feature extracted by an extractor.
type Feature any

// FeatureExtractor is the untyped form of an Extractor, as used by the
// knowledge controller. Use Register to add an Extractor to the Index.
type FeatureExtractor interface {
	// Init the extractor with a kubernetes client and the knowledge spec.
	Init(client client.Client, spec v1alpha1.KnowledgeSpec) error
	// NewInputs returns a pointer to a new inputs struct to be resolved.
	NewInputs() any
	// Extract features from inputs as returned by NewInputs.
	Extract(ctx context.Context, inputs any) ([]Feature, error)
	// FeatureType returns the type of a single extracted feature.
	FeatureType() reflect.Type
}

// Adapter from a typed Extractor to a FeatureExtractor.
type typedExtractor[I, F any] struct {
	extractor Extractor[I, F]
}

func (t typedExtractor[I, F]) Init(client client.Client, spec v1alpha1.KnowledgeSpec) error {
	return t.extractor.Init(client, spec)
}

func (t typedExtractor[I, F]) NewInputs() any { return new(I) }

func (t typedExtractor[I, F]) FeatureType() reflect.Type { return reflect.TypeFor[F]() }

func (t typedExtractor[I, F]) Extract(ctx context.Context, inputs any) ([]Feature, error) {
	in, ok := inputs.(*I)
	if !ok {
		return nil, &inputTypeError{expected: reflect.TypeFor[*I](), got: reflect.TypeOf(inputs)}
	}
	features, err := t.extractor.Extract(ctx, *in)
	if err != nil {
		return nil, err
	}
	out := make([]Feature, len(features))
	for i, f := range features {
		out[i] = f
	}
	return out, nil
}

type inputTypeError struct{ expected, got reflect.Type }

func (e *inputTypeError) Error() string {
	return "expected inputs of type " + e.expected.String() + ", got " + e.got.String()
}

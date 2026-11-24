// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"fmt"
	"reflect"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/inputs"
)

// Index of all supported go feature extractors, keyed by name.
// Each extractor registers itself here through Register in a func init().
var Index = map[string]func() FeatureExtractor{}

// FeatureTypes that expression knowledges can reference as their output,
// keyed by the go type name. The feature types of registered extractors are
// added automatically.
var FeatureTypes = map[string]reflect.Type{}

// Register a go extractor under the given name, together with its feature
// type. Panics on invalid inputs structs and duplicate registrations, so
// mistakes surface at startup.
func Register[I, F any](name string, factory func() Extractor[I, F]) {
	if _, ok := Index[name]; ok {
		panic(fmt.Sprintf("extractor %q registered twice", name))
	}
	inputsType := reflect.TypeFor[I]()
	if inputsType.Kind() != reflect.Struct {
		panic(fmt.Sprintf("extractor %q: inputs must be a struct, got %s", name, inputsType))
	}
	if _, err := inputs.Fields(inputsType); err != nil {
		panic(fmt.Sprintf("extractor %q: %v", name, err))
	}
	Index[name] = func() FeatureExtractor { return typedExtractor[I, F]{extractor: factory()} }
	RegisterFeatureType[F]()
}

// RegisterFeatureType registers F under its go type name, so that expression
// knowledges can use it as their output type.
func RegisterFeatureType[F any]() {
	t := reflect.TypeFor[F]()
	if t.Name() == "" {
		panic(fmt.Sprintf("feature type %s must be a named type", t))
	}
	if existing, ok := FeatureTypes[t.Name()]; ok && existing != t {
		panic(fmt.Sprintf("feature type name %q is used by %s and %s", t.Name(), existing, t))
	}
	FeatureTypes[t.Name()] = t
}

// FeatureTypeOf returns the type of a single feature extracted by the
// knowledge with the given spec, if known.
func FeatureTypeOf(spec v1alpha1.KnowledgeSpec) (reflect.Type, bool) {
	switch {
	case spec.Extractor != nil:
		newExtractor, ok := Index[spec.Extractor.Name]
		if !ok {
			return nil, false
		}
		return newExtractor().FeatureType(), true
	case spec.Expression != nil:
		t, ok := FeatureTypes[spec.Expression.Output]
		return t, ok
	default:
		return nil, false
	}
}

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

// Register a go extractor under the given name. Panics on invalid inputs structs and duplicate registrations, so
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
}

// FeatureTypeOf returns the type of a single feature extracted by the
// knowledge with the given spec. It is only known for go extractors, features
// of expression knowledges are generic json values.
func FeatureTypeOf(spec v1alpha1.KnowledgeSpec) (reflect.Type, bool) {
	if spec.Extractor == nil {
		return nil, false
	}
	newExtractor, ok := Index[spec.Extractor.Name]
	if !ok {
		return nil, false
	}
	return newExtractor().FeatureType(), true
}

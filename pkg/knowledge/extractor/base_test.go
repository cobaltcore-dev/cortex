// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

type mockOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func TestBaseExtractor_Init(t *testing.T) {
	spec := v1alpha1.KnowledgeSpec{
		Extractor: &v1alpha1.KnowledgeExtractorSpec{
			Name:   "mock_extractor",
			Config: runtime.RawExtension{Raw: []byte(`{"option1": "value1", "option2": 2}`)},
		},
	}
	extractor := BaseExtractor[mockOptions]{}
	if err := extractor.Init(nil, spec); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if extractor.Options.Option1 != "value1" || extractor.Options.Option2 != 2 {
		t.Errorf("expected options to be loaded, got %+v", extractor.Options)
	}
}

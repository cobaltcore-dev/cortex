// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockDetection struct {
	resource string
	host     string
	reason   string
}

func (d mockDetection) GetResource() string                { return d.resource }
func (d mockDetection) GetHost() string                    { return d.host }
func (d mockDetection) GetReason() string                  { return d.reason }
func (d mockDetection) WithReason(reason string) Detection { d.reason = reason; return d }

type mockDetectorOptions struct {
	Option1 string `json:"option1"`
	Option2 int    `json:"option2"`
}

func (o mockDetectorOptions) Validate() error {
	return nil
}

func TestDetector_Init(t *testing.T) {
	step := BaseDetector[mockDetectorOptions]{}
	cl := fake.NewClientBuilder().Build()
	err := step.Init(t.Context(), cl, v1alpha1.DetectorSpec{
		Params: runtime.RawExtension{Raw: []byte(`{
			"option1": "value1",
			"option2": 2
		}`)},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if step.Options.Option1 != "value1" {
		t.Errorf("expected Option1 to be 'value1', got %s", step.Options.Option1)
	}

	if step.Options.Option2 != 2 {
		t.Errorf("expected Option2 to be 2, got %d", step.Options.Option2)
	}
}

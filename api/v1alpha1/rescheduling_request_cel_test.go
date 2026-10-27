// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"context"
	"os"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

// crdPath is the generated CRD manifest; the test validates the real
// x-kubernetes-validations that ship, not a hand-built schema.
const crdPath = "../../helm/library/cortex/files/crds/cortex.cloud_reschedulingrequests.yaml"

// loadValidator builds a CEL validator from the generated CRD's root schema.
func loadValidator(t *testing.T) *cel.Validator {
	t.Helper()
	raw, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("read crd: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("unmarshal crd: %v", err)
	}
	if len(crd.Spec.Versions) == 0 || crd.Spec.Versions[0].Schema == nil {
		t.Fatal("crd has no versioned schema")
	}
	internal := &apiextensions.JSONSchemaProps{}
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		crd.Spec.Versions[0].Schema.OpenAPIV3Schema, internal, nil); err != nil {
		t.Fatalf("convert schema: %v", err)
	}
	structural, err := schema.NewStructural(internal)
	if err != nil {
		t.Fatalf("structural: %v", err)
	}
	return cel.NewValidator(structural, true, celconfig.PerCallLimit)
}

// obj builds an unstructured ReschedulingRequest with the given spec/state.
func obj(az, state string, withDrain bool) map[string]any {
	spec := map[string]any{"action": "drain", "availabilityZone": az}
	if withDrain {
		spec["drain"] = map[string]any{}
	}
	return map[string]any{
		"apiVersion": GroupVersion.String(),
		"kind":       "ReschedulingRequest",
		"metadata":   map[string]any{"name": "r"},
		"spec":       spec,
		"status":     map[string]any{"state": state},
	}
}

func TestReschedulingRequestCEL(t *testing.T) {
	v := loadValidator(t)

	tests := []struct {
		name      string
		oldObj    map[string]any
		newObj    map[string]any
		wantValid bool
	}{
		{
			name:      "legal transition Pending->Acknowledged",
			oldObj:    obj("az1", "Pending", true),
			newObj:    obj("az1", "Acknowledged", true),
			wantValid: true,
		},
		{
			name:      "self-transition (conditions-only) allowed",
			oldObj:    obj("az1", "Processing", true),
			newObj:    obj("az1", "Processing", true),
			wantValid: true,
		},
		{
			name:      "Processing->Failed allowed (restart path)",
			oldObj:    obj("az1", "Processing", true),
			newObj:    obj("az1", "Failed", true),
			wantValid: true,
		},
		{
			name:      "illegal transition Succeeded->Processing",
			oldObj:    obj("az1", "Succeeded", true),
			newObj:    obj("az1", "Processing", true),
			wantValid: false,
		},
		{
			name:      "terminal Failed has no outgoing transition",
			oldObj:    obj("az1", "Failed", true),
			newObj:    obj("az1", "Succeeded", true),
			wantValid: false,
		},
		{
			name:      "spec is immutable",
			oldObj:    obj("az1", "Pending", true),
			newObj:    obj("az2", "Pending", true),
			wantValid: false,
		},
		{
			name:      "create requires drain spec for drain action",
			oldObj:    nil, // create: transition rules skip, drain rule applies
			newObj:    obj("az1", "Pending", false),
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var old any
			if tt.oldObj != nil {
				old = tt.oldObj
			}
			errs, _ := v.Validate(context.Background(), field.NewPath("root"), nil,
				tt.newObj, old, celconfig.RuntimeCELCostBudget)
			if tt.wantValid && len(errs) > 0 {
				t.Errorf("expected valid, got errors: %v", errs)
			}
			if !tt.wantValid && len(errs) == 0 {
				t.Error("expected validation errors, got none")
			}
		})
	}
}

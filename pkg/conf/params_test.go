// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
)

func TestUnmarshalParams_NilParameters(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	var result TestStruct
	err := UnmarshalParams(nil, &result)

	if err != nil {
		t.Errorf("expected no error for nil parameters, got %v", err)
	}
	if result.Name != "" {
		t.Errorf("expected empty struct, got Name=%s", result.Name)
	}
}

func TestUnmarshalParams_EmptyParameters(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name,omitempty"`
	}

	params := v1alpha1.Parameters{}
	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("expected no error for empty parameters, got %v", err)
	}
}

func TestUnmarshalParams_StringValue(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr("test-name")},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Name != "test-name" {
		t.Errorf("expected Name='test-name', got '%s'", result.Name)
	}
}

func TestUnmarshalParams_BoolValue(t *testing.T) {
	type TestStruct struct {
		Enabled bool `json:"enabled"`
	}

	params := v1alpha1.Parameters{
		{Key: "enabled", BoolValue: testlib.Ptr(true)},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !result.Enabled {
		t.Errorf("expected Enabled=true, got false")
	}
}

func TestUnmarshalParams_IntValue(t *testing.T) {
	type TestStruct struct {
		Count int64 `json:"count"`
	}

	params := v1alpha1.Parameters{
		{Key: "count", IntValue: testlib.Ptr(int64(42))},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Count != 42 {
		t.Errorf("expected Count=42, got %d", result.Count)
	}
}

func TestUnmarshalParams_FloatValue(t *testing.T) {
	type TestStruct struct {
		Threshold float64 `json:"threshold"`
	}

	params := v1alpha1.Parameters{
		{Key: "threshold", FloatValue: testlib.Ptr(3.14)},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Threshold != 3.14 {
		t.Errorf("expected Threshold=3.14, got %f", result.Threshold)
	}
}

func TestUnmarshalParams_StringListValue(t *testing.T) {
	type TestStruct struct {
		Tags []string `json:"tags"`
	}

	params := v1alpha1.Parameters{
		{Key: "tags", StringListValue: testlib.Ptr([]string{"tag1", "tag2", "tag3"})},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result.Tags) != 3 {
		t.Errorf("expected 3 tags, got %d", len(result.Tags))
	}
	expected := []string{"tag1", "tag2", "tag3"}
	for i, tag := range result.Tags {
		if tag != expected[i] {
			t.Errorf("expected tag[%d]='%s', got '%s'", i, expected[i], tag)
		}
	}
}

func TestUnmarshalParams_MultipleValues(t *testing.T) {
	type TestStruct struct {
		Name      string   `json:"name"`
		Count     int64    `json:"count"`
		Enabled   bool     `json:"enabled"`
		Threshold float64  `json:"threshold"`
		Tags      []string `json:"tags"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr("test")},
		{Key: "count", IntValue: testlib.Ptr(int64(10))},
		{Key: "enabled", BoolValue: testlib.Ptr(true)},
		{Key: "threshold", FloatValue: testlib.Ptr(0.5)},
		{Key: "tags", StringListValue: testlib.Ptr([]string{"a", "b"})},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Name != "test" {
		t.Errorf("expected Name='test', got '%s'", result.Name)
	}
	if result.Count != 10 {
		t.Errorf("expected Count=10, got %d", result.Count)
	}
	if !result.Enabled {
		t.Errorf("expected Enabled=true, got false")
	}
	if result.Threshold != 0.5 {
		t.Errorf("expected Threshold=0.5, got %f", result.Threshold)
	}
	if len(result.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(result.Tags))
	}
}

func TestUnmarshalParams_DuplicateKeys(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr("first")},
		{Key: "name", StringValue: testlib.Ptr("second")},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err == nil {
		t.Error("expected error for duplicate keys, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate parameter key") {
		t.Errorf("expected error about duplicate key, got: %v", err)
	}
}

func TestUnmarshalParams_DuplicateValues(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr("first"), IntValue: testlib.Ptr(int64(1))},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err == nil {
		t.Error("expected error for duplicate keys, got nil")
	}
	if !strings.Contains(err.Error(), "must have exactly one value set") {
		t.Errorf("expected error about multiple values, got: %v", err)
	}
}

func TestUnmarshalParams_ParameterWithNoValue(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "name"}, // No value set
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err == nil {
		t.Error("expected error for parameter with no value, got nil")
	}
	if !strings.Contains(err.Error(), "must have exactly one value set") {
		t.Errorf("expected error about no value set, got: %v", err)
	}
}

func TestUnmarshalParams_UnknownField(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr("test")},
		{Key: "unknown", StringValue: testlib.Ptr("value")},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err == nil {
		t.Error("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode parameters into struct") {
		t.Errorf("expected decode error, got: %v", err)
	}
}

func TestUnmarshalParams_TypeMismatch(t *testing.T) {
	type TestStruct struct {
		Count int64 `json:"count"`
	}

	params := v1alpha1.Parameters{
		{Key: "count", StringValue: testlib.Ptr("not-a-number")},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err == nil {
		t.Error("expected error for type mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode parameters into struct") {
		t.Errorf("expected decode error, got: %v", err)
	}
}

func TestUnmarshalParams_OptionalFieldsMissing(t *testing.T) {
	type TestStruct struct {
		Required string `json:"required"`
		Optional string `json:"optional,omitempty"`
	}

	params := v1alpha1.Parameters{
		{Key: "required", StringValue: testlib.Ptr("value")},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Required != "value" {
		t.Errorf("expected Required='value', got '%s'", result.Required)
	}
	if result.Optional != "" {
		t.Errorf("expected Optional='', got '%s'", result.Optional)
	}
}

func TestUnmarshalParams_NestedStruct(t *testing.T) {
	// Note: UnmarshalParams flattens parameters, so nested structs
	// need to be at the top level in the parameters
	type TestStruct struct {
		Name   string  `json:"name"`
		Weight float64 `json:"weight"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr("nested-test")},
		{Key: "weight", FloatValue: testlib.Ptr(1.5)},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Name != "nested-test" {
		t.Errorf("expected Name='nested-test', got '%s'", result.Name)
	}
	if result.Weight != 1.5 {
		t.Errorf("expected Weight=1.5, got %f", result.Weight)
	}
}

func TestUnmarshalParams_EmptyStringList(t *testing.T) {
	type TestStruct struct {
		Tags []string `json:"tags"`
	}

	params := v1alpha1.Parameters{
		{Key: "tags", StringListValue: testlib.Ptr([]string{})},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result.Tags) != 0 {
		t.Errorf("expected empty tags, got %d", len(result.Tags))
	}
}

func TestUnmarshalParams_ZeroValues(t *testing.T) {
	type TestStruct struct {
		Count     int64   `json:"count"`
		Threshold float64 `json:"threshold"`
		Enabled   bool    `json:"enabled"`
		Name      string  `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "count", IntValue: testlib.Ptr(int64(0))},
		{Key: "threshold", FloatValue: testlib.Ptr(0.0)},
		{Key: "enabled", BoolValue: testlib.Ptr(false)},
		{Key: "name", StringValue: testlib.Ptr("")},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Count != 0 {
		t.Errorf("expected Count=0, got %d", result.Count)
	}
	if result.Threshold != 0.0 {
		t.Errorf("expected Threshold=0.0, got %f", result.Threshold)
	}
	if result.Enabled {
		t.Errorf("expected Enabled=false, got true")
	}
	if result.Name != "" {
		t.Errorf("expected Name='', got '%s'", result.Name)
	}
}

func TestUnmarshalParams_NegativeValues(t *testing.T) {
	type TestStruct struct {
		Count     int64   `json:"count"`
		Threshold float64 `json:"threshold"`
	}

	params := v1alpha1.Parameters{
		{Key: "count", IntValue: testlib.Ptr(int64(-42))},
		{Key: "threshold", FloatValue: testlib.Ptr(-3.14)},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Count != -42 {
		t.Errorf("expected Count=-42, got %d", result.Count)
	}
	if result.Threshold != -3.14 {
		t.Errorf("expected Threshold=-3.14, got %f", result.Threshold)
	}
}

func TestUnmarshalParams_LargeValues(t *testing.T) {
	type TestStruct struct {
		LargeInt   int64   `json:"largeInt"`
		LargeFloat float64 `json:"largeFloat"`
	}

	params := v1alpha1.Parameters{
		{Key: "largeInt", IntValue: testlib.Ptr(int64(9223372036854775807))},  // max int64
		{Key: "largeFloat", FloatValue: testlib.Ptr(1.7976931348623157e+308)}, // approx max float64
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.LargeInt != 9223372036854775807 {
		t.Errorf("expected LargeInt=9223372036854775807, got %d", result.LargeInt)
	}
}

func TestUnmarshalParams_SpecialCharactersInString(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr(`test"with'special\chars`)},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Name != `test"with'special\chars` {
		t.Errorf("expected special characters preserved, got '%s'", result.Name)
	}
}

func TestUnmarshalParams_UnicodeInString(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
	}

	params := v1alpha1.Parameters{
		{Key: "name", StringValue: testlib.Ptr("日本語テスト🚀")},
	}

	var result TestStruct
	err := UnmarshalParams(&params, &result)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Name != "日本語テスト🚀" {
		t.Errorf("expected unicode preserved, got '%s'", result.Name)
	}
}

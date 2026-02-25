// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// Unmarshal a list of parameters into a strongly typed struct.
//
// The struct must have json tags that match the keys of the parameters.
// If the parameters cannot be unmarshaled into the struct, an error is returned.
func UnmarshalParams(p *v1alpha1.Parameters, into any) error {
	if p == nil {
		// This is ok, it just means there are no parameters to unmarshal, so we can
		// return early with no error.
		return nil
	}
	keys := make(map[string]struct{})
	for _, param := range *p {
		// Disallow duplicate keys in the parameters.
		if _, exists := keys[param.Key]; exists {
			return fmt.Errorf("duplicate parameter key: %s", param.Key)
		}
		keys[param.Key] = struct{}{}
	}

	// Check that only one value is set for each parameter.
	for _, param := range *p {
		count := 0
		if param.StringValue != nil {
			count++
		}
		if param.BoolValue != nil {
			count++
		}
		if param.IntValue != nil {
			count++
		}
		if param.FloatValue != nil {
			count++
		}
		if param.StringListValue != nil {
			count++
		}
		if param.FloatMapValue != nil {
			count++
		}
		if count != 1 {
			return fmt.Errorf("parameter %s must have exactly one value set", param.Key)
		}
	}

	paramMap := make(map[string]any)
	for _, param := range *p {
		var value any
		switch {
		case param.StringValue != nil:
			value = *param.StringValue
		case param.BoolValue != nil:
			value = *param.BoolValue
		case param.IntValue != nil:
			value = *param.IntValue
		case param.FloatValue != nil:
			value = *param.FloatValue
		case param.StringListValue != nil:
			value = *param.StringListValue
		case param.FloatMapValue != nil:
			value = *param.FloatMapValue
		} // No value set is handled above.
		paramMap[param.Key] = value
	}

	// This step will also ensure the provided parameters match the expected
	// schema of the struct, and will error if there are unknown fields or
	// type mismatches.
	paramBytes, err := json.Marshal(paramMap)
	if err != nil {
		return fmt.Errorf("failed to marshal parameters: %w", err)
	}
	reader := bytes.NewReader(paramBytes)
	decoder := json.NewDecoder(reader)
	// Disallow unknown fields to catch typos and invalid parameters.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("failed to decode parameters into struct: %w", err)
	}
	return nil
}

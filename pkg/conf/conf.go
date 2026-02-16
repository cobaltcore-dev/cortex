// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"encoding/json"
	"io"
	"os"
)

// Create a new configuration from the default config json file.
//
// This will read two files:
//   - /etc/config/conf.json
//   - /etc/secrets/secrets.json
//
// The values read from secrets.json will override the values in conf.json
func GetConfigOrDie[C any]() C {
	// Note: We need to read the config as a raw map first, to avoid golang
	// unmarshalling default values for the fields.

	// Read the base config from the configmap (not including secrets).
	cmConf, err := readRawConfig("/etc/config/conf.json")
	if err != nil {
		panic(err)
	}
	// Read the secrets config from the kubernetes secret.
	secretConf, err := readRawConfig("/etc/secrets/secrets.json")
	if err != nil {
		panic(err)
	}
	return newConfigFromMaps[C](cmConf, secretConf)
}

func newConfigFromMaps[C any](base, override map[string]any) C {
	// Merge the base config with the override config.
	mergedConf := mergeMaps(base, override)
	// Marshal again, and then unmarshal into the config struct.
	mergedBytes, err := json.Marshal(mergedConf)
	if err != nil {
		panic(err)
	}
	var c C
	if err := json.Unmarshal(mergedBytes, &c); err != nil {
		panic(err)
	}
	return c
}

// Read the json as a map from the given file path.
func readRawConfig(filepath string) (map[string]any, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return readRawConfigFromBytes(bytes)
}

func readRawConfigFromBytes(data []byte) (map[string]any, error) {
	var conf map[string]any
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, err
	}
	return conf, nil
}

// mergeMaps recursively overrides dst with src (in-place)
func mergeMaps(dst, src map[string]any) map[string]any {
	result := dst
	for k, v := range src {
		if v == nil {
			// If src value is nil, skip override
			continue
		}
		if dstVal, ok := dst[k]; ok {
			// If both are maps, merge recursively
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := v.(map[string]any)
			if dstIsMap && srcIsMap {
				result[k] = mergeMaps(dstMap, srcMap)
				continue
			}
		}
		// Otherwise, override
		result[k] = v
	}
	return result
}

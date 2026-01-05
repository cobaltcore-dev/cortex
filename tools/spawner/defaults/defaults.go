// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package defaults

import (
	"encoding/json"
	"os"

	"github.com/sapcc/go-bits/must"
)

type Defaults interface {
	// GetDefault returns the default value for the given key.
	GetDefault(key string) string
	// SetDefault sets the default value for the given key.
	SetDefault(key string, value string)
}

type defaults struct {
	// Where to store the defaults.
	filename string
}

func NewDefaults(filename string) Defaults {
	return &defaults{filename: filename}
}

func (d *defaults) GetDefault(key string) string {
	file, err := os.Open(d.filename)
	if err != nil {
		return ""
	}
	defer file.Close()
	var defaults map[string]string
	must.Succeed(json.NewDecoder(file).Decode(&defaults))
	return defaults[key]
}

func (d *defaults) SetDefault(key, value string) {
	var defaults map[string]string
	file, err := os.Open(d.filename)
	if err != nil {
		defaults = make(map[string]string)
	} else {
		defer file.Close()
		must.Succeed(json.NewDecoder(file).Decode(&defaults))
	}
	defaults[key] = value
	newFile := must.Return(os.Create(d.filename))
	defer newFile.Close()
	must.Succeed(json.NewEncoder(newFile).Encode(defaults))
}

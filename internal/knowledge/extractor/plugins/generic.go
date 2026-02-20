// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	_ "embed"
	"errors"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

type Generic struct {
	Host  string  `db:"host"`
	Value float64 `db:"value"`
}

type GenericExtractor struct {
	BaseExtractor[
		struct{},
		Generic,
	]
}

func (e *GenericExtractor) Extract(d []*v1alpha1.Datasource, _ []*v1alpha1.Knowledge) ([]Feature, error) {
	if len(d) != 1 {
		return nil, errors.New("TODO")
	}
	dsSpec := &d[0].Spec
	if dsSpec.Type != v1alpha1.DatasourceTypePrometheus {
		return nil, errors.New("TODO")
	}
	name := dsSpec.Prometheus.Alias
	if name == "" {
		return nil, errors.New("TODO")
	}

	query := "SELECT host, value FROM generic WHERE name = '" + name + "'"
	return e.ExtractSQL(query)
}

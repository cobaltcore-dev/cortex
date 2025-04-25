// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package monitoring

type MockObserver struct {
	// Observations recorded by the mock observer.
	Observations []float64
}

func (m *MockObserver) Observe(value float64) {
	m.Observations = append(m.Observations, value)
}

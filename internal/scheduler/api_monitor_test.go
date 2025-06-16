// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMonitoredCallback_Respond_RecordsMetric(t *testing.T) {
	reg := monitoring.NewRegistry(newTestMonitoringConfig())
	monitor := NewSchedulerMonitor(reg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
	pattern := "/test"

	cb := MonitoredCallback{
		apiMonitor: &monitor,
		w:          w,
		r:          r,
		pattern:    pattern,
		t:          time.Now().Add(-2 * time.Second), // simulate 2s duration
	}

	cb.Respond(http.StatusOK, nil, "")

	// Check that the metric was recorded
	if count := testutil.CollectAndCount(monitor.ApiRequestsTimer); count == 0 {
		t.Error("ApiRequestsTimer did not record any observations")
	}
	// Check that the metric is present.
	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() == "cortex_scheduler_api_request_duration_seconds" {
			found = true
			break
		}
	}
	if !found {
		t.Error("cortex_scheduler_api_request_duration_seconds not found in gathered metrics")
	}
}

func TestMonitoredCallback_Respond_WithError(t *testing.T) {
	reg := monitoring.NewRegistry(newTestMonitoringConfig())
	monitor := NewSchedulerMonitor(reg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/fail", http.NoBody)
	pattern := "/fail"

	errMsg := "something went wrong"
	cb := MonitoredCallback{
		apiMonitor: &monitor,
		w:          w,
		r:          r,
		pattern:    pattern,
		t:          time.Now(),
	}

	err := errors.New("fail")
	cb.Respond(http.StatusInternalServerError, err, errMsg)

	resp := w.Result()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, resp.StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, errMsg) {
		t.Errorf("expected body to contain error message, got %q", body)
	}
}

func TestAPIMonitor_Callback_ReturnsMonitoredCallback(t *testing.T) {
	reg := monitoring.NewRegistry(newTestMonitoringConfig())
	monitor := NewSchedulerMonitor(reg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cb", http.NoBody)
	pattern := "/cb"

	cb := monitor.Callback(w, r, pattern)
	if cb.w != w || cb.r != r || cb.pattern != pattern {
		t.Error("Callback did not set fields correctly")
	}
	if cb.t.IsZero() {
		t.Error("Callback did not set time")
	}
}

func newTestMonitoringConfig() conf.MonitoringConfig {
	return conf.MonitoringConfig{
		Labels: map[string]string{"test": "true"},
		Port:   0,
	}
}

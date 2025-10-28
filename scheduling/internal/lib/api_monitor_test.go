// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewSchedulerMonitor(t *testing.T) {
	monitor := NewSchedulerMonitor()

	if monitor.ApiRequestsTimer == nil {
		t.Error("expected ApiRequestsTimer to be initialized")
	}

	// Verify the metric is a histogram by recording a value
	observer := monitor.ApiRequestsTimer.WithLabelValues("GET", "/test", "200", "")
	observer.Observe(0.1)
}

func TestAPIMonitor_Describe(t *testing.T) {
	monitor := NewSchedulerMonitor()
	ch := make(chan *prometheus.Desc, 1)

	monitor.Describe(ch)
	close(ch)

	// Should have exactly one descriptor
	count := 0
	for range ch {
		count++
	}

	if count != 1 {
		t.Errorf("expected 1 descriptor, got %d", count)
	}
}

func TestAPIMonitor_Collect(t *testing.T) {
	monitor := NewSchedulerMonitor()

	// Record some metrics first
	observer := monitor.ApiRequestsTimer.WithLabelValues("GET", "/test", "200", "")
	observer.Observe(0.5)

	ch := make(chan prometheus.Metric, 10)
	monitor.Collect(ch)
	close(ch)

	// Should have at least one metric
	count := 0
	for range ch {
		count++
	}

	if count == 0 {
		t.Error("expected at least one metric to be collected")
	}
}

func TestAPIMonitor_Callback(t *testing.T) {
	monitor := NewSchedulerMonitor()
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	pattern := "/test"

	callback := monitor.Callback(w, req, pattern)

	if callback.apiMonitor != &monitor {
		t.Error("expected callback to reference the monitor")
	}
	if callback.w != w {
		t.Error("expected callback to reference the response writer")
	}
	if callback.r != req {
		t.Error("expected callback to reference the request")
	}
	if callback.pattern != pattern {
		t.Error("expected callback to store the pattern")
	}
	if callback.t.IsZero() {
		t.Error("expected callback to record start time")
	}
}

func TestMonitoredCallback_Respond_WithoutError(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		pattern string
		code    int
		text    string
	}{
		{
			name:    "successful GET request",
			method:  "GET",
			pattern: "/api/v1/test",
			code:    200,
			text:    "",
		},
		{
			name:    "POST request with created status",
			method:  "POST",
			pattern: "/api/v1/create",
			code:    201,
			text:    "",
		},
		{
			name:    "client error without actual error",
			method:  "GET",
			pattern: "/api/v1/invalid",
			code:    400,
			text:    "bad request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh registry for each test to avoid conflicts
			registry := prometheus.NewRegistry()

			apiRequestsTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "test_cortex_scheduler_api_request_duration_seconds",
				Help:    "Duration of API requests",
				Buckets: prometheus.DefBuckets,
			}, []string{"method", "path", "status", "error"})

			registry.MustRegister(apiRequestsTimer)

			monitor := APIMonitor{
				ApiRequestsTimer: apiRequestsTimer,
			}

			req := httptest.NewRequest(tt.method, tt.pattern, nil)
			w := httptest.NewRecorder()

			callback := monitor.Callback(w, req, tt.pattern)

			// Add a small delay to ensure measurable duration
			time.Sleep(1 * time.Millisecond)

			callback.Respond(tt.code, nil, tt.text)

			// When there's no error, Respond doesn't set the HTTP status code
			// It only records metrics with the provided code
			if w.Code == tt.code && tt.code != 200 {
				t.Errorf("expected HTTP status code to remain 200 (default), but it was set to %d", w.Code)
			}

			// Verify metrics were recorded by checking the histogram
			metricFamily, err := registry.Gather()
			if err != nil {
				t.Errorf("failed to gather metrics: %v", err)
			}

			found := false
			expectedStatusCode := ""
			switch tt.code {
			case 200:
				expectedStatusCode = "200"
			case 201:
				expectedStatusCode = "201"
			case 400:
				expectedStatusCode = "400"
			default:
				expectedStatusCode = "200" // fallback
			}
			for _, mf := range metricFamily {
				if *mf.Name == "test_cortex_scheduler_api_request_duration_seconds" {
					for _, m := range mf.Metric {
						labelMatch := true
						for _, labelPair := range m.Label {
							switch *labelPair.Name {
							case "method":
								if *labelPair.Value != tt.method {
									labelMatch = false
								}
							case "path":
								if *labelPair.Value != tt.pattern {
									labelMatch = false
								}
							case "status":
								if *labelPair.Value != expectedStatusCode {
									labelMatch = false
								}
							case "error":
								if *labelPair.Value != tt.text {
									labelMatch = false
								}
							}
						}
						if labelMatch && m.Histogram != nil && *m.Histogram.SampleCount == 1 {
							found = true
							break
						}
					}
				}
			}
			if !found {
				t.Error("expected metric with correct labels to be recorded")
			}
		})
	}
}

func TestMonitoredCallback_Respond_WithError(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		pattern      string
		code         int
		err          error
		text         string
		expectedBody string
	}{
		{
			name:         "internal server error",
			method:       "GET",
			pattern:      "/api/v1/error",
			code:         500,
			err:          errors.New("database connection failed"),
			text:         "Internal Server Error",
			expectedBody: "Internal Server Error\n",
		},
		{
			name:         "not found error",
			method:       "GET",
			pattern:      "/api/v1/notfound",
			code:         404,
			err:          errors.New("resource not found"),
			text:         "Not Found",
			expectedBody: "Not Found\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh registry for each test
			registry := prometheus.NewRegistry()

			apiRequestsTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "test_cortex_scheduler_api_request_duration_seconds",
				Help:    "Duration of API requests",
				Buckets: prometheus.DefBuckets,
			}, []string{"method", "path", "status", "error"})

			registry.MustRegister(apiRequestsTimer)

			monitor := APIMonitor{
				ApiRequestsTimer: apiRequestsTimer,
			}

			req := httptest.NewRequest(tt.method, tt.pattern, nil)
			w := httptest.NewRecorder()

			callback := monitor.Callback(w, req, tt.pattern)

			// Add a small delay to ensure measurable duration
			time.Sleep(1 * time.Millisecond)

			callback.Respond(tt.code, tt.err, tt.text)

			// Verify HTTP response
			if w.Code != tt.code {
				t.Errorf("expected status code %d, got %d", tt.code, w.Code)
			}

			if w.Body.String() != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, w.Body.String())
			}

			// Verify metrics were recorded by checking the histogram
			metricFamily, err := registry.Gather()
			if err != nil {
				t.Errorf("failed to gather metrics: %v", err)
			}

			found := false
			expectedStatusCode := ""
			switch tt.code {
			case 404:
				expectedStatusCode = "404"
			case 500:
				expectedStatusCode = "500"
			default:
				expectedStatusCode = "500" // fallback
			}
			for _, mf := range metricFamily {
				if *mf.Name == "test_cortex_scheduler_api_request_duration_seconds" {
					for _, m := range mf.Metric {
						labelMatch := true
						for _, labelPair := range m.Label {
							switch *labelPair.Name {
							case "method":
								if *labelPair.Value != tt.method {
									labelMatch = false
								}
							case "path":
								if *labelPair.Value != tt.pattern {
									labelMatch = false
								}
							case "status":
								if *labelPair.Value != expectedStatusCode {
									labelMatch = false
								}
							case "error":
								if *labelPair.Value != tt.text {
									labelMatch = false
								}
							}
						}
						if labelMatch && m.Histogram != nil && *m.Histogram.SampleCount == 1 {
							found = true
							break
						}
					}
				}
			}
			if !found {
				t.Error("expected metric with correct labels to be recorded")
			}
		})
	}
}

func TestMonitoredCallback_Respond_NilMonitor(t *testing.T) {
	// Test with nil monitor to ensure no panic
	callback := MonitoredCallback{
		apiMonitor: nil,
		w:          httptest.NewRecorder(),
		r:          httptest.NewRequest("GET", "/test", nil),
		pattern:    "/test",
		t:          time.Now(),
	}

	// Should not panic even with nil monitor
	callback.Respond(200, nil, "")

	// Test with nil ApiRequestsTimer
	monitor := &APIMonitor{ApiRequestsTimer: nil}
	callback.apiMonitor = monitor

	// Should not panic even with nil timer
	callback.Respond(200, nil, "")
}

func TestMonitoredCallback_TimeMeasurement(t *testing.T) {
	registry := prometheus.NewRegistry()

	apiRequestsTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_cortex_scheduler_api_request_duration_seconds",
		Help:    "Duration of API requests",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status", "error"})

	registry.MustRegister(apiRequestsTimer)

	monitor := APIMonitor{
		ApiRequestsTimer: apiRequestsTimer,
	}

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	callback := monitor.Callback(w, req, "/test")

	// Sleep for a measurable duration
	sleepDuration := 10 * time.Millisecond
	time.Sleep(sleepDuration)

	callback.Respond(200, nil, "")

	// Verify the metric was recorded by gathering metrics
	metricFamily, err := registry.Gather()
	if err != nil {
		t.Errorf("failed to gather metrics: %v", err)
	}

	found := false
	for _, mf := range metricFamily {
		if *mf.Name == "test_cortex_scheduler_api_request_duration_seconds" {
			for _, m := range mf.Metric {
				if m.Histogram != nil && *m.Histogram.SampleCount == 1 {
					// Verify that some time was measured (should be > sleep duration)
					if *m.Histogram.SampleSum >= sleepDuration.Seconds()/2 {
						found = true
						break
					}
				}
			}
		}
	}
	if !found {
		t.Error("expected time measurement to be recorded with reasonable duration")
	}
}

func TestMonitoredCallback_HTTPMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			registry := prometheus.NewRegistry()

			apiRequestsTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "test_cortex_scheduler_api_request_duration_seconds",
				Help:    "Duration of API requests",
				Buckets: prometheus.DefBuckets,
			}, []string{"method", "path", "status", "error"})

			registry.MustRegister(apiRequestsTimer)

			monitor := APIMonitor{
				ApiRequestsTimer: apiRequestsTimer,
			}

			req := httptest.NewRequest(method, "/test", nil)
			w := httptest.NewRecorder()

			callback := monitor.Callback(w, req, "/test")
			callback.Respond(200, nil, "")

			// Verify the method label is recorded correctly
			metricFamily, err := registry.Gather()
			if err != nil {
				t.Errorf("failed to gather metrics: %v", err)
			}

			found := false
			for _, mf := range metricFamily {
				if *mf.Name == "test_cortex_scheduler_api_request_duration_seconds" {
					for _, m := range mf.Metric {
						for _, labelPair := range m.Label {
							if *labelPair.Name == "method" && *labelPair.Value == method {
								if m.Histogram != nil && *m.Histogram.SampleCount == 1 {
									found = true
									break
								}
							}
						}
						if found {
							break
						}
					}
				}
				if found {
					break
				}
			}
			if !found {
				t.Errorf("expected metric with method %s to be recorded", method)
			}
		})
	}
}

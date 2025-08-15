// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/cinder/api"
)

type mockPipeline struct {
	runFunc func(api.ExternalSchedulerRequest) ([]string, error)
}

func (m *mockPipeline) Run(req api.ExternalSchedulerRequest) ([]string, error) {
	return m.runFunc(req)
}

func validRequestBody() []byte {
	req := api.ExternalSchedulerRequest{
		Spec: map[string]any{"foo": "bar"},
		Hosts: []api.ExternalSchedulerHost{
			{VolumeHost: "host1"},
			{VolumeHost: "host2"},
		},
		Weights: map[string]float64{
			"host1": 1.0,
			"host2": 2.0,
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		panic("failed to marshal valid request body: " + err.Error())
	}
	return b
}

func missingWeightBody() []byte {
	req := api.ExternalSchedulerRequest{
		Spec: map[string]any{"foo": "bar"},
		Hosts: []api.ExternalSchedulerHost{
			{VolumeHost: "host1"},
			{VolumeHost: "host2"},
		},
		Weights: map[string]float64{
			"host1": 1.0,
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		panic("failed to marshal valid request body: " + err.Error())
	}
	return b
}

func unknownWeightBody() []byte {
	req := api.ExternalSchedulerRequest{
		Spec: map[string]any{"foo": "bar"},
		Hosts: []api.ExternalSchedulerHost{
			{VolumeHost: "host1"},
		},
		Weights: map[string]float64{
			"host1": 1.0,
			"host2": 2.0,
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		panic("failed to marshal valid request body: " + err.Error())
	}
	return b
}

func TestCinderExternalScheduler_Success(t *testing.T) {
	pipeline := &mockPipeline{
		runFunc: func(req api.ExternalSchedulerRequest) ([]string, error) {
			return []string{"host2", "host1"}, nil
		},
	}
	a := &httpAPI{
		Pipeline: pipeline,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}

	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", bytes.NewReader(validRequestBody()))
	w := httptest.NewRecorder()

	a.CinderExternalScheduler(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	var out api.ExternalSchedulerResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(out.Hosts) != 2 || out.Hosts[0] != "host2" {
		t.Errorf("unexpected hosts order: %+v", out.Hosts)
	}
}

func TestCinderExternalScheduler_InvalidMethod(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodGet, "/scheduler/cinder/external", http.NoBody)
	w := httptest.NewRecorder()
	api.CinderExternalScheduler(w, req)
}

func TestCinderExternalScheduler_InvalidJSON(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", bytes.NewReader([]byte(`{invalid json}`)))
	w := httptest.NewRecorder()
	api.CinderExternalScheduler(w, req)
}

func TestCinderExternalScheduler_MissingWeight(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", bytes.NewReader(missingWeightBody()))
	w := httptest.NewRecorder()
	api.CinderExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing weight, got %d", resp.StatusCode)
	}
}

func TestCinderExternalScheduler_UnknownWeight(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", bytes.NewReader(unknownWeightBody()))
	w := httptest.NewRecorder()
	api.CinderExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for unknown weight, got %d", resp.StatusCode)
	}
}

func TestCinderExternalScheduler_PipelineError(t *testing.T) {
	pipeline := &mockPipeline{
		runFunc: func(req api.ExternalSchedulerRequest) ([]string, error) {
			return nil, errors.New("pipeline error")
		},
	}
	api := &httpAPI{
		Pipeline: pipeline,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", bytes.NewReader(validRequestBody()))
	w := httptest.NewRecorder()
	api.CinderExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500 for pipeline error, got %d", resp.StatusCode)
	}
}

func TestCinderExternalScheduler_BodyReadError(t *testing.T) {
	api := &httpAPI{
		Pipeline: &mockPipeline{
			runFunc: func(req api.ExternalSchedulerRequest) ([]string, error) {
				return nil, nil // No need to run pipeline for this test
			},
		},
		config:  conf.SchedulerAPIConfig{LogRequestBodies: true},
		monitor: scheduler.APIMonitor{},
	}
	// Simulate a body that returns error on Read
	r := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", io.NopCloser(badReader{}))
	w := httptest.NewRecorder()
	api.CinderExternalScheduler(w, r)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500 for body read error, got %d", resp.StatusCode)
	}
}

func TestCinderExternalScheduler_EmptyBody(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", http.NoBody)
	w := httptest.NewRecorder()
	api.CinderExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", resp.StatusCode)
	}
}

type badReader struct{}

func (badReader) Read([]byte) (int, error) { return 0, errors.New("read error") }
func (badReader) Close() error             { return nil }

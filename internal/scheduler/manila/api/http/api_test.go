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
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api"
)

type mockPipeline struct {
	runFunc func(api.Request) ([]string, error)
}

func (m *mockPipeline) Run(req api.Request) ([]string, error) {
	return m.runFunc(req)
}

func validRequestBody() []byte {
	req := ExternalSchedulerRequest{
		Spec: map[string]any{"foo": "bar"},
		Hosts: []ExternalSchedulerHost{
			{ShareHost: "host1"},
			{ShareHost: "host2"},
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
	req := ExternalSchedulerRequest{
		Spec: map[string]any{"foo": "bar"},
		Hosts: []ExternalSchedulerHost{
			{ShareHost: "host1"},
			{ShareHost: "host2"},
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
	req := ExternalSchedulerRequest{
		Spec: map[string]any{"foo": "bar"},
		Hosts: []ExternalSchedulerHost{
			{ShareHost: "host1"},
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

func TestManilaExternalScheduler_Success(t *testing.T) {
	pipeline := &mockPipeline{
		runFunc: func(req api.Request) ([]string, error) {
			return []string{"host2", "host1"}, nil
		},
	}
	api := &httpAPI{
		Pipeline: pipeline,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}

	req := httptest.NewRequest(http.MethodPost, "/scheduler/manila/external", bytes.NewReader(validRequestBody()))
	w := httptest.NewRecorder()

	api.ManilaExternalScheduler(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	var out ExternalSchedulerResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(out.Hosts) != 2 || out.Hosts[0] != "host2" {
		t.Errorf("unexpected hosts order: %+v", out.Hosts)
	}
}

func TestManilaExternalScheduler_InvalidMethod(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodGet, "/scheduler/manila/external", http.NoBody)
	w := httptest.NewRecorder()
	api.ManilaExternalScheduler(w, req)
}

func TestManilaExternalScheduler_InvalidJSON(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/manila/external", bytes.NewReader([]byte(`{invalid json}`)))
	w := httptest.NewRecorder()
	api.ManilaExternalScheduler(w, req)
}

func TestManilaExternalScheduler_MissingWeight(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/manila/external", bytes.NewReader(missingWeightBody()))
	w := httptest.NewRecorder()
	api.ManilaExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing weight, got %d", resp.StatusCode)
	}
}

func TestManilaExternalScheduler_UnknownWeight(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/manila/external", bytes.NewReader(unknownWeightBody()))
	w := httptest.NewRecorder()
	api.ManilaExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for unknown weight, got %d", resp.StatusCode)
	}
}

func TestManilaExternalScheduler_PipelineError(t *testing.T) {
	pipeline := &mockPipeline{
		runFunc: func(req api.Request) ([]string, error) {
			return nil, errors.New("pipeline error")
		},
	}
	api := &httpAPI{
		Pipeline: pipeline,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/manila/external", bytes.NewReader(validRequestBody()))
	w := httptest.NewRecorder()
	api.ManilaExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500 for pipeline error, got %d", resp.StatusCode)
	}
}

func TestManilaExternalScheduler_BodyReadError(t *testing.T) {
	api := &httpAPI{
		Pipeline: &mockPipeline{
			runFunc: func(req api.Request) ([]string, error) {
				return nil, nil // No need to run pipeline for this test
			},
		},
		config:  conf.SchedulerAPIConfig{LogRequestBodies: true},
		monitor: scheduler.APIMonitor{},
	}
	// Simulate a body that returns error on Read
	r := httptest.NewRequest(http.MethodPost, "/scheduler/manila/external", io.NopCloser(badReader{}))
	w := httptest.NewRecorder()
	api.ManilaExternalScheduler(w, r)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500 for body read error, got %d", resp.StatusCode)
	}
}

func TestManilaExternalScheduler_EmptyBody(t *testing.T) {
	api := &httpAPI{
		Pipeline: nil,
		config:   conf.SchedulerAPIConfig{},
		monitor:  scheduler.APIMonitor{},
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/manila/external", http.NoBody)
	w := httptest.NewRecorder()
	api.ManilaExternalScheduler(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", resp.StatusCode)
	}
}

type badReader struct{}

func (badReader) Read([]byte) (int, error) { return 0, errors.New("read error") }
func (badReader) Close() error             { return nil }

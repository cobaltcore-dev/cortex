// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShimReader(t *testing.T) {
	data := "hello, world"
	sr := &shimReader{r: strings.NewReader(data)}
	got, err := io.ReadAll(sr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != data {
		t.Errorf("read = %q, want %q", string(got), data)
	}
	if sr.n != int64(len(data)) {
		t.Errorf("byte count = %d, want %d", sr.n, len(data))
	}
}

func TestShimReaderEmpty(t *testing.T) {
	sr := &shimReader{r: strings.NewReader("")}
	got, err := io.ReadAll(sr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("read = %q, want empty", string(got))
	}
	if sr.n != 0 {
		t.Errorf("byte count = %d, want 0", sr.n)
	}
}

func TestShimWriter(t *testing.T) {
	tests := []struct {
		name  string
		limit int64
		input string
		want  string
	}{
		{"within limit", 100, "hello", "hello"},
		{"exact limit", 5, "hello", "hello"},
		{"exceeds limit", 3, "hello", "hel"},
		{"zero limit", 0, "hello", ""},
		{"multi write within limit", 10, "hello", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			sw := &shimWriter{w: &buf, n: tt.limit}
			_, err := sw.Write([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if buf.String() != tt.want {
				t.Errorf("buffer = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

func TestShimWriterMultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	sw := &shimWriter{w: &buf, n: 5}
	if _, err := sw.Write([]byte("abc")); err != nil { // 3 bytes, 2 remaining
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := sw.Write([]byte("defgh")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "abcde" {
		t.Errorf("buffer = %q, want %q", buf.String(), "abcde")
	}
}

func TestShimResponseWriterStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &shimResponseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
	sw.WriteHeader(http.StatusNotFound)
	if sw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want %d", sw.statusCode, http.StatusNotFound)
	}
}

func TestShimResponseWriterDefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &shimResponseWriter{ResponseWriter: rec}
	if _, err := sw.Write([]byte("body")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First Write without WriteHeader should default to 200.
	if sw.statusCode != http.StatusOK {
		t.Errorf("statusCode = %d, want %d", sw.statusCode, http.StatusOK)
	}
}

func TestShimResponseWriterBytesWritten(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &shimResponseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
	if _, err := sw.Write([]byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := sw.Write([]byte(" world")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sw.bytesWritten != 11 {
		t.Errorf("bytesWritten = %d, want 11", sw.bytesWritten)
	}
}

func TestShimResponseWriterBodyCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &shimResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
		bodyBuf:        &bytes.Buffer{},
		bodyLimit:      8,
	}
	if _, err := sw.Write([]byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := sw.Write([]byte(" world!")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// bodyBuf should contain at most 8 bytes.
	if sw.bodyBuf.String() != "hello wo" {
		t.Errorf("bodyBuf = %q, want %q", sw.bodyBuf.String(), "hello wo")
	}
	// But bytesWritten counts everything.
	if sw.bytesWritten != 12 {
		t.Errorf("bytesWritten = %d, want 12", sw.bytesWritten)
	}
}

func TestShimResponseWriterNilBodyBuf(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &shimResponseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
	// Should not panic when bodyBuf is nil.
	if _, err := sw.Write([]byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sw.bytesWritten != 5 {
		t.Errorf("bytesWritten = %d, want 5", sw.bytesWritten)
	}
}

func TestWrapHandlerLogsAndMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer upstream.Close()

	down, up := newTestTimers()
	s := &Shim{
		config:                 config{PlacementURL: upstream.URL},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}

	wrapped := s.wrapHandler("/test", func(w http.ResponseWriter, r *http.Request) {
		s.forward(w, r)
	})

	req := httptest.NewRequest(http.MethodGet, "/test?foo=bar", http.NoBody)
	req.Header.Set("X-OpenStack-Request-Id", "req-test-123")
	w := httptest.NewRecorder()
	wrapped(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	// Downstream metric should have one observation.
	if n := histSampleCount(t, down, "GET", "/test", "200"); n != 1 {
		t.Errorf("downstream observation count = %d, want 1", n)
	}
}

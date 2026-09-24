// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequiredPathParam(t *testing.T) {
	t.Run("valid param", func(t *testing.T) {
		mux := http.NewServeMux()
		var gotValue string
		var gotOK bool
		mux.HandleFunc("GET /test/{name}", func(w http.ResponseWriter, r *http.Request) {
			gotValue, gotOK = requiredPathParam(w, r, "name")
		})
		req := httptest.NewRequest(http.MethodGet, "/test/VCPU", http.NoBody)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if !gotOK {
			t.Fatal("expected ok = true")
		}
		if gotValue != "VCPU" {
			t.Fatalf("value = %q, want %q", gotValue, "VCPU")
		}
	})
	t.Run("wrong param name returns empty", func(t *testing.T) {
		mux := http.NewServeMux()
		var gotOK bool
		mux.HandleFunc("GET /test/{name}", func(w http.ResponseWriter, r *http.Request) {
			_, gotOK = requiredPathParam(w, r, "nonexistent")
		})
		req := httptest.NewRequest(http.MethodGet, "/test/VCPU", http.NoBody)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if gotOK {
			t.Fatal("expected ok = false for wrong param name")
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestRequiredUUIDPathParam(t *testing.T) {
	tests := []struct {
		name       string
		paramValue string
		wantOK     bool
		wantCode   int
	}{
		{
			name:       "valid uuid",
			paramValue: "d9b3a520-2a3c-4f6b-8b9a-1c2d3e4f5a6b",
			wantOK:     true,
		},
		{
			name:       "invalid uuid",
			paramValue: "not-a-uuid",
			wantOK:     false,
			wantCode:   http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var gotValue string
			var gotOK bool
			mux.HandleFunc("GET /test/{uuid}", func(w http.ResponseWriter, r *http.Request) {
				gotValue, gotOK = requiredUUIDPathParam(w, r, "uuid")
			})
			req := httptest.NewRequest(http.MethodGet, "/test/"+tt.paramValue, http.NoBody)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tt.wantOK)
			}
			if tt.wantOK && gotValue != tt.paramValue {
				t.Fatalf("value = %q, want %q", gotValue, tt.paramValue)
			}
			if !tt.wantOK && w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

func TestVMDetection_GetResource(t *testing.T) {
	tests := []struct {
		name     string
		vmID     string
		expected string
	}{
		{
			name:     "returns VM ID",
			vmID:     "vm-123",
			expected: "vm-123",
		},
		{
			name:     "returns empty string when VM ID is empty",
			vmID:     "",
			expected: "",
		},
		{
			name:     "returns UUID format VM ID",
			vmID:     "550e8400-e29b-41d4-a716-446655440000",
			expected: "550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := VMDetection{VMID: tt.vmID}
			if got := d.GetResource(); got != tt.expected {
				t.Errorf("GetResource() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestVMDetection_GetReason(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		expected string
	}{
		{
			name:     "returns reason",
			reason:   "high CPU usage",
			expected: "high CPU usage",
		},
		{
			name:     "returns empty string when reason is empty",
			reason:   "",
			expected: "",
		},
		{
			name:     "returns detailed reason",
			reason:   "kvm monitoring indicates cpu steal pct 85.50% which is above 80.00% threshold",
			expected: "kvm monitoring indicates cpu steal pct 85.50% which is above 80.00% threshold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := VMDetection{Reason: tt.reason}
			if got := d.GetReason(); got != tt.expected {
				t.Errorf("GetReason() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestVMDetection_GetHost(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected string
	}{
		{
			name:     "returns host",
			host:     "compute-host-1",
			expected: "compute-host-1",
		},
		{
			name:     "returns empty string when host is empty",
			host:     "",
			expected: "",
		},
		{
			name:     "returns FQDN host",
			host:     "compute-host-1.example.com",
			expected: "compute-host-1.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := VMDetection{Host: tt.host}
			if got := d.GetHost(); got != tt.expected {
				t.Errorf("GetHost() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestVMDetection_WithReason(t *testing.T) {
	tests := []struct {
		name          string
		initialReason string
		newReason     string
		expectedVMID  string
		expectedHost  string
	}{
		{
			name:          "sets new reason",
			initialReason: "old reason",
			newReason:     "new reason",
			expectedVMID:  "vm-123",
			expectedHost:  "host-1",
		},
		{
			name:          "sets reason from empty",
			initialReason: "",
			newReason:     "new reason",
			expectedVMID:  "vm-456",
			expectedHost:  "host-2",
		},
		{
			name:          "clears reason",
			initialReason: "existing reason",
			newReason:     "",
			expectedVMID:  "vm-789",
			expectedHost:  "host-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := VMDetection{
				VMID:   tt.expectedVMID,
				Reason: tt.initialReason,
				Host:   tt.expectedHost,
			}

			result := d.WithReason(tt.newReason)

			// Check that the reason was updated
			if got := result.GetReason(); got != tt.newReason {
				t.Errorf("WithReason() reason = %v, want %v", got, tt.newReason)
			}

			// Check that VMID is preserved
			if got := result.GetResource(); got != tt.expectedVMID {
				t.Errorf("WithReason() preserved VMID = %v, want %v", got, tt.expectedVMID)
			}

			// Check that Host is preserved
			if got := result.GetHost(); got != tt.expectedHost {
				t.Errorf("WithReason() preserved Host = %v, want %v", got, tt.expectedHost)
			}
		})
	}
}

func TestVMDetection_ImplementsDetectionInterface(t *testing.T) {
	// Verify that VMDetection implements the lib.Detection interface
	var _ lib.Detection = VMDetection{}
	var _ lib.Detection = &VMDetection{}

	d := VMDetection{
		VMID:   "test-vm",
		Reason: "test reason",
		Host:   "test-host",
	}

	// Verify interface methods work correctly
	if d.GetResource() != "test-vm" {
		t.Error("GetResource() interface method not working")
	}
	if d.GetReason() != "test reason" {
		t.Error("GetReason() interface method not working")
	}
	if d.GetHost() != "test-host" {
		t.Error("GetHost() interface method not working")
	}

	updated := d.WithReason("updated reason")
	if updated.GetReason() != "updated reason" {
		t.Error("WithReason() interface method not working")
	}
}

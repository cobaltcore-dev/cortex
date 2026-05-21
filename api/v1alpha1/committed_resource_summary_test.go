// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ptr[T any](v T) *T { return &v }

func makeSpec(amount, fg, az string, endTime *time.Time) CommittedResourceSpec {
	s := CommittedResourceSpec{
		Amount:           resource.MustParse(amount),
		FlavorGroupName:  fg,
		AvailabilityZone: az,
		ResourceType:     CommittedResourceTypeMemory,
		ProjectID:        "proj-1",
		DomainID:         "dom-1",
		CommitmentUUID:   "uuid-1",
		State:            CommitmentStatusConfirmed,
	}
	if endTime != nil {
		s.EndTime = &metav1.Time{Time: *endTime}
	}
	return s
}

func makeStatus(reason string, accepted *CommittedResourceSpec, instances []string) CommittedResourceStatus {
	status := CommittedResourceStatus{
		AssignedInstances: instances,
	}
	if accepted != nil {
		status.AcceptedSpec = accepted.DeepCopy()
	}
	condStatus := metav1.ConditionTrue
	if reason != CommittedResourceReasonAccepted {
		condStatus = metav1.ConditionFalse
	}
	status.Conditions = []metav1.Condition{{
		Type:   CommittedResourceConditionReady,
		Status: condStatus,
		Reason: reason,
	}}
	return status
}

func TestComputeStatusSummary(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	in := func(d time.Duration) *time.Time { t := now.Add(d); return &t }

	tests := []struct {
		name   string
		spec   CommittedResourceSpec
		status CommittedResourceStatus
		want   string
	}{
		{
			name:   "no Ready condition",
			spec:   makeSpec("2Gi", "fg1", "az1", in(4*time.Hour)),
			status: CommittedResourceStatus{},
			want:   "",
		},
		{
			name:   "Accepted, 3 VMs, exp in hours",
			spec:   makeSpec("2Gi", "fg1", "az1", in(4*time.Hour+30*time.Minute)),
			status: makeStatus(CommittedResourceReasonAccepted, ptr(makeSpec("2Gi", "fg1", "az1", in(4*time.Hour+30*time.Minute))), []string{"a", "b", "c"}),
			want:   "Accepted · 3 VMs · exp in 4h 30m",
		},
		{
			name:   "Accepted, 1 VM, singular",
			spec:   makeSpec("2Gi", "fg1", "az1", in(25*time.Hour+2*time.Minute)),
			status: makeStatus(CommittedResourceReasonAccepted, ptr(makeSpec("2Gi", "fg1", "az1", in(25*time.Hour+2*time.Minute))), []string{"a"}),
			want:   "Accepted · 1 VM · exp in 1d 1h",
		},
		{
			name:   "Accepted, 0 VMs, no expiry",
			spec:   makeSpec("2Gi", "fg1", "az1", nil),
			status: makeStatus(CommittedResourceReasonAccepted, ptr(makeSpec("2Gi", "fg1", "az1", nil)), nil),
			want:   "Accepted · 0 VMs · no expiry",
		},
		{
			name: "Accepted with amount diff",
			spec: makeSpec("5Gi", "fg1", "az1", in(3*24*time.Hour+2*time.Hour)),
			status: makeStatus(CommittedResourceReasonAccepted,
				ptr(makeSpec("2Gi", "fg1", "az1", in(3*24*time.Hour+2*time.Hour))),
				[]string{"a", "b", "c"}),
			want: "Accepted (amount 2Gi→5Gi) · 3 VMs · exp in 3d 2h",
		},
		{
			name: "Accepted with az and fg diff",
			spec: func() CommittedResourceSpec {
				s := makeSpec("2Gi", "fg2", "az2", nil)
				return s
			}(),
			status: makeStatus(CommittedResourceReasonAccepted,
				ptr(makeSpec("2Gi", "fg1", "az1", nil)),
				[]string{"a"}),
			want: "Accepted (fg fg1→fg2; az az1→az2) · 1 VM · no expiry",
		},
		{
			name:   "Reserving with expiry",
			spec:   makeSpec("2Gi", "fg1", "az1", in(3*24*time.Hour+2*time.Hour)),
			status: makeStatus(CommittedResourceReasonReserving, ptr(makeSpec("2Gi", "fg1", "az1", in(3*24*time.Hour+2*time.Hour))), nil),
			want:   "Reserving · exp in 3d 2h",
		},
		{
			name:   "Reserving with amount diff",
			spec:   makeSpec("5Gi", "fg1", "az1", in(time.Hour+3*time.Minute)),
			status: makeStatus(CommittedResourceReasonReserving, ptr(makeSpec("2Gi", "fg1", "az1", in(time.Hour+3*time.Minute))), nil),
			want:   "Reserving (amount 2Gi→5Gi) · exp in 1h 3m",
		},
		{
			name:   "Rejected",
			spec:   makeSpec("2Gi", "fg1", "az1", in(4*time.Hour)),
			status: makeStatus(CommittedResourceReasonRejected, ptr(makeSpec("2Gi", "fg1", "az1", in(4*time.Hour))), nil),
			want:   "Rejected",
		},
		{
			name:   "Planned with expiry in days",
			spec:   makeSpec("2Gi", "fg1", "az1", in(3*24*time.Hour+2*time.Hour)),
			status: makeStatus(CommittedResourceReasonPlanned, nil, nil),
			want:   "Planned · exp in 3d 2h",
		},
		{
			name:   "Planned no expiry",
			spec:   makeSpec("2Gi", "fg1", "az1", nil),
			status: makeStatus(CommittedResourceReasonPlanned, nil, nil),
			want:   "Planned · no expiry",
		},
		{
			name:   "expired EndTime shows expired",
			spec:   makeSpec("2Gi", "fg1", "az1", in(-time.Hour)),
			status: makeStatus(CommittedResourceReasonAccepted, ptr(makeSpec("2Gi", "fg1", "az1", in(-time.Hour))), []string{"a"}),
			want:   "Accepted · 1 VM · expired",
		},
		{
			name:   "sub-minute expiry shows seconds",
			spec:   makeSpec("2Gi", "fg1", "az1", in(18*time.Second)),
			status: makeStatus(CommittedResourceReasonAccepted, ptr(makeSpec("2Gi", "fg1", "az1", in(18*time.Second))), nil),
			want:   "Accepted · 0 VMs · exp in 18s",
		},
		{
			name:   "sub-hour expiry shows minutes and seconds",
			spec:   makeSpec("2Gi", "fg1", "az1", in(23*time.Minute+5*time.Second)),
			status: makeStatus(CommittedResourceReasonAccepted, ptr(makeSpec("2Gi", "fg1", "az1", in(23*time.Minute+5*time.Second))), nil),
			want:   "Accepted · 0 VMs · exp in 23m 5s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeStatusSummary(tc.spec, tc.status, now)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatRemaining(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{18 * time.Second, "18s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m 0s"},
		{23*time.Minute + 5*time.Second, "23m 5s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{time.Hour, "1h 0m"},
		{time.Hour + 3*time.Minute, "1h 3m"},
		{23*time.Hour + 59*time.Minute, "23h 59m"},
		{24 * time.Hour, "1d 0h"},
		{3*24*time.Hour + 2*time.Hour, "3d 2h"},
	}
	for _, tc := range tests {
		t.Run(tc.d.String(), func(t *testing.T) {
			got := formatRemaining(tc.d)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

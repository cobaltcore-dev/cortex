// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// SchedulingIntent defines the intent of a scheduling decision (e.g. initial
// scheduling, rescheduling, failover). It is the base type for the Nova intent
// constants in api/external/nova and is carried on Reservation.Spec.Intent.
type SchedulingIntent string

const (
	// SchedulingIntentUnknown is the default intent when none is specified.
	SchedulingIntentUnknown SchedulingIntent = "Unknown"
)

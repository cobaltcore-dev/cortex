// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMonitor_RecordCalculation(t *testing.T) {
	m := NewMonitor()
	m.RecordCalculation(calculationSummary{
		duration:             500 * time.Millisecond,
		partitions:           3,
		desiredReservations:  4,
		existingReservations: 5,
	})

	if got := testutil.ToFloat64(m.calculationRuns); got != 1 {
		t.Errorf("calculationRuns = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.partitions); got != 3 {
		t.Errorf("partitions = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.desiredReservations); got != 4 {
		t.Errorf("desiredReservations = %v, want 4", got)
	}
	if got := testutil.ToFloat64(m.existingReservations); got != 5 {
		t.Errorf("existingReservations = %v, want 5", got)
	}
}

func TestMonitor_Counters(t *testing.T) {
	m := NewMonitor()
	m.RecordCalculationAbort("collect")
	m.RecordCalculationAbort("pathfinding")
	m.RecordCalculationAbort("pathfinding")
	m.RecordReservationCreated()
	m.RecordReservationUpdated()
	m.RecordReservationDeleted()
	m.RecordReservationError("create")
	m.RecordSchedulerRequest("200", 50*time.Millisecond)
	m.RecordSchedulerRequest("error", 100*time.Millisecond)

	expected := `
# HELP cortex_failover_host_calculation_aborts_total Total number of failover host calculation-loop passes aborted before producing a desired state, by reason
# TYPE cortex_failover_host_calculation_aborts_total counter
cortex_failover_host_calculation_aborts_total{reason="collect"} 1
cortex_failover_host_calculation_aborts_total{reason="pathfinding"} 2
`
	if err := testutil.CollectAndCompare(m.calculationAborts, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}

	if got := testutil.ToFloat64(m.reservationsCreated); got != 1 {
		t.Errorf("reservationsCreated = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.reservationsUpdated); got != 1 {
		t.Errorf("reservationsUpdated = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.reservationsDeleted); got != 1 {
		t.Errorf("reservationsDeleted = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.reservationsErrors.WithLabelValues("create")); got != 1 {
		t.Errorf("reservationsErrors{create} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.schedulerRequests.WithLabelValues("200")); got != 1 {
		t.Errorf("schedulerRequests{200} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.schedulerRequests.WithLabelValues("error")); got != 1 {
		t.Errorf("schedulerRequests{error} = %v, want 1", got)
	}
}

// A nil monitor must be safe to call so controllers and tests can run without one.
func TestMonitor_NilSafe(t *testing.T) {
	var m *Monitor
	m.RecordCalculation(calculationSummary{})
	m.RecordCalculationAbort("collect")
	m.RecordSchedulerRequest("200", time.Second)
	m.RecordReservationCreated()
	m.RecordReservationUpdated()
	m.RecordReservationDeleted()
	m.RecordReservationError("create")
}

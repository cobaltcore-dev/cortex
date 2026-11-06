// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package changes

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// setProgressingAge sets the Progressing condition on a change request with a LastTransitionTime
// backdated by age, simulating a request that entered its current phase that long ago.
func setProgressingAge(t *testing.T, c client.Client, txName string, age time.Duration) {
	t.Helper()
	var tx v1alpha1.CommittedResourceChange
	if err := c.Get(testCtx, types.NamespacedName{Name: txName}, &tx); err != nil {
		t.Fatalf("get CRC %q: %v", txName, err)
	}
	tx.Status.Conditions = []metav1.Condition{{
		Type:               v1alpha1.ChangeConditionProgressing,
		Status:             metav1.ConditionTrue,
		Reason:             "Advancing",
		LastTransitionTime: metav1.NewTime(time.Now().Add(-age)),
	}}
	if err := c.Status().Update(testCtx, &tx); err != nil {
		t.Fatalf("update CRC %q status: %v", txName, err)
	}
}

// TestChangeReconcilerMonitor_Stuck verifies the stuck gauge counts only non-terminal change
// requests older than the threshold, excluding fresh and terminal ones.
func TestChangeReconcilerMonitor_Stuck(t *testing.T) {
	scheme := testScheme(t)

	mk := func(name, az string, phase v1alpha1.ChangePhase) *v1alpha1.CommittedResourceChange {
		return &v1alpha1.CommittedResourceChange{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.CommittedResourceChangeSpec{AvailabilityZone: az, Source: v1alpha1.TxSourceAPI},
			Status:     v1alpha1.CommittedResourceChangeStatus{Phase: phase},
		}
	}
	c := testClient(t, scheme,
		mk("tx-stuck", "az-1", v1alpha1.ChangePhaseRollingBack),
		mk("tx-fresh", "az-1", v1alpha1.ChangePhaseAwaitingScheduling),
		mk("tx-term", "az-1", v1alpha1.ChangePhaseAccepted),
	)

	// Only the stuck one is older than the threshold. The terminal one is old too but must be
	// excluded because it is terminal; the fresh one is below the threshold.
	setProgressingAge(t, c, "tx-stuck", 2*time.Hour)
	setProgressingAge(t, c, "tx-fresh", 0)
	setProgressingAge(t, c, "tx-term", 2*time.Hour)

	m := NewChangeReconcilerMonitor(c, 30*time.Minute)

	const want = `
# HELP cortex_committed_resource_change_stuck Number of non-terminal CommittedResourceChanges stuck in their current phase beyond the configured threshold.
# TYPE cortex_committed_resource_change_stuck gauge
cortex_committed_resource_change_stuck{availability_zone="az-1",phase="RollingBack"} 1
`
	if err := testutil.CollectAndCompare(&m, strings.NewReader(want), "cortex_committed_resource_change_stuck"); err != nil {
		t.Errorf("unexpected stuck metric:\n%v", err)
	}
}

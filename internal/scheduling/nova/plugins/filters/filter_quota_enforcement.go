// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
)

type FilterQuotaEnforcementOpts struct{}

func (FilterQuotaEnforcementOpts) Validate() error { return nil }

// FilterQuotaEnforcement enforces project quota in the scheduling pipeline.
//
// It checks whether a VM scheduling request has headroom under the project's quota
// by performing a two-tier check:
//
//  1. CR headroom: checks if any CommittedResource CRD for the project has unused
//     capacity that can accommodate the request (Spec.Amount - Status.UsedResources >= request).
//
//  2. PAYG headroom: checks if the pay-as-you-go budget has room:
//     Quota[resource] - sum(CR.Spec.Amount) - PaygUsage[resource] >= request.
//
// If neither tier has headroom, ALL hosts are removed from the result (global reject).
// The filter skips enforcement for non-VM-creation intents (evacuations, live migrations,
// reservation scheduling) since those don't represent new resource consumption.
//
// Disabled by default; activated by adding "filter_quota_enforcement" to the pipeline config.
type FilterQuotaEnforcement struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterQuotaEnforcementOpts]
}

func (s *FilterQuotaEnforcement) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	// Step 1: Skip intents that don't represent new resource consumption.
	intent, err := request.GetIntent()
	if err == nil {
		switch intent {
		case api.EvacuateIntent, api.LiveMigrationIntent:
			traceLog.Info("skipping quota enforcement for non-consuming intent", "intent", intent)
			return result, nil
		case api.ReserveForFailoverIntent:
			traceLog.Info("skipping quota enforcement for failover reservation intent")
			return result, nil
		case api.ReserveForCommittedResourceIntent:
			// TODO: revisit whether committed resource reservation scheduling should also be quota-checked
			traceLog.Info("skipping quota enforcement for committed resource reservation intent")
			return result, nil
		}
	}

	// Step 2: Extract project, AZ, and hw_version from the request.
	projectID := request.Spec.Data.ProjectID
	az := request.Spec.Data.AvailabilityZone
	hwVersion := request.Spec.Data.Flavor.Data.ExtraSpecs["hw_version"]

	if projectID == "" {
		traceLog.Warn("no project ID in request, skipping quota enforcement")
		return result, nil
	}
	if az == "" {
		traceLog.Warn("no availability zone in request, skipping quota enforcement")
		return result, nil
	}
	if hwVersion == "" {
		traceLog.Warn("no hw_version in flavor extra specs, skipping quota enforcement")
		return result, nil
	}

	// Step 3: Compute resource demands from the request.
	numInstances := request.Spec.Data.NumInstances
	if numInstances == 0 {
		numInstances = 1
	}
	memoryMB := request.Spec.Data.Flavor.Data.MemoryMB
	vcpus := request.Spec.Data.Flavor.Data.VCPUs
	// MB to GiB: using 1024 as divisor is slightly conservative (overestimates GiB needed).
	requestRAM := int64(math.Ceil(float64(memoryMB*numInstances) / 1024.0))
	requestCores := int64(vcpus * numInstances) //nolint:gosec // VCPUs from flavor specs, realistically bounded
	requestInstances := int64(numInstances)

	// Step 4: Derive the liquid resource names.
	resourceRAM := "hw_version_" + hwVersion + "_ram"
	resourceCores := "hw_version_" + hwVersion + "_cores"
	resourceInstances := "hw_version_" + hwVersion + "_instances"

	traceLog.Info("quota enforcement check",
		"projectID", projectID,
		"az", az,
		"hwVersion", hwVersion,
		"requestRAM", requestRAM,
		"requestCores", requestCores,
		"requestInstances", requestInstances,
	)

	// Step 5: List CommittedResource CRDs and check CR headroom.
	var crList v1alpha1.CommittedResourceList
	if err := s.Client.List(context.Background(), &crList); err != nil {
		traceLog.Error("failed to list CommittedResources", "error", err)
		return nil, err
	}

	// Filter matching CRs and check headroom + accumulate amounts for PAYG formula.
	var matchingCRAmountGiB int64
	crHasHeadroom := false

	for i := range crList.Items {
		cr := &crList.Items[i]

		// Prefer AcceptedSpec (last successful reconcile snapshot) over Spec
		// to avoid mis-bucketing during spec transitions.
		spec := &cr.Spec
		if cr.Status.AcceptedSpec != nil {
			spec = cr.Status.AcceptedSpec
		}

		// Must match project, AZ, flavor group, resource type, and be active.
		if spec.ProjectID != projectID {
			continue
		}
		if spec.AvailabilityZone != az {
			continue
		}
		if spec.FlavorGroupName != hwVersion {
			continue
		}
		if spec.ResourceType != v1alpha1.CommittedResourceTypeMemory {
			continue
		}
		if spec.State != v1alpha1.CommitmentStatusConfirmed && spec.State != v1alpha1.CommitmentStatusGuaranteed {
			continue
		}

		// Convert CR amount to GiB.
		amountGiB := quantityToGiB(spec.Amount)
		matchingCRAmountGiB += amountGiB

		// Check if this specific CR has headroom for the request.
		if !crHasHeadroom {
			usedGiB := int64(0)
			if cr.Status.UsedResources != nil {
				if usedQty, ok := cr.Status.UsedResources["memory"]; ok {
					usedGiB = quantityToGiB(usedQty)
				}
			}
			freeGiB := amountGiB - usedGiB
			if freeGiB >= requestRAM {
				traceLog.Info("CR headroom found",
					"cr", cr.Name,
					"amountGiB", amountGiB,
					"usedGiB", usedGiB,
					"freeGiB", freeGiB,
					"requestRAM", requestRAM,
				)
				crHasHeadroom = true
			}
		}
	}

	if crHasHeadroom {
		traceLog.Info("quota enforcement ACCEPT: CR headroom available")
		return result, nil
	}

	// Step 6: Get ProjectQuota for this project + AZ by deterministic name.
	var projectQuota v1alpha1.ProjectQuota
	pqName := fmt.Sprintf("quota-%s-%s", projectID, az)
	if err := s.Client.Get(context.Background(), types.NamespacedName{Name: pqName}, &projectQuota); err != nil {
		if apierrors.IsNotFound(err) {
			traceLog.Info("no ProjectQuota CRD found for project+AZ, skipping enforcement",
				"projectID", projectID, "az", az)
			return result, nil
		}
		traceLog.Error("failed to get ProjectQuota", "name", pqName, "error", err)
		return nil, err
	}

	// Step 7: PAYG headroom check for all resources (ram, cores, instances).
	// For RAM: paygHeadroom = Quota - sum(CR amounts) - PaygUsage (CRs reserve RAM capacity)
	// For cores/instances: paygHeadroom = Quota - PaygUsage (no CR deduction)
	type resourceCheck struct {
		name     string
		request  int64
		crDeduct int64
	}
	checks := []resourceCheck{
		{name: resourceRAM, request: requestRAM, crDeduct: matchingCRAmountGiB},
		{name: resourceCores, request: requestCores, crDeduct: 0},
		{name: resourceInstances, request: requestInstances, crDeduct: 0},
	}

	for _, check := range checks {
		if projectQuota.Spec.Quota == nil {
			continue
		}
		quota, quotaSet := projectQuota.Spec.Quota[check.name]
		if !quotaSet {
			continue
		}

		paygUsage := int64(0)
		if projectQuota.Status.PaygUsage != nil {
			paygUsage = projectQuota.Status.PaygUsage[check.name]
		}

		headroom := quota - check.crDeduct - paygUsage

		traceLog.Info("PAYG headroom calculation",
			"resource", check.name,
			"quota", quota,
			"crDeduct", check.crDeduct,
			"paygUsage", paygUsage,
			"headroom", headroom,
			"request", check.request,
		)

		if headroom < check.request {
			traceLog.Info("quota enforcement REJECT: no PAYG headroom",
				"projectID", projectID,
				"az", az,
				"resource", check.name,
				"request", check.request,
				"headroom", headroom,
			)
			for host := range result.Activations {
				delete(result.Activations, host)
			}
			return result, nil
		}
	}

	traceLog.Info("quota enforcement ACCEPT: PAYG headroom available")
	return result, nil
}

// quantityToGiB converts a Kubernetes resource.Quantity to GiB (int64).
// Uses ceiling division to be conservative.
func quantityToGiB(q resource.Quantity) int64 {
	// Value() returns the quantity in the base unit (bytes for memory).
	bytes := q.Value()
	const giB = int64(1024 * 1024 * 1024)
	// Ceiling division: (bytes + giB - 1) / giB
	return (bytes + giB - 1) / giB
}

func init() {
	Index["filter_quota_enforcement"] = func() NovaFilter { return &FilterQuotaEnforcement{} }
}

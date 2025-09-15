// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Commitment model from the limes API.
// See: https://github.com/sapcc/limes/blob/5ea068b/docs/users/api-spec-resources.md?plain=1#L493
// See: https://github.com/sapcc/go-api-declarations/blob/94ee3e5/limes/resources/commitment.go#L19
type Commitment struct {
	// A unique numerical identifier for this commitment. This API uses this
	// numerical ID to refer to the commitment in other API calls.
	ID int `json:"id"`
	// A unique string identifier for this commitment. The next major version of
	// this API will use this UUID instead of the numerical ID to refer to
	// commitments in API calls.
	UUID string `json:"uuid"`
	// The resource for which usage is committed.
	ServiceType  string `json:"service_type"`
	ResourceName string `json:"resource_name"`
	// The availability zone in which usage is committed.
	AvailabilityZone string `json:"availability_zone"`
	// The amount of usage that was committed to.
	Amount uint64 `json:"amount"`
	// For measured resources, the unit for this resource. The value from the
	// amount field is measured in this unit.
	Unit string `json:"unit"`
	// The requested duration of this commitment, expressed as a comma-separated
	// sequence of positive integer multiples of time units like "1 year,
	// 3 months". Acceptable time units include "second", "minute", "hour",
	// "day", "month" and "year".
	Duration string `json:"duration"`
	// UNIX timestamp when this commitment was created.
	CreatedAt uint64 `json:"created_at"`
	// UNIX timestamp when this commitment should be confirmed. Only shown if
	// this was given when creating the commitment, to delay confirmation into
	// the future.
	ConfirmBy *uint64 `json:"confirm_by,omitempty"`
	// UNIX timestamp when this commitment was confirmed. Only shown after
	// confirmation.
	ConfirmedAt *uint64 `json:"confirmed_at,omitempty"`
	// UNIX timestamp when this commitment is set to expire. Note that the
	// duration counts from confirmBy (or from createdAt for immediately-
	// confirmed commitments) and is calculated at creation time, so this is
	// also shown on unconfirmed commitments.
	ExpiresAt uint64 `json:"expires_at"`
	// Whether the commitment is marked for transfer to a different project.
	// Transferable commitments do not count towards quota calculation in their
	// project, but still block capacity and still count towards billing. Not
	// shown if false.
	Transferable bool `json:"transferable"`
	// The current status of this commitment. If provided, one of "planned",
	// "pending", "guaranteed", "confirmed", "superseded", or "expired".
	Status string `json:"status,omitempty"`
	// Whether a mail notification should be sent if a created commitment is
	// confirmed. Can only be set if the commitment contains a confirmBy value.
	NotifyOnConfirm bool `json:"notify_on_confirm"`

	// Data from Keystone

	// The openstack project ID this commitment is for.
	ProjectID string `json:"project_id"`
	// The openstack domain ID this commitment is for.
	DomainID string `json:"domain_id"`

	// Resolved flavor if the commitment is for a specific instance,
	// i.e. has the unit instances_<flavor_name>.
	Flavor *Flavor
}

// Convert the given limes commitment unit and value to a resource.Quantity.
func (c *Commitment) ParseResource() (resource.Quantity, error) {
	val := int64(c.Amount)
	switch c.Unit {
	case "":
		return *resource.NewQuantity(val, resource.DecimalSI), nil
	case "B":
		return *resource.NewQuantity(val, resource.BinarySI), nil
	case "KiB":
		return *resource.NewQuantity(val*1024, resource.BinarySI), nil
	case "MiB":
		return *resource.NewQuantity(val*1024*1024, resource.BinarySI), nil
	case "GiB":
		return *resource.NewQuantity(val*1024*1024*1024, resource.BinarySI), nil
	case "TiB":
		return *resource.NewQuantity(val*1024*1024*1024*1024, resource.BinarySI), nil
	default:
		return resource.Quantity{}, fmt.Errorf("unsupported limes unit: %s", c.Unit)
	}
}

// OpenStack flavor model as returned by the Nova API under /flavors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-flavors
type Flavor struct {
	ID          string  `json:"id"`
	Disk        int     `json:"disk"` // in GB.
	RAM         int     `json:"ram"`  // in MB.
	Name        string  `json:"name"`
	RxTxFactor  float64 `json:"rxtx_factor"`
	VCPUs       int     `json:"vcpus"`
	IsPublic    bool    `json:"os-flavor-access:is_public"`
	Ephemeral   int     `json:"OS-FLV-EXT-DATA:ephemeral"`
	Description string  `json:"description"`

	// JSON string of extra specifications used when scheduling the flavor.
	ExtraSpecs map[string]string `json:"extra_specs" db:"extra_specs"`
}

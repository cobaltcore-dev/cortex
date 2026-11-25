// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

// Commitment model from the OpenStack limes API.
// See: https://github.com/sapcc/limes/blob/5ea068b/docs/users/api-spec-resources.md?plain=1#L493
// See: https://github.com/sapcc/go-api-declarations/blob/94ee3e5/limes/resources/commitment.go#L19
type Commitment struct {
	// Fields from the OpenStack limes API.

	// A unique numerical identifier for this commitment. This API uses this
	// numerical ID to refer to the commitment in other API calls.
	ID int `json:"id" db:"id,primarykey"`
	// A unique string identifier for this commitment. The next major version of
	// this API will use this UUID instead of the numerical ID to refer to
	// commitments in API calls.
	UUID string `json:"uuid" db:"uuid,primarykey"`
	// The resource for which usage is committed.
	ServiceType  string `json:"service_type" db:"service_type"`
	ResourceName string `json:"resource_name" db:"resource_name"`
	// The availability zone in which usage is committed.
	AvailabilityZone string `json:"availability_zone" db:"availability_zone"`
	// The amount of usage that was committed to.
	Amount uint64 `json:"amount" db:"amount"`
	// For measured resources, the unit for this resource. The value from the
	// amount field is measured in this unit.
	Unit string `json:"unit" db:"unit"`
	// The requested duration of this commitment, expressed as a comma-separated
	// sequence of positive integer multiples of time units like "1 year,
	// 3 months". Acceptable time units include "second", "minute", "hour",
	// "day", "month" and "year".
	Duration string `json:"duration" db:"duration"`
	// UNIX timestamp when this commitment was created.
	CreatedAt uint64 `json:"created_at" db:"created_at"`
	// UNIX timestamp when this commitment should be confirmed. Only shown if
	// this was given when creating the commitment, to delay confirmation into
	// the future.
	ConfirmBy *uint64 `json:"confirm_by,omitempty" db:"confirm_by"`
	// UNIX timestamp when this commitment was confirmed. Only shown after
	// confirmation.
	ConfirmedAt *uint64 `json:"confirmed_at,omitempty" db:"confirmed_at"`
	// UNIX timestamp when this commitment is set to expire. Note that the
	// duration counts from confirm_by (or from created_at for immediately-
	// confirmed commitments) and is calculated at creation time, so this is
	// also shown on unconfirmed commitments.
	ExpiresAt uint64 `json:"expires_at" db:"expires_at"`
	// Whether the commitment is marked for transfer to a different project.
	// Transferable commitments do not count towards quota calculation in their
	// project, but still block capacity and still count towards billing. Not
	// shown if false.
	Transferable bool `json:"transferable" db:"transferable"`
	// The current status of this commitment. If provided, one of "planned",
	// "pending", "guaranteed", "confirmed", "superseded", or "expired".
	Status string `json:"status,omitempty" db:"status"`
	// Whether a mail notification should be sent if a created commitment is
	// confirmed. Can only be set if the commitment contains a confirm_by value.
	NotifyOnConfirm bool `json:"notify_on_confirm" db:"notify_on_confirm"`

	// Additional fields added during the fetch process.

	// The openstack project ID this commitment is for.
	ProjectID string `json:"project_id" db:"project_id"`
	// The openstack domain ID this commitment is for.
	DomainID string `json:"domain_id" db:"domain_id"`
}

// Table in which the openstack model is stored.
func (Commitment) TableName() string { return "openstack_limes_commitments_v2" }

// Indexes for the resource provider table.
func (Commitment) Indexes() map[string][]string { return nil }

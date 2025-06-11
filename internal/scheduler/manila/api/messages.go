// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

// Manila request context object. For the spec of this object, see:
//
// - This: https://github.com/sapcc/manila/blob/4ffdfc/manila/context.py#L29
// - And: https://github.com/openstack/oslo.context/blob/db20dd/oslo_context/context.py#L329
//
// Some fields are omitted: "service_catalog", "read_deleted" (same as "show_deleted")
type ManilaRequestContext struct {
	// Fields added by oslo.context

	UserID          string   `json:"user"`
	ProjectID       string   `json:"project_id"`
	SystemScope     string   `json:"system_scope"`
	DomainID        string   `json:"domain"`
	UserDomainID    string   `json:"user_domain"`
	ProjectDomainID string   `json:"project_domain"`
	IsAdmin         bool     `json:"is_admin"`
	ReadOnly        bool     `json:"read_only"`
	ShowDeleted     bool     `json:"show_deleted"`
	AuthToken       string   `json:"auth_token"`
	RequestID       string   `json:"request_id"`
	GlobalRequestID string   `json:"global_request_id"`
	ResourceUUID    string   `json:"resource_uuid"`
	Roles           []string `json:"roles"`
	UserIdentity    string   `json:"user_identity"`
	IsAdminProject  bool     `json:"is_admin_project"`

	// Fields added by the Manila scheduler

	RemoteAddress string `json:"remote_address"`
	Timestamp     string `json:"timestamp"`
	QuotaClass    string `json:"quota_class"`
	ProjectName   string `json:"project_name"`
}

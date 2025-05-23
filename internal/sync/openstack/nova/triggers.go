// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

// Trigger executed when new servers are available.
const TriggerNovaServersSynced = "triggers/sync/openstack/nova/types/servers"

// Trigger executed when new hypervisors are available.
const TriggerNovaHypervisorsSynced = "triggers/sync/openstack/nova/types/hypervisors"

// Trigger executed when new flavors are available.
const TriggerNovaFlavorsSynced = "triggers/sync/openstack/nova/types/flavors"

// Trigger executed when new migrations are available.
const TriggerNovaMigrationsSynced = "triggers/sync/openstack/nova/types/migrations"

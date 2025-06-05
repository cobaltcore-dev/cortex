-- Copyright 2025 SAP SE
-- SPDX-License-Identifier: Apache-2.0

-- Modify the data types of columns in the openstack_hypervisors table
-- after the 2.53 microversion change of Nova. This changes the
-- openstack_hypervisors.id column as well as the
-- openstack_hypervisors.service_id column to UUIDs.
-- The target data types should be strings.

-- Remove hypervisors with IDs that don't contain 36 characters
DELETE FROM openstack_hypervisors
WHERE LENGTH(id) != 36;

ALTER TABLE IF EXISTS openstack_hypervisors
    ALTER COLUMN id TYPE VARCHAR(36) USING id::VARCHAR(36),
    ALTER COLUMN service_id TYPE VARCHAR(36) USING service_id::VARCHAR(36);
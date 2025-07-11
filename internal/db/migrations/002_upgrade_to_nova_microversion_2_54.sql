-- Copyright 2025 SAP SE
-- SPDX-License-Identifier: Apache-2.0

-- Modify the data types of columns in the openstack_hypervisors table
-- after the 2.53 microversion change of Nova. This changes the
-- openstack_hypervisors.id column as well as the
-- openstack_hypervisors.service_id column to UUIDs.
ALTER TABLE IF EXISTS openstack_hypervisors
    ALTER COLUMN id TYPE VARCHAR(36) USING id::VARCHAR(36),
    ALTER COLUMN service_id TYPE VARCHAR(36) USING service_id::VARCHAR(36);
DELETE FROM openstack_hypervisors WHERE LENGTH(id) != 36;

-- Starting in microversion 2.54 of Nova, servers no longer return
-- the flavor id, but instead the flavor name. Thus, we need to infer
-- the flavor name through the openstack_flavors table and create the column.
ALTER TABLE IF EXISTS openstack_servers
    ADD COLUMN IF NOT EXISTS flavor_name VARCHAR(255);
-- Update the flavor_name column with the corresponding flavor names
UPDATE openstack_servers AS s
SET flavor_name = f.name
FROM openstack_flavors AS f
WHERE s.flavor_id = f.id;
-- Delete all servers where the flavor name could not be determined.
DELETE FROM openstack_servers WHERE flavor_name IS NULL;
-- Remove the flavor_id column as it is no longer needed.
ALTER TABLE IF EXISTS openstack_servers
    DROP COLUMN IF EXISTS flavor_id;
-- Also drop the flavor_id column in the dependency tables.
ALTER TABLE IF EXISTS feature_vm_host_residency
    DROP COLUMN IF EXISTS flavor_id;
ALTER TABLE IF EXISTS feature_vm_life_span
    DROP COLUMN IF EXISTS flavor_id;
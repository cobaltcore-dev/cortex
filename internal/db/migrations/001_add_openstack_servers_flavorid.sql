-- Copyright 2025 SAP SE
-- SPDX-License-Identifier: Apache-2.0

-- Add the new column with a default value.
ALTER TABLE IF EXISTS openstack_servers
ADD COLUMN IF NOT EXISTS flavor_id VARCHAR(255) DEFAULT 'flavor';

-- Update existing rows to have a non-null value
UPDATE openstack_servers
SET flavor_id = 'flavor'
WHERE flavor_id IS NULL;

-- Alter the column to set it as NOT NULL
ALTER TABLE openstack_servers
ALTER COLUMN flavor_id SET NOT NULL;
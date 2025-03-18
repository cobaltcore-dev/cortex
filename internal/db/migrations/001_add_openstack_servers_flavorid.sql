-- Copyright 2025 SAP SE
-- SPDX-License-Identifier: Apache-2.0

-- Add the new column with a default value.
ALTER TABLE IF EXISTS openstack_servers
ADD COLUMN IF NOT EXISTS flavor_id VARCHAR(255) DEFAULT 'flavor';

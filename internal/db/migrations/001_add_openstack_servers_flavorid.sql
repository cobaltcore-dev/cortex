-- Copyright 2025 SAP SE
-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE IF EXISTS openstack_servers
ADD COLUMN IF NOT EXISTS flavor_id VARCHAR(255) NOT NULL;
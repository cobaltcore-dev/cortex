-- Copyright SAP SE
-- SPDX-License-Identifier: Apache-2.0

-- Query to extract flavor groups from the openstack_flavors_v2 table
-- Groups flavors by their hw_version extra_spec (or flavor name prefix as workaround)
-- Filters to only include KVM flavors (QEMU and Cloud-Hypervisor)
SELECT 
    name,
    vcpus,
    ram as memory_mb,
    disk,
    ephemeral,
    extra_specs
FROM openstack_flavors_v2
WHERE LOWER(extra_specs) LIKE '%"capabilities:hypervisor_type":"qemu"%'
   OR LOWER(extra_specs) LIKE '%"capabilities:hypervisor_type":"ch"%'
ORDER BY name;

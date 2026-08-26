-- Details of VMware compute hosts. Only VMware hosts are considered here: KVM
-- hosts are served by a dedicated CRD, and ironic hosts are excluded. The
-- WHERE clause keeps rows whose service host matches the VMware naming
-- convention (nova-compute-%) while dropping ironic ones (nova-compute-ironic-%).
WITH host_traits AS (
    SELECT
        h.id AS hypervisor_id,
        h.service_host,
        h.running_vms,
        h.state,
        h.status,
        h.service_disabled_reason,
        STRING_AGG(t.name, ',') AS traits
    FROM openstack_hypervisors h
    LEFT JOIN openstack_resource_provider_traits t
        ON h.id = t.resource_provider_uuid
    WHERE h.service_host LIKE 'nova-compute-%'
        AND h.service_host NOT LIKE 'nova-compute-ironic-%'
    GROUP BY h.id, h.service_host, h.running_vms, h.state, h.status, h.service_disabled_reason
),
-- Physical memory size (in MiB) of a single host inside a VMware building block.
-- A building block pools multiple physical hosts into one resource provider, so
-- the per-host memory size is recovered from the pooled memory inventory:
--   amount_hosts        = (total - reserved) / max_unit
--   physical_host_size  = max_unit + reserved / amount_hosts   (in MiB)
host_physical_memory AS (
    SELECT
        i.resource_provider_uuid,
        i.max_unit
            + i.reserved / ((i.total - i.reserved) / CAST(i.max_unit AS FLOAT))
            AS physical_size_mb
    FROM openstack_resource_provider_inventory_usages i
    WHERE i.inventory_class_name = 'MEMORY_MB'
        AND i.max_unit > 0
        AND (i.total - i.reserved) > 0
)
SELECT
    ht.service_host AS compute_host,
    ht.running_vms AS running_vms,
    -- CPU Architecture
    CASE
        WHEN ht.traits LIKE '%CUSTOM_HW_SAPPHIRE_RAPIDS%' THEN 'sapphire-rapids'
        ELSE 'cascade-lake'
    END AS cpu_architecture,
    CASE
        WHEN ht.traits LIKE '%CUSTOM_HANA_EXCLUSIVE_HOST%' THEN 'hana'
        ELSE 'general-purpose'
    END AS workload_type,
    CASE
        WHEN ht.traits LIKE '%CUSTOM_DECOMMISSIONING%' THEN true
        ELSE false
    END AS decommissioned,
    CASE
        WHEN ht.traits LIKE '%CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE%' THEN true
        ELSE false
    END AS external_customer,
    CASE
        WHEN ht.traits LIKE '%COMPUTE_STATUS_DISABLED%' THEN false
        WHEN ht.status != 'enabled' THEN false
        WHEN ht.state != 'up' THEN false
        ELSE true
    END AS enabled,
    CASE
        WHEN ht.traits LIKE '%COMPUTE_STATUS_DISABLED%' THEN '[disabled] ' || COALESCE(ht.service_disabled_reason, '--')
        WHEN ht.status != 'enabled' THEN '[disabled] ' || COALESCE(ht.service_disabled_reason, '--')
        WHEN ht.state != 'up' THEN '[down] ' || COALESCE(ht.service_disabled_reason, '--')
        ELSE NULL
    END AS disabled_reason,
    -- Physical host size category of a single host inside the building block.
    -- The size is first rounded to whole GiB; if that is at least 1024 GiB it is
    -- reported in TiB ("<n>TiB"), otherwise in GiB ("<n>GiB"). Rounding to GiB
    -- first avoids a value like 1023.6 GiB being shown as "1024GiB" instead of
    -- "1TiB". "unknown" when the memory inventory is missing.
    CASE
        WHEN hpm.physical_size_mb IS NULL THEN 'unknown'
        WHEN ROUND(hpm.physical_size_mb / 1024.0) >= 1024
        THEN CAST(CAST(ROUND(ROUND(hpm.physical_size_mb / 1024.0) / 1024.0) AS INTEGER) AS TEXT) || 'TiB'
        ELSE CAST(CAST(ROUND(hpm.physical_size_mb / 1024.0) AS INTEGER) AS TEXT) || 'GiB'
    END AS physical_host_size
FROM host_traits ht
LEFT JOIN host_physical_memory hpm
    ON ht.hypervisor_id = hpm.resource_provider_uuid;

SELECT
    h.service_host AS compute_host,
    -- Total allocatable capacity is calculated as (total) * allocation_ratio.
    CAST(i_memory_mb.total * i_memory_mb.allocation_ratio AS FLOAT)
        AS total_memory_allocatable_mb,
    CAST(i_vcpu.total * i_vcpu.allocation_ratio AS FLOAT)
        AS total_vcpus_allocatable,
    CAST(i_disk_gb.total * i_disk_gb.allocation_ratio AS FLOAT)
        AS total_disk_allocatable_gb,
    -- Utilization is calculated as (used / capacity) * 100.
    CASE
        WHEN (i_memory_mb.total * i_memory_mb.allocation_ratio) = 0 THEN 0
        ELSE (i_memory_mb.used / (i_memory_mb.total * i_memory_mb.allocation_ratio)) * 100
    END AS ram_utilized_pct,
    CASE
        WHEN (i_vcpu.total * i_vcpu.allocation_ratio) = 0 THEN 0
        ELSE (i_vcpu.used / (i_vcpu.total * i_vcpu.allocation_ratio)) * 100
    END AS vcpus_utilized_pct,
    CASE
        WHEN (i_disk_gb.total * i_disk_gb.allocation_ratio) = 0 THEN 0
        ELSE (i_disk_gb.used / (i_disk_gb.total * i_disk_gb.allocation_ratio)) * 100
    END AS disk_utilized_pct,
    a.availability_zone
FROM openstack_hypervisors AS h
JOIN openstack_resource_provider_inventory_usages AS i_memory_mb
    ON h.id = i_memory_mb.resource_provider_uuid
    AND i_memory_mb.inventory_class_name = 'MEMORY_MB'
JOIN openstack_resource_provider_inventory_usages AS i_vcpu
    ON h.id = i_vcpu.resource_provider_uuid
    AND i_vcpu.inventory_class_name = 'VCPU'
JOIN openstack_resource_provider_inventory_usages AS i_disk_gb
    ON h.id = i_disk_gb.resource_provider_uuid
    AND i_disk_gb.inventory_class_name = 'DISK_GB'
JOIN openstack_aggregates AS a
    ON h.service_host = a.compute_host
    AND a.availability_zone IS NOT NULL;
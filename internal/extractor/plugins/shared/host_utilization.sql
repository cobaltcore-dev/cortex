WITH host_resources AS (
    SELECT
        h.service_host AS compute_host,
        CAST((i_memory_mb.total - i_memory_mb.reserved) * i_memory_mb.allocation_ratio AS FLOAT) AS total_ram_allocatable_mb,
        CAST((i_vcpu.total - i_vcpu.reserved) * i_vcpu.allocation_ratio AS FLOAT) AS total_vcpus_allocatable,
        CAST((i_disk_gb.total - i_disk_gb.reserved) * i_disk_gb.allocation_ratio AS FLOAT) AS total_disk_allocatable_gb,
        i_memory_mb.used AS ram_used_mb,
        i_vcpu.used AS vcpus_used,
        i_disk_gb.used AS disk_used_gb
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
)


-- Resource usage formulas:
-- - "TotalAllocatableCapacity": The maximum usable resource after reserving capacity and applying overcommit.
--      Formula: (placement.total - placement.reserved) * placement.allocation_ratio
-- - "UsedAbsolute": The actual amount of resource currently in use (includes overcommit).
--      Formula: placement.used
-- - "UsedPct": Percentage of allocatable capacity that is currently used.
--      Formula: placement.used / TotalAllocatableCapacity
-- - "Available": Remaining allocatable resource.
--      Formula: TotalAllocatableCapacity - placement.used
--
-- Note: The "TotalAllocatableCapacity" does not include the capacity reserved for failover capacity!
-- Reference: https://github.com/openstack/placement/blob/4d3df47ee3e394e3178d58c15306620809ad2806/placement/objects/allocation.py#L224-L227

SELECT
    compute_host,
    total_ram_allocatable_mb,
    total_vcpus_allocatable,
    total_disk_allocatable_gb,
    CASE
        WHEN total_ram_allocatable_mb = 0 THEN 0
        ELSE (ram_used_mb / total_ram_allocatable_mb) * 100
    END AS ram_utilized_pct,
    CASE
        WHEN total_vcpus_allocatable = 0 THEN 0
        ELSE (vcpus_used / total_vcpus_allocatable) * 100
    END AS vcpus_utilized_pct,
    CASE
        WHEN total_disk_allocatable_gb = 0 THEN 0
        ELSE (disk_used_gb / total_disk_allocatable_gb) * 100
    END AS disk_utilized_pct,
    ram_used_mb,
    vcpus_used,
    disk_used_gb
FROM host_resources;
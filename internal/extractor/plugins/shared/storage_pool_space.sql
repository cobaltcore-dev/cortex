SELECT
    name AS storage_pool_name,
    CASE
        WHEN capabilities_free_capacity_gb < 0 THEN 0
        ELSE capabilities_free_capacity_gb
    END AS capacity_left_gb,
    CASE
        WHEN capabilities_total_capacity_gb IS NULL OR capabilities_total_capacity_gb <= 0 THEN 0
        WHEN (CAST(capabilities_free_capacity_gb AS float) / CAST(capabilities_total_capacity_gb AS float)) * 100 < 0 THEN 0
        ELSE (CAST(capabilities_free_capacity_gb AS float) / CAST(capabilities_total_capacity_gb AS float)) * 100
    END AS capacity_left_pct
FROM openstack_storage_pools;
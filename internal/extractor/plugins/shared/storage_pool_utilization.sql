SELECT
    name AS storage_pool_name,
    CASE
        WHEN capabilities_reserved_percentage IS NULL OR capabilities_reserved_percentage < 0 THEN 0
        ELSE CAST(capabilities_reserved_percentage AS float)
    END AS capacity_utilized_pct
FROM openstack_storage_pools;
SELECT
    h.service_host AS compute_host,
    -- We'll use CASE WHEN to clamp the values to 0 if they are negative.
    -- This is compatible with SQLite as well, compared to using GREATEST.
    CASE WHEN h.free_ram_mb < 0 THEN 0 ELSE h.free_ram_mb END AS ram_left_mb,
    CASE WHEN (h.vcpus - h.vcpus_used) < 0 THEN 0 ELSE (h.vcpus - h.vcpus_used) END AS vcpus_left,
    CASE WHEN h.free_disk_gb < 0 THEN 0 ELSE h.free_disk_gb END AS disk_left_gb,
    CASE
        WHEN h.memory_mb IS NULL OR h.memory_mb <= 0 THEN 0
        WHEN (CAST(h.free_ram_mb AS float) / CAST(h.memory_mb AS float)) * 100 < 0 THEN 0
        ELSE (CAST(h.free_ram_mb AS float) / CAST(h.memory_mb AS float)) * 100
    END AS ram_left_pct,
    CASE
        WHEN h.vcpus IS NULL OR h.vcpus <= 0 THEN 0
        WHEN (CAST((h.vcpus - h.vcpus_used) AS float) / CAST(h.vcpus AS float)) * 100 < 0 THEN 0
        ELSE (CAST((h.vcpus - h.vcpus_used) AS float) / CAST(h.vcpus AS float)) * 100
    END AS vcpus_left_pct,
    CASE
        WHEN h.local_gb IS NULL OR h.local_gb <= 0 THEN 0
        WHEN (CAST(h.free_disk_gb AS float) / CAST(h.local_gb AS float)) * 100 < 0 THEN 0
        ELSE (CAST(h.free_disk_gb AS float) / CAST(h.local_gb AS float)) * 100
    END AS disk_left_pct
FROM openstack_hypervisors AS h
-- Ironic hypervisors will report 0 for memory_mb, local_gb, and vcpus.
-- Therefore it doesn't really make sense to include them here.
WHERE h.hypervisor_type != 'ironic';
SELECT
    h.service_host AS compute_host,
    CASE
        WHEN h.memory_mb IS NULL OR h.memory_mb <= 0 THEN 0
        WHEN (CAST(h.memory_mb_used AS float) / CAST(h.memory_mb AS float)) * 100 < 0 THEN 0
        ELSE (CAST(h.memory_mb_used AS float) / CAST(h.memory_mb AS float)) * 100
    END AS ram_utilized_pct,
    CASE
        WHEN h.vcpus IS NULL OR h.vcpus <= 0 THEN 0
        WHEN (CAST(h.vcpus_used AS float) / CAST(h.vcpus AS float)) * 100 < 0 THEN 0
        ELSE (CAST(h.vcpus_used AS float) / CAST(h.vcpus AS float)) * 100
    END AS vcpus_utilized_pct,
    CASE
        WHEN h.local_gb IS NULL OR h.local_gb <= 0 THEN 0
        WHEN (CAST(h.local_gb_used AS float) / CAST(h.local_gb AS float)) * 100 < 0 THEN 0
        ELSE (CAST(h.local_gb_used AS float) / CAST(h.local_gb AS float)) * 100
    END AS disk_utilized_pct
FROM openstack_hypervisors AS h
-- Ironic hypervisors will report 0 for memory_mb, local_gb, and vcpus.
-- Therefore it doesn't really make sense to include them here.
WHERE h.hypervisor_type != 'ironic';
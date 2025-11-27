SELECT
    h.nova_compute_host AS compute_host,
    AVG(m.value) AS avg_cpu_contention,
    MAX(m.value) AS max_cpu_contention
FROM vrops_host_metrics m
JOIN feature_vrops_resolved_hostsystem h ON m.hostsystem = h.vrops_hostsystem
WHERE m.name = 'vrops_hostsystem_cpu_contention_short_term_percentage'
GROUP BY h.nova_compute_host;
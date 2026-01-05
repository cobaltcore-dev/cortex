SELECT
    m.hostsystem AS vrops_hostsystem,
    AVG(m.value) AS avg_cpu_contention,
    MAX(m.value) AS max_cpu_contention
FROM vrops_host_metrics m
WHERE m.name = 'vrops_hostsystem_cpu_contention_long_term_percentage'
GROUP BY m.hostsystem;
SELECT
    node AS compute_host,
    AVG(value) AS avg_memory_active,
    MAX(value) AS max_memory_active
FROM node_exporter_metrics
WHERE name = 'node_exporter_memory_active_pct'
GROUP BY node;
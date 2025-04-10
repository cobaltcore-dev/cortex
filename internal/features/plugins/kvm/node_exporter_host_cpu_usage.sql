SELECT
    node AS compute_host,
    AVG(value) AS avg_cpu_usage,
    MAX(value) AS max_cpu_usage
FROM node_exporter_metrics
WHERE name = 'node_exporter_cpu_usage_pct'
GROUP BY node;
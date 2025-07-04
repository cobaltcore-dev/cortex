SELECT
  osp.name AS storage_pool_name,
  AVG(nnm.value) AS avg_cpu_usage_pct,
  MAX(nnm.value) AS max_cpu_usage_pct
FROM openstack_storage_pools osp
JOIN netapp_aggregate_labels_metrics nalm ON nalm.aggr = osp.pool
JOIN netapp_node_metrics nnm ON nnm.node = nalm.node
WHERE nnm.name = 'netapp_node_cpu_busy'
GROUP BY osp.name, nalm.node;
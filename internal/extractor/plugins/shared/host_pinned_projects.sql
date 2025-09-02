SELECT
    uuid as aggregate_uuid,
    name as aggregate_name,
    compute_host,
    trim(tenant.value) as project_id
FROM openstack_aggregates_v2 agg
CROSS JOIN LATERAL unnest(string_to_array(agg.metadata::jsonb ->> 'filter_tenant_id', ',')) AS tenant(value)
WHERE agg.metadata::jsonb ? 'filter_tenant_id'
  AND agg.metadata::jsonb ->> 'filter_tenant_id' != ''
  AND agg.metadata::jsonb ->> 'filter_tenant_id' != '[]'
  AND trim(tenant.value) != ''
  AND trim(tenant.value) IS NOT NULL

UNION ALL

--- Get all hypervisors which don't have a filter applied to them
SELECT
    NULL as aggregate_uuid,
    NULL as aggregate_name,
    h.service_host as compute_host,
    NULL as project_id
FROM openstack_hypervisors h
WHERE h.hypervisor_type != 'ironic' --- Filter out ironic hypervisors
  AND h.service_host IS NOT NULL
  AND h.service_host NOT IN (
      SELECT DISTINCT agg.compute_host
      FROM openstack_aggregates_v2 agg
      WHERE agg.metadata::jsonb ? 'filter_tenant_id'
        AND agg.compute_host IS NOT NULL
  );
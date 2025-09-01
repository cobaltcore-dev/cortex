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
  AND trim(tenant.value) IS NOT NULL;
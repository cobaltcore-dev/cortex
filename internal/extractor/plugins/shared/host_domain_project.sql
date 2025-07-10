SELECT
  h.service_host AS compute_host,
  STRING_AGG(DISTINCT p.name, ',') AS project_names,
  STRING_AGG(DISTINCT p.id, ',') AS project_ids,
  STRING_AGG(DISTINCT d.name, ',') AS domain_names,
  STRING_AGG(DISTINCT d.id, ',') AS domain_ids
FROM
  openstack_servers s
JOIN
  openstack_projects p ON s.tenant_id = p.id
JOIN
  openstack_domains d ON p.domain_id = d.id
LEFT JOIN
  openstack_hypervisors h ON s.os_ext_srv_attr_host = h.service_host
WHERE
  s.status != 'DELETED'
  AND s.status != 'ERROR'
  AND h.service_host IS NOT NULL
GROUP BY
  h.service_host
ORDER BY
  h.service_host;
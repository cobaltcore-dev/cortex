SELECT
  h.service_host AS compute_host,
  GROUP_CONCAT(DISTINCT p.name) AS project_names,
  GROUP_CONCAT(DISTINCT p.id) AS project_ids,
  GROUP_CONCAT(DISTINCT d.name) AS domain_names,
  GROUP_CONCAT(DISTINCT d.id) AS domain_ids
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
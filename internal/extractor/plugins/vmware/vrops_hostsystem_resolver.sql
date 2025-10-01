-- Resolve hostsystem names from vROps to Nova compute hosts
SELECT DISTINCT
    m.hostsystem AS vrops_hostsystem,
    s.os_ext_srv_attr_host AS nova_compute_host
FROM vrops_vm_metrics m
LEFT JOIN openstack_servers_v2 s ON m.instance_uuid = s.id
WHERE s.os_ext_srv_attr_host IS NOT NULL;

-- Resolve hostsystem names from vROps to Nova compute hosts
SELECT
    m.hostsystem AS vrops_hostsystem,
    h.service_host AS nova_compute_host
FROM vrops_vm_metrics m
JOIN openstack_servers s ON m.instance_uuid = s.id
JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
GROUP BY m.hostsystem, h.service_host;
-- Extract the noisiness for each project in OpenStack with the following steps:
-- 1. Get the average cpu usage of each project through the vROps metrics.
-- 2. Find on which hosts the projects are currently running through the
-- OpenStack servers and hypervisors.
-- 3. Store the avg cpu usage together with the current hosts in the database.
-- This feature can then be used to draw new VMs away from VMs of the same
-- project in case this project is known to cause high cpu usage.
WITH projects_avg_cpu AS (
    SELECT
        m.project AS tenant_id,
        AVG(m.value) AS avg_cpu
    FROM vrops_vm_metrics m
    WHERE m.name = 'vrops_virtualmachine_cpu_demand_ratio'
    GROUP BY m.project
    ORDER BY avg_cpu DESC
),
host_cpu_usage AS (
    SELECT
        s.tenant_id,
        h.service_host,
        AVG(p.avg_cpu) AS avg_cpu_of_project
    FROM openstack_servers s
    JOIN vrops_vm_metrics m ON s.id = m.instance_uuid
    JOIN projects_avg_cpu p ON s.tenant_id = p.tenant_id
    JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
    GROUP BY s.tenant_id, h.service_host
    ORDER BY avg_cpu_of_project DESC
)
SELECT
    tenant_id AS project,
    service_host AS compute_host,
    avg_cpu_of_project
FROM host_cpu_usage;
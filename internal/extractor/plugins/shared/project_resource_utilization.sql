SELECT
    p.id as project_id,
    COALESCE(SUM(f.vcpus), 0) as total_vcpus_used,
    COALESCE(SUM(f.ram), 0) as total_ram_used_mb,
    COALESCE(SUM(f.disk), 0) as total_disk_used_gb,
    COUNT(s.id) as total_servers
FROM openstack_projects p
LEFT JOIN openstack_servers s ON s.tenant_id = p.id AND s.status != 'DELETED'
LEFT JOIN openstack_flavors_v2 f ON f.name = s.flavor_name
GROUP BY p.id
ORDER BY p.id;
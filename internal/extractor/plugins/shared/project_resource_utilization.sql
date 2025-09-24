SELECT
    s.tenant_id as project_id,
    az.availability_zone as availability_zone,
    COALESCE(SUM(f.vcpus), 0) as total_vcpus_used,
    COALESCE(SUM(f.ram), 0) as total_ram_used_mb,
    COALESCE(SUM(f.disk), 0) as total_disk_used_gb,
    COUNT(s.id) as total_servers,
    SUM(CASE WHEN f.name IS NULL THEN 1 ELSE 0 END) as unresolved_server_flavors
FROM openstack_servers s
LEFT JOIN openstack_flavors_v2 f ON f.name = s.flavor_name
LEFT JOIN feature_host_az az ON az.compute_host = s.os_ext_srv_attr_host
WHERE s.status != 'DELETED'
GROUP BY s.tenant_id, az.availability_zone
ORDER BY s.tenant_id;

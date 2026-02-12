SELECT
    s.id AS server_uuid,
    s.flavor_name AS flavor_name,
    s.tenant_id AS project_id,
    s.os_ext_srv_attr_host AS current_host,
    f.ram AS ram,
    f.vcpus AS vcpus
FROM openstack_servers s
LEFT JOIN openstack_flavors_v2 f
    ON s.flavor_name = f.name;
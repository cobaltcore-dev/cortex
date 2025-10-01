SELECT
    CAST(EXTRACT(EPOCH FROM (
        servers.updated::timestamp - servers.created::timestamp
    )) AS BIGINT) AS duration,
    COALESCE(flavors.name, 'unknown') AS flavor_name
FROM openstack_deleted_servers servers
LEFT JOIN openstack_flavors_v2 flavors ON flavors.name = servers.flavor_name;
-- No need to check for DELETED status as this table only contains deleted servers.
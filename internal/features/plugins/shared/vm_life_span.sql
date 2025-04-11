SELECT
    CAST(EXTRACT(EPOCH FROM (
        servers.updated::timestamp - servers.created::timestamp
    )) AS BIGINT) AS duration,
    COALESCE(servers.flavor_id, 'unknown') AS flavor_id,
    COALESCE(flavors.name, 'unknown') AS flavor_name,
    servers.id AS instance_uuid
FROM openstack_servers servers
LEFT JOIN openstack_flavors flavors ON flavors.id = servers.flavor_id
WHERE servers.status = 'DELETED';
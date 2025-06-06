SELECT
    CAST(EXTRACT(EPOCH FROM (
        servers.updated::timestamp - servers.created::timestamp
    )) AS BIGINT) AS duration,
    COALESCE(flavors.name, 'unknown') AS flavor_name,
    servers.id AS instance_uuid
FROM openstack_servers servers
LEFT JOIN openstack_flavors flavors ON flavors.name = servers.flavor_name
WHERE servers.status = 'DELETED';
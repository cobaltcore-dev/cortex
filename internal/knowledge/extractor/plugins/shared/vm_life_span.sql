WITH deleted_servers AS (
    SELECT
        EXTRACT(EPOCH FROM (servers.updated::timestamp - servers.created::timestamp))::BIGINT AS duration,
        COALESCE(flavors.name, 'unknown')::TEXT AS flavor_name,
        true::BOOLEAN AS deleted
    FROM openstack_deleted_servers servers
    LEFT JOIN openstack_flavors_v2 flavors ON flavors.name = servers.flavor_name
    WHERE servers.created IS NOT NULL AND servers.updated IS NOT NULL
),
running_servers AS (
    SELECT
        -- Life time for running servers is calculated from creation time to now
        EXTRACT(EPOCH FROM (NOW()::timestamp - servers.created::timestamp))::BIGINT AS duration,
        COALESCE(flavors.name, 'unknown')::TEXT AS flavor_name,
        false::BOOLEAN AS deleted
    FROM openstack_servers servers
    LEFT JOIN openstack_flavors_v2 flavors ON flavors.name = servers.flavor_name
    WHERE servers.created IS NOT NULL
)
SELECT * FROM deleted_servers
UNION ALL
SELECT * FROM running_servers;
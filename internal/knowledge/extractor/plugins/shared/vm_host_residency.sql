WITH durations AS (
    SELECT
        migrations.instance_uuid,
        migrations.uuid AS migration_uuid,
        flavors.name AS flavor_name,
        COALESCE(
            CAST(EXTRACT(EPOCH FROM (
                -- Use the LAG window function to get the timestamp of
                -- the previous migration
                migrations.created_at::timestamp -
                LAG(migrations.created_at) OVER (
                    PARTITION BY migrations.instance_uuid
                    ORDER BY migrations.created_at
                )::timestamp
            )) AS BIGINT),
            -- Use the duration since the server was created if there is no
            -- previous migration
            CAST(EXTRACT(EPOCH FROM (
                migrations.created_at::timestamp -
                servers.created::timestamp
            )) AS BIGINT)
        ) AS duration
    FROM openstack_migrations AS migrations
    LEFT JOIN openstack_servers AS servers ON servers.id = migrations.instance_uuid
    LEFT JOIN openstack_flavors_v2 AS flavors ON flavors.name = servers.flavor_name
)
SELECT
    -- Sometimes the server can be vanished already, set default
    -- values for that case.
    COALESCE(durations.duration, 0) AS duration,
    COALESCE(durations.flavor_name, 'unknown') AS flavor_name
FROM openstack_migrations AS migrations
LEFT JOIN durations ON migrations.uuid = durations.migration_uuid;
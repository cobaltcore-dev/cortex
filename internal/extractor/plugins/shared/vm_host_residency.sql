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
    LEFT JOIN openstack_flavors AS flavors ON flavors.id = servers.flavor_name
)
SELECT
    -- Sometimes the server can be vanished already, set default
    -- values for that case.
    COALESCE(durations.duration, 0) AS duration,
    COALESCE(durations.flavor_name, 'unknown') AS flavor_name,
    migrations.instance_uuid AS instance_uuid,
    migrations.uuid AS migration_uuid,
    migrations.source_compute AS source_host,
    migrations.dest_compute AS target_host,
    migrations.source_node AS source_node,
    migrations.dest_node AS target_node,
    COALESCE(migrations.user_id, 'unknown') AS user_id,
    COALESCE(migrations.project_id, 'unknown') AS project_id,
    migrations.migration_type AS type,
    CAST(EXTRACT(EPOCH FROM (migrations.created_at::timestamp)) AS BIGINT) AS time
FROM openstack_migrations AS migrations
LEFT JOIN durations ON migrations.uuid = durations.migration_uuid;
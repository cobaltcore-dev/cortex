WITH host_az AS (
    SELECT
        a.compute_host,
        -- Get the first non-NULL availability_zone for each host, or NULL if none
        (SELECT az FROM unnest(array_agg(a.availability_zone)) az WHERE az IS NOT NULL LIMIT 1) AS availability_zone
    FROM openstack_aggregates_v2 a
    GROUP BY a.compute_host
)
SELECT
    ht.service_host AS compute_host,
    haz.availability_zone AS availability_zone
FROM openstack_hypervisors ht
LEFT JOIN host_az haz ON ht.service_host = haz.compute_host;
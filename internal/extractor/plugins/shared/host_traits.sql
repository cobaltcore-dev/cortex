-- The openstack_hypervisors table contains a uuid (field id) and the
-- field service_host. The openstack_resource_provider_traits table
-- contains a resource_provider_uuid which corresponds to the hypervisor
-- id. We want to combine all traits (field name) of a service_host like
-- this: "TRAIT_1,TRAIT_2,TRAIT_3" in one single string.
SELECT
    h.service_host AS compute_host,
    STRING_AGG(t.name, ',') AS traits
FROM openstack_hypervisors AS h
JOIN openstack_resource_provider_traits AS t
    ON h.id = t.resource_provider_uuid
GROUP BY h.service_host
ORDER BY h.service_host;
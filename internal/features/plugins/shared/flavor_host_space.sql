WITH flavor_host_space AS (
    SELECT
        flavors.id AS flavor_id,
        hypervisors.service_host AS compute_host,
        hypervisors.free_ram_mb - flavors.ram AS ram_left_mb,
        hypervisors.vcpus - hypervisors.vcpus_used - flavors.vcpus AS vcpus_left,
        hypervisors.free_disk_gb - flavors.disk AS disk_left_gb
    FROM openstack_flavors AS flavors
    CROSS JOIN openstack_hypervisors AS hypervisors
)
SELECT * FROM flavor_host_space;
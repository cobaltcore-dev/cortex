-- First get all instance commitments and map them to their flavors to calculate total vCPUs, RAM, and Disk
-- The name of the flavor is extracted from the resource_name field after the prefix 'instances_'
-- Then sum up instance commitments resources of these flavors per project
WITH instance_commitments AS (
    SELECT
        c.project_id,
        c.availability_zone,
        SUM(c.amount) as total_instances,
        SUM(c.amount * f.vcpus) as total_committed_vcpus,
        SUM(c.amount * f.ram) as total_committed_ram_mb,
        SUM(c.amount * f.disk) as total_committed_disk_gb,
        SUM(CASE WHEN f.name IS NULL THEN c.amount ELSE 0 END) as unresolved_instance_commitments
    FROM openstack_limes_commitments_v2 c
    LEFT JOIN openstack_flavors_v2 f ON f.name = REPLACE(c.resource_name, 'instances_', '')
    WHERE c.service_type = 'compute'
      AND c.resource_name LIKE 'instances_%'
      AND c.status = 'confirmed' -- Might need to change that wen 'guaranteed' is supported
    GROUP BY c.project_id, c.availability_zone
),
-- Get all bare resource commitments (cores and ram) per project
-- These are commitments not tied to specific instance flavors
-- Sum them up per project
bare_commitments AS (
    SELECT
        project_id,
        availability_zone,
        SUM(CASE WHEN resource_name = 'cores' THEN amount ELSE 0 END) as total_committed_vcpus,
        SUM(CASE WHEN resource_name = 'ram' THEN amount ELSE 0 END) as total_committed_ram_mb
    FROM openstack_limes_commitments_v2
    WHERE service_type = 'compute' AND resource_name IN ('cores', 'ram') AND status = 'confirmed'
    GROUP BY project_id, availability_zone
)
SELECT
    COALESCE(ic.project_id, dc.project_id) as project_id,
    COALESCE(ic.availability_zone, dc.availability_zone) as availability_zone,
    COALESCE(ic.total_instances, 0) as total_instance_commitments,
    COALESCE(ic.unresolved_instance_commitments, 0) as unresolved_instance_commitments,
    COALESCE(ic.total_committed_vcpus, 0) + COALESCE(dc.total_committed_vcpus, 0) as total_committed_vcpus,
    COALESCE(ic.total_committed_ram_mb, 0) + COALESCE(dc.total_committed_ram_mb, 0) as total_committed_ram_mb,
    COALESCE(ic.total_committed_disk_gb, 0) as total_committed_disk_gb
FROM instance_commitments ic
FULL OUTER JOIN bare_commitments dc ON ic.project_id = dc.project_id AND ic.availability_zone = dc.availability_zone;
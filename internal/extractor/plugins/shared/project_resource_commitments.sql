-- First get all instance commitments and map them to their flavors to calculate total vCPUs, RAM, and Disk
-- The name of the flavor is extracted from the resource_name field after the prefix 'instances_'
-- Then sum up instance commitments resources of these flavors per project
WITH instance_commitments AS (
    SELECT
        c.project_id,
        SUM(c.amount) as total_instances,
        SUM(c.amount * f.vcpus) as total_committed_vcpus,
        SUM(c.amount * f.ram) as total_committed_ram_mb,
        SUM(c.amount * f.disk) as total_committed_disk_gb
    FROM openstack_limes_commitments_v2 c
    LEFT JOIN openstack_flavors_v2 f ON f.name = REPLACE(c.resource_name, 'instances_', '')
    WHERE c.service_type = 'compute'
      AND c.resource_name LIKE 'instances_%'
    GROUP BY c.project_id
),
-- Get all bare resource commitments (cores and ram) per project
-- These are commitments not tied to specific instance flavors
-- Sum them up per project
bare_commitments AS (
    SELECT
        project_id,
        SUM(CASE WHEN resource_name = 'cores' THEN amount ELSE 0 END) as total_committed_vcpus,
        SUM(CASE WHEN resource_name = 'ram' THEN amount ELSE 0 END) as total_committed_ram_mb
    FROM openstack_limes_commitments_v2
    WHERE service_type = 'compute' AND resource_name IN ('cores', 'ram')
    GROUP BY project_id
),
-- Sum up instance commitments and bare commitments per project
combined_commitments AS (
    SELECT
        COALESCE(ic.project_id, dc.project_id) as project_id,
        COALESCE(ic.total_instances, 0) as total_instance_commitments,
        COALESCE(ic.total_committed_vcpus, 0) + COALESCE(dc.total_committed_vcpus, 0) as total_committed_vcpus,
        COALESCE(ic.total_committed_ram_mb, 0) + COALESCE(dc.total_committed_ram_mb, 0) as total_committed_ram_mb,
        COALESCE(ic.total_committed_disk_gb, 0) as total_committed_disk_gb
    FROM instance_commitments ic
    FULL OUTER JOIN bare_commitments dc ON ic.project_id = dc.project_id
)
-- Join with projects to ensure all projects are listed, even those without commitments
SELECT
    p.id as project_id,
    COALESCE(cc.total_instance_commitments, 0) as total_instance_commitments,
    COALESCE(cc.total_committed_vcpus, 0) as total_committed_vcpus,
    COALESCE(cc.total_committed_ram_mb, 0) as total_committed_ram_mb,
    COALESCE(cc.total_committed_disk_gb, 0) as total_committed_disk_gb
FROM openstack_projects p
LEFT JOIN combined_commitments cc ON p.id = cc.project_id
ORDER BY p.id;

SELECT
        c.project_id,
        c.amount as total_instances,
        f.vcpus as total_committed_vcpus,
        f.ram as total_committed_ram_mb,
        f.disk as total_committed_disk_gb
    FROM openstack_limes_commitments_v2 c
    LEFT JOIN openstack_flavors_v2 f ON f.name = REPLACE(c.resource_name, 'instances_', '')
    WHERE c.service_type = 'compute'
      AND c.resource_name LIKE 'instances_%'
      AND c.project_id = '5a39c2f52d57455ead9834a915cba9a4'
    ORDER BY c.project_id;
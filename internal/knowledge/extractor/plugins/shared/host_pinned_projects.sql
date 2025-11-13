-- Extract host-to-project pinning relationships from Nova aggregates
-- This query identifies which projects are restricted to specific compute hosts
-- and which hosts are unrestricted (accept any project)

-- CTE to identify all compute hosts that have project restrictions
-- These are hosts that belong to aggregates with 'filter_tenant_id' metadata
WITH restricted_hosts AS (
    SELECT DISTINCT compute_host
    FROM openstack_aggregates_v2
    WHERE metadata::jsonb ? 'filter_tenant_id'  -- Check if metadata contains the filter key
      AND compute_host IS NOT NULL
)

-- Main query combining restricted and unrestricted hosts
SELECT DISTINCT
    aggregate_uuid,
    aggregate_name,
    compute_host,
    project_id,
    domain_id,
    label  -- Formatted label showing "project_name (domain_name)"
FROM (
    -- Part 1: Extract hosts with specific project restrictions
    -- Parse comma-separated tenant IDs from aggregate metadata
    SELECT
        a.uuid as aggregate_uuid,
        a.name as aggregate_name,
        a.compute_host,
        trim(tenant_value) as project_id,  -- Clean whitespace from parsed tenant IDs
        p.domain_id,
        -- Create label only when project_id is not NULL, otherwise NULL
        CASE
            WHEN trim(tenant_value) IS NOT NULL AND trim(tenant_value) != '' THEN
                COALESCE(p.name, 'unknown') || ' (' || COALESCE(d.name, 'unknown') || ')'
            ELSE NULL
        END AS label
    FROM openstack_aggregates_v2 a
    CROSS JOIN unnest(string_to_array(a.metadata::jsonb ->> 'filter_tenant_id', ',')) AS tenant_value
    -- Join with projects table to get project details and domain information
    LEFT JOIN openstack_projects p ON trim(tenant_value) = p.id
    -- Join with domains table to get domain names for the label
    LEFT JOIN openstack_domains d ON p.domain_id = d.id
    WHERE a.metadata::jsonb ->> 'filter_tenant_id' IS NOT NULL
      AND a.metadata::jsonb ->> 'filter_tenant_id' NOT IN ('', '[]')  -- Exclude empty values
      AND trim(tenant_value) != ''  -- Exclude empty tenant values after trimming

    UNION ALL

    -- Part 2: Find unrestricted hosts (no project limitations)
    -- These hosts can accept VMs from any project
    SELECT
        NULL as aggregate_uuid,  -- No aggregate association for unrestricted hosts
        NULL as aggregate_name,
        h.service_host as compute_host,
        NULL as project_id,  -- No specific project restriction
        NULL as domain_id,   -- No domain for unrestricted hosts
        NULL as label        -- No label for unrestricted hosts
    FROM openstack_hypervisors h
    WHERE h.hypervisor_type != 'ironic'  -- Exclude baremetal hypervisors
      AND h.service_host IS NOT NULL
      AND h.service_host NOT IN (SELECT compute_host FROM restricted_hosts)  -- Only unrestricted hosts
) AS combined_results

ORDER BY compute_host, aggregate_uuid NULLS LAST;
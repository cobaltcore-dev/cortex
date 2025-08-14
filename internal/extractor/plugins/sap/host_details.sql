WITH host_traits AS (
    SELECT
        h.service_host,
        h.hypervisor_type,
        h.running_vms,
        h.state,
        h.status,
        h.service_disabled_reason,
        STRING_AGG(t.name, ',') AS traits
    FROM openstack_hypervisors h
    LEFT JOIN openstack_resource_provider_traits t
        ON h.id = t.resource_provider_uuid
    GROUP BY h.service_host, h.hypervisor_type, h.running_vms, h.state, h.status, h.service_disabled_reason
)
SELECT
    ht.service_host AS compute_host,
    ht.running_vms AS running_vms,
    COALESCE(haz.availability_zone, 'unknown') AS availability_zone,
    -- CPU Architecture
    CASE
        WHEN ht.traits LIKE '%CUSTOM_HW_SAPPHIRE_RAPIDS%' THEN 'sapphire-rapids'
        WHEN ht.traits LIKE '%CUSTOM_NUMASIZE_C48_M729%' THEN 'cascade-lake'
        ELSE 'unknown'
    END AS cpu_architecture,
    ht.hypervisor_type,
    CASE
        WHEN ht.service_host LIKE 'nova-compute-%' THEN 'vmware'
        WHEN ht.service_host LIKE 'node%-bb%' THEN 'kvm'
        ELSE 'unknown'
    END AS hypervisor_family,
    CASE
        WHEN ht.traits LIKE '%CUSTOM_HANA_EXCLUSIVE_HOST%' THEN 'hana'
        ELSE 'general-purpose'
    END AS workload_type,
    CASE
        WHEN ht.traits LIKE '%CUSTOM_DECOMMISSIONING%' THEN false
        WHEN ht.traits LIKE '%CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED%' THEN false
        WHEN ht.traits LIKE '%COMPUTE_STATUS_DISABLED%' THEN false
        WHEN ht.status != 'enabled' THEN false
        WHEN ht.state != 'up' THEN false
        ELSE true
    END AS enabled,
    CASE
        WHEN ht.traits LIKE '%CUSTOM_DECOMMISSIONING%' THEN 'decommissioning'
        WHEN ht.traits LIKE '%CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED%' THEN 'external customer'
        WHEN ht.traits LIKE '%COMPUTE_STATUS_DISABLED%' THEN 'compute status disabled trait (' || COALESCE(ht.service_disabled_reason, '--') || ')'
        WHEN ht.status != 'enabled' THEN 'status: not enabled (' || COALESCE(ht.service_disabled_reason, '--') || ')'
        WHEN ht.state != 'up' THEN 'state: not up (' || COALESCE(ht.service_disabled_reason, '-') || ')'
        ELSE NULL
    END AS disabled_reason
FROM host_traits ht
LEFT JOIN feature_host_az haz ON ht.service_host = haz.compute_host;
SELECT
    os.id AS instance_uuid,
    os.os_ext_srv_attr_host AS host,
    MAX(value) AS max_steal_time_pct
FROM kvm_libvirt_domain_metrics kvm
JOIN openstack_servers os ON os.os_ext_srv_attr_instance_name = kvm.domain
WHERE kvm.name = 'kvm_libvirt_domain_steal_pct' AND os.id IS NOT NULL
GROUP BY os.os_ext_srv_attr_host, os.id;
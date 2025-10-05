SELECT * FROM (
    SELECT *,
           ROW_NUMBER() OVER (PARTITION BY os.id ORDER BY kvm.timestamp DESC) as rn
    FROM kvm_cpu_steal_time_metrics kvm
    LEFT JOIN openstack_servers os ON os.os_ext_srv_attr_instance_name = kvm.domain
) ranked
WHERE rn = 1 AND os.id IS NOT NULL;
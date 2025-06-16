-- Migration: Add allocatable columns to feature_host_utilization
ALTER TABLE feature_host_utilization
    ADD COLUMN total_memory_allocatable_mb FLOAT;
ALTER TABLE feature_host_utilization
    ADD COLUMN total_vcpus_allocatable FLOAT;
ALTER TABLE feature_host_utilization
    ADD COLUMN total_disk_allocatable_gb FLOAT;

-- Migration: Add allocatable columns to feature_host_utilization
ALTER TABLE IF EXISTS feature_host_utilization
    ADD COLUMN IF NOT EXISTS total_memory_allocatable_mb FLOAT;
ALTER TABLE IF EXISTS feature_host_utilization
    ADD COLUMN IF NOT EXISTS total_vcpus_allocatable FLOAT;
ALTER TABLE IF EXISTS feature_host_utilization
    ADD COLUMN IF NOT EXISTS total_disk_allocatable_gb FLOAT;

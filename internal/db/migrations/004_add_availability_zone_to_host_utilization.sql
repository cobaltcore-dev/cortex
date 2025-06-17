-- Migration: Add availability_zone to host_utilization
ALTER TABLE feature_host_utilization
    ADD COLUMN availability_zone VARCHAR(255);

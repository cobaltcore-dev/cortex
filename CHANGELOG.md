# Changelog

## v0.0.36

- Removed the `prometheuscommunity/postgres-exporter` sidecar container for `cortex-postgres` due to security vulnerabilities. Metrics of the exporter are no longer in use as part of the migration away from PostgreSQL.
- Updated `cortex-postgres` to version `v0.5.10` which includes the above change and other minor updates to the postgres image.

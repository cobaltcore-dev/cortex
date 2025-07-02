#!/bin/sh

set -e

# Parse the cmd line args.
pg_password="$1"

# Print out the number of rows in each table in the PostgreSQL database.
# This is useful for verifying that the database is not empty before the upgrade.
echo "The following data should be preserved during the upgrade:"
PGPASSWORD=$pg_password psql -U postgres -d postgres -c "
SELECT table_schema, table_name
FROM information_schema.tables
WHERE
  table_type = 'BASE TABLE'
  AND table_schema NOT IN ('pg_catalog', 'information_schema')
GROUP BY table_schema, table_name
ORDER BY table_schema, table_name;"

# Create a PostgreSQL dump of the database and save it to the persistent directory.
PGPASSWORD=$pg_password pg_dumpall -U postgres \
  | gzip > /bitnami/postgresql/pre-upgrade-dump.gz
# Rename the current PostgreSQL data directory to ensure it is preserved
# in case of issues after the upgrade.
backup_dir="/bitnami/postgresql/data.bak.$(date +%Y%m%d%H%M%S)"
if [ -d /bitnami/postgresql/data ]; then
  echo "Backing up current PostgreSQL data directory to $backup_dir"
  mv /bitnami/postgresql/data "$backup_dir"
fi

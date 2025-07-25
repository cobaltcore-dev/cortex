#!/bin/sh

set -e

# Parse the cmd line args.
pg_password="$1"

# Wait until postgres is available.
until PGPASSWORD=$pg_password psql -U postgres -c '\q'; do
  echo "PostgreSQL is not yet available. Retrying in 1 second..."
  sleep 1
done

# Restore from the backup created during pre-upgrade.
gunzip -c /bitnami/postgresql/pre-upgrade-dump.gz | PGPASSWORD=$pg_password psql -U postgres

echo "The following data was loaded during the upgrade:"
PGPASSWORD=$pg_password psql -U postgres -d postgres -c "
SELECT table_schema, table_name
FROM information_schema.tables
WHERE
  table_type = 'BASE TABLE'
  AND table_schema NOT IN ('pg_catalog', 'information_schema')
GROUP BY table_schema, table_name
ORDER BY table_schema, table_name;"

# Database Migrations

This directory contains SQL migrations for all CLI-things utilities. Migrations are automatically applied when using tools that require database storage.

## Migration Files

### 20251104_0001_publicip.sql
**Utility**: `publicip`
**Tables**:
- `public.public_ip_history` - Tracks public IP addresses over time
- `public.dns_targets` - DNS target configurations
- `public.dns_history` - DNS record changes over time

### 20251104_0002_cloudflare_backup.sql
**Utility**: `cloudflare-backup`
**Tables**:
- `public.cloudflare_accounts` - Cloudflare account information
- `public.cloudflare_zones` - DNS zone data
- `public.cloudflare_dns_records` - Individual DNS records
- `public.cloudflare_backup_runs` - Backup execution logs

### 20251104_0003_internal_ip_history.sql
**Utility**: `internalip`
**Tables**:
- `public.internal_ip_history` - Internal IP address tracking for devices
- `public.current_internal_ips` - View of currently active IPs

## Migration System

The migration system uses the `dbconf` package which:

1. **Automatically discovers** migration files in this directory
2. **Tracks applied migrations** in `public._migrations` table
3. **Applies migrations in order** based on filename sorting
4. **Supports rollback** by manually managing migration states

## Configuration

Migrations are applied using the same configuration as other utilities:

```bash
# Environment variables
export DB_HOST=localhost
export DB_PORT=5432
export DB_NAME=yourdbname
export DB_USER=youruser
export DB_PASSWORD=yourpassword

# Or via config.ini
[default]
DB_HOST=localhost
DB_PORT=5432
DB_NAME=yourdbname
DB_USER=youruser
DB_PASSWORD=yourpassword
DB_MIGRATIONS_DIR=./migrations
```

## Manual Migration Application

If you need to apply migrations manually:

```bash
# Using dbtool
go run -tags dbtool dbtool.go --help
# (dbtool automatically applies migrations when needed)

# Or via psql
psql -h localhost -U youruser -d yourdbname -f migrations/20251104_0003_internal_ip_history.sql
```

## Adding New Migrations

When adding a new utility that requires database storage:

1. **Create a new migration file** with timestamp prefix: `YYYYMMDD_####_utility_name.sql`
2. **Follow the naming convention** to ensure proper ordering
3. **Include CREATE TABLE IF NOT EXISTS** statements for safety
4. **Add appropriate indexes** for performance
5. **Update this README** with documentation

### Migration File Template

```sql
-- Utility Name: your-utility
-- Description: Brief description of what this migration does
-- Dependencies: List any previous migrations this depends on

CREATE TABLE IF NOT EXISTS public.your_table (
    id SERIAL PRIMARY KEY,
    -- Your columns here
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_your_table_column ON public.your_table(column);

-- Views for common queries
CREATE OR REPLACE VIEW public.your_view AS
SELECT * FROM public.your_table WHERE updated_at IS NULL;
```

## Database Views

Several utilities create helpful views:

- `public.current_internal_ips` - Currently active internal IPs
- Add more views as needed for common query patterns

## Security Considerations

- All tables are created in the `public` schema
- Consider setting appropriate GRANT permissions in production
- Migration files should not contain sensitive data
- Use parameterized queries in application code

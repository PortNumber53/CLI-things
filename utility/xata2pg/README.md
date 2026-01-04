# xata2pg

Export Xata Postgres databases (given as Postgres DSNs) and recreate them into a standard PostgreSQL server.

## Input file format

One DSN per line. Blank lines and `# comments` are ignored.

Example:

```
postgresql://rr8013:<YOUR_API_KEY>@us-west-2.sql.xata.sh/dbagentthing:main?sslmode=require
postgresql://rr8013:<YOUR_API_KEY>@us-west-2.sql.xata.sh/anotherdb:main?sslmode=require
```

## Target Postgres configuration (.env)

Either provide:

- `POSTGRESQL_DATABASE_URL` (recommended), **or**
- the discrete values:
  - `POSTGRESQL_HOST`
  - `POSTGRESQL_PORT`
  - `POSTGRESQL_USER`
  - `POSTGRESQL_PASSWORD`
  - `POSTGRESQL_SSLMODE` (optional; defaults to `disable`)

## Usage

```bash
go run ./utility/xata2pg --input /path/to/xata-dsns.txt

# dump files are written under ./xata2pg-dumps/ by default
```

### Common flags

- `--dump-dir ./xata2pg-dumps` - where to write `.sql` dumps
- `--include-branch` (default true) - include the `:branch` suffix in the target DB name (converted to `__branch`)
- `--drop-existing` - drop target DBs before recreating them
- `--schema auto|pg_dump|introspect` - schema strategy (auto tries pg_dump pre/post and falls back to introspection)
- `--data copy|none` - data strategy (copy streams per-table data via `psql COPY`; avoids `pg_dump` for data)

## Troubleshooting

### `pg_dump: error: role with OID ... does not exist`

This can happen when the source Postgres endpoint has objects owned by (or referencing privileges for) a role that is **not visible** via `pg_roles`.

`xata2pg` will detect this error and run a few diagnostic catalog queries against the source DB to show which objects reference a missing role OID, so you can share that output with Xata support.

If Xata support isn't an option (deprecated endpoint), prefer:

- `--schema introspect` (generate CREATE TABLE/constraints/indexes from catalogs), and
- `--data copy` (stream data table-by-table via `COPY ... TO STDOUT | COPY ... FROM STDIN`)



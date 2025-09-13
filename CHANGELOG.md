## 2025-09-12

### Added

- Global verbose flag for `dbtool` (`-v` / `--verbose`). When enabled:
  - Prints which `.env` files were discovered and applied, in order, in `dbtool.go`.
  - Shows how `DBTOOL_CONFIG_FILE` is resolved when relative to a `.env` file.
  - Logs which `config.ini` path is used and when it is read inside `utility/dbtool/database.go`.
  - `usage()` now includes a "Global flags" section describing verbose mode.

### Fixed

- `config.ini` parser now also accepts top-level `key=value` entries (outside of any section), treating them as `[default]` values. This matches common minimal INI styles.
- Verbose mode now reads `DBTOOL_VERBOSE` dynamically at call time to ensure `-v/--verbose` set by the CLI is honored during library calls.

### Changed

- Verbose connection diagnostics now print which host, port, database, and username are used when connecting (DSN or discrete fields), without exposing passwords.

# Changelog

All notable changes to this project will be documented in this file.

## 2025-09-07

### Added

- Support for `DATABASE_URL` DSN in `dbtool.go`. If set to `postgres://` or `postgresql://`, it takes precedence over discrete `DB_*` fields.
- Environment variable fallback for configuration keys, including `DATABASE_URL`.
- `pg_dump` and `psql` runners now use DSN when available and minimally set environment variables.
- New command: `table list <dbname> [--schema=<schema>]` to list tables from `information_schema.tables`. Excludes system schemas by default.

### Notes

- Xata: If `DATABASE_URL` is set to a HTTPS Xata workspace URL (e.g., `https://<workspace>.xata.sh/db/<db>`), the tool surfaces a helpful error. Use Xata's PostgreSQL-compatible DSN instead (e.g., `postgres://...`).

## 2025-08-29

### Added

- New PostgreSQL database CLI in `dbtool.go` guarded by build tag `dbtool`.
- Commands:
  - `database list` — list non-template databases.
  - `database dump <dbname> <filepath> [--structure-only]` — dump DB via `pg_dump`.
  - `database import <dbname> <filepath> [--overwrite]` — import SQL via `psql` with optional reset.
  - `database reset <dbname> [--noconfirm]` — drop and recreate `public` schema.
  - `query <dbname> --query="<sql>" [--json]` — run SQL with optional JSON output.
  - Aliases: `db`, `ls`, `export`, `load`, `wipe`, `q`.
  - Help system: `help` for summary and `help <command> [subcommand]` for detailed usage.

### Notes

- Run with build tag to avoid conflicting `main()`:
  - `go run -tags dbtool dbtool.go <command> ...`
  - `go build -tags dbtool -o dbtool dbtool.go`

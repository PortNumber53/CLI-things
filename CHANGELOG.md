# Changelog

All notable changes to this project will be documented in this file.

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

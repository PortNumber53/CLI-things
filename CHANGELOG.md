## 2025-11-02

### Added

- **Version flag**: Added `--version` flag to display version information for debugging purposes. Usage: `dbtool --version`

### Changed

- **Environment variable override priority**: Command-line environment variables now take precedence over values from `.env` files and `config.ini`. This allows direct overrides like `DATABASE_URL="..." dbtool db list` without modifying configuration files.
- Modified `applyEnvFile()` in both `dbtool.go` and `utility/dbconf/dbconf.go` to check if an environment variable already exists before setting it from `.env` files using `os.LookupEnv()`.

### Documentation

- Updated README.md and .windsurf_plan.md to document the configuration priority: command-line env vars > .env files > config.ini
- Added examples showing how to override configuration values via command-line environment variables.
- Added Global Flags section to README documenting `--verbose` and `--version` flags.

## 2025-09-13

### Fixed

- `dbtool query` could fail on some providers with a driver-level protocol error like "unexpected ReadyForQuery" when executing non-row statements (e.g., `CREATE EXTENSION`). The tool now falls back to invoking `psql -c` for such statements when this specific error is encountered.
- For Xata Postgres endpoints (DSNs whose host contains `xata.sh`), `db.Ping()` can fail even when subsequent queries would succeed. We now skip `Ping()` during connect for such DSNs and let operations surface errors, restoring compatibility with Xata.

### Added

- New helper `RunPSQLInline()` in `utility/dbtool/database.go` to execute a single SQL statement via `psql -c`, used as a safe fallback path.

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

## 2025-10-24

### Added

- New shared module `utility/dbconf/` that mirrors `dbtool`'s `.env` loading and config resolution so other tools reuse the same DB credentials. Exposes `DefaultDBName()`, `ConnectDB()`, `ConnectDBAs()`.
- New CLI `utility/publicip/` to fetch the current public IP with parallel provider queries and optional storage to PostgreSQL (`--store`), preserving a compact history via `first_use_at` and nullable `last_use_at`.
- Cloudflare sync in `publicip` via `--sync-cf` (deprecated alias `--check-cf`): reads the current stored IP and updates A records for `brain.portnumber53.com`, `*.stage.portnumber53.com`, and `*.dev.portnumber53.com`. Uses `CLOUDFLARE_API_KEY` API token.

### Changed

- `publicip`: Cloudflare operations now use a dedicated timeout flag `--cf-timeout` (default 20s) and include simple retries with exponential backoff to reduce transient timeouts.
- Renamed `--check-cf` to `--sync-cf` for clarity; kept `--check-cf` as a deprecated alias for backward compatibility.

### Ops

- Added `systemd/publicip.service` and `systemd/publicip.timer` to schedule `publicip` to run `--store --sync-cf` every 15 minutes (with boot delay). Includes repo `WorkingDirectory` and uses the tool's own `.env`/config loading.

### CI/CD

- Added `Jenkinsfile` to build `utility/publicip` and deploy the binary to local server `crash` as user `grimlock` via passwordless SSH. The pipeline also attempts to reload systemd and start the `publicip.service` if available on the target.

### Fixed

- `Jenkinsfile`: replace unsupported Declarative option `ansiColor('xterm')` with `wrap([$class: 'AnsiColorBuildWrapper', colorMapName: 'xterm'])` for compatibility.

## 2025-10-25

### Added

- `utility/publicip`: DNS targets tracking and history
  - New tables auto-created: `public.dns_targets(fqdn text PRIMARY KEY, enabled boolean)`, `public.dns_history(fqdn text, ip inet, first_use_at, last_use_at, PRIMARY KEY(fqdn, ip))`.
  - Flags:
    - `--init-dns-targets`: seeds default targets derived from `--cf-host` zone (e.g., `brain.<zone>`, `*.stage.<zone>`, `*.dev.<zone>`)
    - `--collect-cf`: pulls current CF A records for enabled targets and writes to `dns_history`
    - `--sync-cf`: now reads targets from DB and compares DB-recorded DNS IP vs current stored public IP; updates CF only when different
    - `--force`: forces CF update regardless of DB state

### Ops

- New systemd units/timers:
  - `publicip-collect.service/.timer` — hourly collection of Cloudflare DNS into DB
  - `publicip-sync.service/.timer` — every-minute sync of DNS to current public IP via DB targets
- `publicip.service` now loads environment from `/etc/cli-things/publicip.conf`.

### Changed

- Increased Cloudflare operation resilience:
  - Added retries with exponential backoff to zone lookup and DNS record fetch.
  - Increased systemd `publicip.service` Cloudflare timeout to `--cf-timeout 60s`.

### CI/CD

- `Jenkinsfile` updated to deploy new units/timers and to seed `/etc/cli-things/publicip.conf` from a sample on first install.

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

# CLI-things

tap tap, is this thing working?
[![Go Tests](https://github.com/PortNumber53/CLI-things/actions/workflows/go-tests.yml/badge.svg)](https://github.com/PortNumber53/CLI-things/actions/workflows/go-tests.yml)

## env-anonymizer

A CLI utility for generating `.env.example` files while preserving comments and file structure.

### Features
- Reads from `.env` and optional `.env.local` files
- Anonymizes sensitive environment variable values
- Preserves comments and blank lines from the original files
- Supports custom input and output file paths

### Usage
```bash
go run env-anonymizer.go [flags]
```

#### Flags
- `-env`: Path to the main .env file (default: `.env`)
- `-local`: Path to the local .env override file (default: `.env.local`)
- `-output`: Path for the generated .env.example file (default: `.env.example`)

### Example
Given a `.env` file:
```
DATABASE_URL=postgresql://user:password@localhost/mydb
API_KEY=secret123
```

The generated `.env.example` will look like:
```
DATABASE_URL=<DATABASE_URL_VALUE>
API_KEY=<API_KEY_VALUE>
```

This helps developers share environment configuration templates without exposing sensitive information.

## dbtool (PostgreSQL management)

A CLI utility to list, dump, import, reset PostgreSQL databases, and run ad-hoc queries.

> Note: `dbtool.go` uses build tag `dbtool` to avoid conflicting with other mains.

### Build/Run

```bash
# Run directly
go run -tags dbtool dbtool.go <command> ...

# Build a binary
go build -tags dbtool -o dbtool dbtool.go
```

### Configuration

dbtool reads connection settings from `~/.config/<current-folder-name>/config.ini` under the `[default]` section.

Example `config.ini`:

```
[default]
DB_HOST=localhost
DB_PORT=5432
DB_NAME=yourdbname
DB_USER=youruser
DB_PASSWORD=yourpassword
DB_SSLMODE=disable
DB_MIGRATIONS_DIR=/path/to/migrations
```

Notes:
- `DB_PORT` defaults to `5432` if not set.
- `DB_SSLMODE` defaults to `disable` if not set (valid values: `disable`, `require`, `verify-ca`, `verify-full`).

### Commands & Aliases

- `database list` (aliases: `db list`, `db ls`)
- `database dump <dbname> <filepath> [--structure-only]` (aliases: `db dump`, `db export`)
- `database import <dbname> <filepath> [--overwrite]` (aliases: `db import`, `db load`)
- `database reset <dbname> [--noconfirm]` (aliases: `db reset`, `db wipe`)
- `query <dbname> --query="<sql>" [--json]` (alias: `q`)
- `help [command] [subcommand]` (alias: `h`, `-h`, `--help`)

### Examples

```bash
# Show summary help
go run -tags dbtool dbtool.go help

# Detailed help for a command or subcommand
go run -tags dbtool dbtool.go help db
go run -tags dbtool dbtool.go help database dump

# List databases
go run -tags dbtool dbtool.go db ls

# Dump schema only
go run -tags dbtool dbtool.go db export mydb /tmp/mydb.sql --structure-only

# Import with overwrite (reset schema first)
go run -tags dbtool dbtool.go db load mydb /tmp/mydb.sql --overwrite

# Reset database without confirmation
go run -tags dbtool dbtool.go db wipe mydb --noconfirm

# Run a query and output JSON
go run -tags dbtool dbtool.go q mydb --query="SELECT 1 AS one" --json

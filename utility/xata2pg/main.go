package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	_ "github.com/lib/pq"
)

type targetConfig struct {
	DatabaseURL string
	Host        string
	Port        string
	User        string
	Password    string
	SSLMode     string
}

type schemaMode string

const (
	schemaAuto       schemaMode = "auto"
	schemaPgDump     schemaMode = "pg_dump"
	schemaIntrospect schemaMode = "introspect"
)

type dataMode string

const (
	dataNone dataMode = "none"
	dataCopy dataMode = "copy"
)

func main() {
	var (
		inputFile     = flag.String("input", "", "Path to a text file containing Xata Postgres DSNs (one per line)")
		dumpDir       = flag.String("dump-dir", "./xata2pg-dumps", "Directory to write SQL dump files")
		includeBranch = flag.Bool("include-branch", true, "Include :branch in target DB name (as __branch)")
		dropExisting  = flag.Bool("drop-existing", false, "Drop target DBs before recreating them")
		schemaOnly    = flag.Bool("schema-only", false, "DEPRECATED: use --data=none (kept for compatibility)")
		schemaSrc     = flag.String("schema", "auto", "Schema strategy: auto|pg_dump|introspect (auto tries pg_dump pre/post, falls back to introspection)")
		dataSrc       = flag.String("data", "copy", "Data strategy: copy|none (copy streams table data via psql COPY)")
		excludeSchema = flag.String("exclude-schema-regex", "", "Optional regex of schema names to exclude from introspection-based migration")
		verbose       = flag.Bool("v", false, "Verbose logging")
	)
	flag.Parse()

	if *inputFile == "" {
		fmt.Fprintln(os.Stderr, "missing required --input")
		flag.Usage()
		os.Exit(2)
	}

	// Load .env files up the tree (mirrors dbtool behavior).
	_ = loadEnvFromNearestDotEnv(*verbose)

	cfg, err := loadTargetConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "target config error:", err)
		os.Exit(2)
	}

	lines, err := readDSNLines(*inputFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to read input:", err)
		os.Exit(1)
	}
	if len(lines) == 0 {
		fmt.Fprintln(os.Stderr, "no DSNs found in input file")
		os.Exit(2)
	}

	if err := os.MkdirAll(*dumpDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "failed to create dump dir:", err)
		os.Exit(1)
	}

	adminDSN, err := cfg.adminDSN()
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to build admin DSN:", err)
		os.Exit(2)
	}
	adminDB, err := sql.Open("postgres", adminDSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to connect to target postgres:", err)
		os.Exit(1)
	}
	defer adminDB.Close()

	sm := schemaMode(*schemaSrc)
	if sm != schemaAuto && sm != schemaPgDump && sm != schemaIntrospect {
		fmt.Fprintln(os.Stderr, "invalid --schema; must be auto|pg_dump|introspect")
		os.Exit(2)
	}
	dm := dataMode(*dataSrc)
	if dm != dataCopy && dm != dataNone {
		fmt.Fprintln(os.Stderr, "invalid --data; must be copy|none")
		os.Exit(2)
	}
	if *schemaOnly {
		dm = dataNone
	}
	var excludeSchemaRe *regexp.Regexp
	if strings.TrimSpace(*excludeSchema) != "" {
		rx, err := regexp.Compile(*excludeSchema)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid --exclude-schema-regex:", err)
			os.Exit(2)
		}
		excludeSchemaRe = rx
	}

	for _, src := range lines {
		srcInfo, err := parseSourceDSN(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip invalid DSN %q: %v\n", redactDSN(src), err)
			continue
		}

		targetDBName := buildTargetDBName(srcInfo.db, srcInfo.branch, *includeBranch)

		if *verbose {
			fmt.Fprintf(os.Stderr, "source: %s -> target db: %s\n", redactDSN(src), targetDBName)
			fmt.Fprintf(os.Stderr, "dump dir: %s\n", *dumpDir)
		}

		if err := ensureDatabase(adminDB, targetDBName, *dropExisting, *verbose); err != nil {
			fmt.Fprintf(os.Stderr, "failed to ensure database %q: %v\n", targetDBName, err)
			os.Exit(1)
		}

		targetDSN, err := cfg.dsnFor(targetDBName)
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to build target DSN:", err)
			os.Exit(2)
		}

		// 1) Apply schema (pre-data), 2) copy data table-by-table, 3) apply schema (post-data).
		if err := migrateOne(src, targetDSN, filepath.Join(*dumpDir, targetDBName), sm, dm, excludeSchemaRe, *verbose); err != nil {
			fmt.Fprintf(os.Stderr, "migrate failed for %s -> %s: %v\n", srcInfo.fullName(), targetDBName, err)
			os.Exit(1)
		}

		fmt.Printf("ok: %s -> %s\n", srcInfo.fullName(), targetDBName)
	}
}

func migrateOne(sourceDSN, targetDSN, dumpBasePath string, sm schemaMode, dm dataMode, excludeSchemaRe *regexp.Regexp, verbose bool) error {
	// dumpBasePath is a prefix; we write <prefix>.pre.sql and <prefix>.post.sql
	prePath := dumpBasePath + ".pre.sql"
	postPath := dumpBasePath + ".post.sql"

	// Schema phase (pre/post)
	switch sm {
	case schemaPgDump, schemaAuto:
		if verbose {
			fmt.Fprintf(os.Stderr, "schema(pg_dump): writing %s and %s\n", prePath, postPath)
		}
		if err := runPgDumpSection(sourceDSN, prePath, "pre-data", verbose); err != nil {
			maybeDiagnosePgDumpError(sourceDSN, err, verbose)
			if sm == schemaPgDump {
				return fmt.Errorf("pg_dump pre-data failed: %w", err)
			}
			if verbose {
				fmt.Fprintln(os.Stderr, "schema(pg_dump) failed; falling back to introspection")
			}
			if err2 := writeIntrospectedSchema(sourceDSN, prePath, postPath, excludeSchemaRe, verbose); err2 != nil {
				return fmt.Errorf("schema introspection fallback failed: %w (original pg_dump error: %v)", err2, err)
			}
			break
		}
		if err := runPgDumpSection(sourceDSN, postPath, "post-data", verbose); err != nil {
			maybeDiagnosePgDumpError(sourceDSN, err, verbose)
			if sm == schemaPgDump {
				return fmt.Errorf("pg_dump post-data failed: %w", err)
			}
			if verbose {
				fmt.Fprintln(os.Stderr, "schema(pg_dump post-data) failed; falling back to introspection")
			}
			if err2 := writeIntrospectedSchema(sourceDSN, prePath, postPath, excludeSchemaRe, verbose); err2 != nil {
				return fmt.Errorf("schema introspection fallback failed: %w (original pg_dump error: %v)", err2, err)
			}
		}
	case schemaIntrospect:
		if err := writeIntrospectedSchema(sourceDSN, prePath, postPath, excludeSchemaRe, verbose); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown schema mode %q", sm)
	}

	// Apply pre-data schema
	if err := runPsqlFile(targetDSN, prePath, verbose); err != nil {
		return fmt.Errorf("apply pre-data schema failed: %w", err)
	}

	// Data phase
	if dm == dataCopy {
		if err := copyAllTables(sourceDSN, targetDSN, excludeSchemaRe, verbose); err != nil {
			return fmt.Errorf("data copy failed: %w", err)
		}
	}

	// Apply post-data schema (constraints, indexes, etc)
	if err := runPsqlFile(targetDSN, postPath, verbose); err != nil {
		return fmt.Errorf("apply post-data schema failed: %w", err)
	}
	return nil
}

type sourceInfo struct {
	db     string
	branch string
}

func (s sourceInfo) fullName() string {
	if s.branch == "" {
		return s.db
	}
	return s.db + ":" + s.branch
}

func parseSourceDSN(dsn string) (sourceInfo, error) {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return sourceInfo{}, err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return sourceInfo{}, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if !strings.Contains(u.Host, "xata.sh") {
		return sourceInfo{}, fmt.Errorf("host does not look like Xata (%q)", u.Host)
	}
	rawDB := strings.TrimPrefix(u.Path, "/")
	if rawDB == "" {
		return sourceInfo{}, errors.New("missing database in URL path")
	}
	parts := strings.SplitN(rawDB, ":", 2)
	out := sourceInfo{db: parts[0]}
	if len(parts) == 2 {
		out.branch = parts[1]
	}
	return out, nil
}

func buildTargetDBName(db, branch string, includeBranch bool) string {
	name := db
	if includeBranch && strings.TrimSpace(branch) != "" {
		name = db + "__" + branch
	}
	name = sanitizeIdentifier(name)
	if name == "" {
		return "db_xata"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "db_" + name
	}
	return name
}

var reIdentSafe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func sanitizeIdentifier(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, ":", "__")
	s = reIdentSafe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	return strings.ToLower(s)
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func ensureDatabase(admin *sql.DB, dbname string, dropExisting bool, verbose bool) error {
	if dropExisting {
		if verbose {
			fmt.Fprintf(os.Stderr, "dropping database (if exists): %s\n", dbname)
		}
		// Terminate connections first so DROP DATABASE can succeed.
		_, _ = admin.Exec(
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`,
			dbname,
		)
		if _, err := admin.Exec("DROP DATABASE IF EXISTS " + quoteIdent(dbname)); err != nil {
			return err
		}
	}

	// Create if missing.
	var exists bool
	if err := admin.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`,
		dbname,
	).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if verbose {
			fmt.Fprintf(os.Stderr, "database exists: %s\n", dbname)
		}
		return nil
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "creating database: %s\n", dbname)
	}
	_, err := admin.Exec("CREATE DATABASE " + quoteIdent(dbname))
	return err
}

func runPgDumpSection(sourceDSN, outPath string, section string, verbose bool) error {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return fmt.Errorf("pg_dump not found on PATH")
	}
	// Be conservative about metadata that can reference roles/privileges.
	args := []string{
		"-d", sourceDSN,
		"--no-owner",
		"--no-acl",
		"--no-comments",
		"--no-security-labels",
		"--section", section,
		"--file", outPath,
	}
	// Intentionally no data. These sections contain only schema.
	cmd := exec.Command("pg_dump", args...)
	// Avoid leaking credentials by not echoing command; only show redacted DSN.
	if verbose {
		fmt.Fprintf(os.Stderr, "pg_dump(%s): %s -> %s\n", section, redactDSN(sourceDSN), outPath)
	}
	cmd.Stdout = os.Stdout
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		return pgDumpError{Err: err, Stderr: stderr.String()}
	}
	return nil
}

type pgDumpError struct {
	Err    error
	Stderr string
}

func (e pgDumpError) Error() string {
	// Keep the original error for users who just want the exit status.
	return e.Err.Error()
}

func (e pgDumpError) Unwrap() error { return e.Err }

func runPsqlFile(targetDSN, sqlFile string, verbose bool) error {
	if _, err := exec.LookPath("psql"); err != nil {
		return fmt.Errorf("psql not found on PATH")
	}
	args := []string{"-X", "-q", "-d", targetDSN, "-v", "ON_ERROR_STOP=1", "-f", sqlFile}
	cmd := exec.Command("psql", args...)
	if verbose {
		fmt.Fprintf(os.Stderr, "psql: restoring into %s from %s\n", redactDSN(targetDSN), sqlFile)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyAllTables(sourceDSN, targetDSN string, excludeSchemaRe *regexp.Regexp, verbose bool) error {
	srcDB, err := sql.Open("postgres", sourceDSN)
	if err != nil {
		return err
	}
	defer srcDB.Close()

	tables, err := listBaseTables(srcDB, excludeSchemaRe)
	if err != nil {
		return err
	}
	for _, t := range tables {
		if verbose {
			fmt.Fprintf(os.Stderr, "copy: %s.%s\n", t.schema, t.name)
		}
		if err := streamCopyTable(sourceDSN, targetDSN, t.schema, t.name); err != nil {
			return fmt.Errorf("copy %s.%s failed: %w", t.schema, t.name, err)
		}
	}
	return nil
}

type tableRef struct {
	schema string
	name   string
}

func listBaseTables(db *sql.DB, excludeSchemaRe *regexp.Regexp) ([]tableRef, error) {
	rows, err := db.Query(
		`select table_schema::text, table_name::text
		   from information_schema.tables
		  where table_type = 'BASE TABLE'
		    and table_schema not in ('pg_catalog','information_schema')
		  order by 1,2`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tableRef
	for rows.Next() {
		var s, n string
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		if excludeSchemaRe != nil && excludeSchemaRe.MatchString(s) {
			continue
		}
		out = append(out, tableRef{schema: s, name: n})
	}
	return out, rows.Err()
}

func streamCopyTable(sourceDSN, targetDSN, schema, table string) error {
	if _, err := exec.LookPath("psql"); err != nil {
		return fmt.Errorf("psql not found on PATH")
	}
	fq := quoteIdent(schema) + "." + quoteIdent(table)
	srcSQL := fmt.Sprintf("COPY %s TO STDOUT WITH (FORMAT binary)", fq)
	dstSQL := fmt.Sprintf("COPY %s FROM STDIN WITH (FORMAT binary)", fq)

	srcCmd := exec.Command("psql", "-X", "-q", "-d", sourceDSN, "-v", "ON_ERROR_STOP=1", "-c", srcSQL)
	dstCmd := exec.Command("psql", "-X", "-q", "-d", targetDSN, "-v", "ON_ERROR_STOP=1", "-c", dstSQL)

	// Pipe src stdout into dst stdin
	pr, pw := io.Pipe()
	srcCmd.Stdout = pw
	srcCmd.Stderr = os.Stderr
	dstCmd.Stdin = pr
	dstCmd.Stdout = os.Stdout
	dstCmd.Stderr = os.Stderr

	// Start destination first (ready to read), then start source.
	if err := dstCmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return err
	}
	if err := srcCmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		_ = dstCmd.Wait()
		return err
	}

	srcErr := srcCmd.Wait()
	_ = pw.Close()
	dstErr := dstCmd.Wait()
	_ = pr.Close()

	if srcErr != nil {
		return fmt.Errorf("source COPY failed: %w", srcErr)
	}
	if dstErr != nil {
		return fmt.Errorf("target COPY failed: %w", dstErr)
	}
	return nil
}

func writeIntrospectedSchema(sourceDSN, prePath, postPath string, excludeSchemaRe *regexp.Regexp, verbose bool) error {
	srcDB, err := sql.Open("postgres", sourceDSN)
	if err != nil {
		return err
	}
	defer srcDB.Close()

	tables, err := listBaseTables(srcDB, excludeSchemaRe)
	if err != nil {
		return err
	}
	schemas := map[string]struct{}{}
	for _, t := range tables {
		schemas[t.schema] = struct{}{}
	}

	// Collect sequence references from DEFAULT nextval(...::regclass) so we can CREATE SEQUENCE
	// before table creation (otherwise CREATE TABLE fails).
	type seqRef struct {
		seqSchema string
		seqName   string
		tSchema   string
		tName     string
		colName   string
	}
	var seqRefs []seqRef
	seqSet := map[string]struct{}{} // key = schema.name

	var pre bytes.Buffer
	var post bytes.Buffer
	pre.WriteString("-- generated by xata2pg (introspect)\n")
	post.WriteString("-- generated by xata2pg (introspect)\n")
	for s := range schemas {
		pre.WriteString("CREATE SCHEMA IF NOT EXISTS " + quoteIdent(s) + ";\n")
	}
	pre.WriteString("\n")

	// First pass: scan defaults and gather required sequences.
	for _, t := range tables {
		cols, err := loadTableColumns(srcDB, t.schema, t.name)
		if err != nil {
			return fmt.Errorf("introspect columns %s.%s: %w", t.schema, t.name, err)
		}
		for _, c := range cols {
			schema, seq, ok := extractNextvalSequence(t.schema, c.def)
			if !ok {
				continue
			}
			key := schema + "." + seq
			if _, exists := seqSet[key]; exists {
				continue
			}
			seqSet[key] = struct{}{}
			seqRefs = append(seqRefs, seqRef{
				seqSchema: schema,
				seqName:   seq,
				tSchema:   t.schema,
				tName:     t.name,
				colName:   c.name,
			})
		}
	}

	// Emit sequences before tables so regclass defaults can resolve.
	if len(seqRefs) > 0 {
		pre.WriteString("-- sequences (required by DEFAULT nextval(...::regclass))\n")
		for _, sr := range seqRefs {
			pre.WriteString("CREATE SEQUENCE IF NOT EXISTS " + quoteIdent(sr.seqSchema) + "." + quoteIdent(sr.seqName) + ";\n")
		}
		pre.WriteString("\n")
	}

	for _, t := range tables {
		cols, err := loadTableColumns(srcDB, t.schema, t.name)
		if err != nil {
			return fmt.Errorf("introspect columns %s.%s: %w", t.schema, t.name, err)
		}
		// Ensure unqualified regclass resolution works for this table's schema.
		pre.WriteString("SET search_path = " + quoteIdent(t.schema) + ", public;\n")
		pre.WriteString("CREATE TABLE IF NOT EXISTS " + quoteIdent(t.schema) + "." + quoteIdent(t.name) + " (\n")
		for i, c := range cols {
			line := "  " + quoteIdent(c.name) + " " + c.typ
			// Prefer identity over explicit nextval defaults when present.
			if c.identity != "" {
				if c.identity == "a" {
					line += " GENERATED ALWAYS AS IDENTITY"
				} else if c.identity == "d" {
					line += " GENERATED BY DEFAULT AS IDENTITY"
				}
			} else if c.def != "" {
				line += " DEFAULT " + rewriteNextvalDefault(t.schema, c.def)
			}
			if c.notNull {
				line += " NOT NULL"
			}
			if i < len(cols)-1 {
				line += ","
			}
			line += "\n"
			pre.WriteString(line)
		}
		pre.WriteString(");\n\n")

		// Constraints and indexes in post phase
		if err := appendConstraintsAndIndexes(&post, srcDB, t.schema, t.name); err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "xata2pg: warn: skipping some post-data DDL for %s.%s: %v\n", t.schema, t.name, err)
			}
		}
	}

	// After data copy, advance sequences to max(column) so inserts work.
	if len(seqRefs) > 0 {
		post.WriteString("-- set sequences to max(column) after data copy\n")
		for _, sr := range seqRefs {
			seqLit := regclassLiteral(sr.seqSchema, sr.seqName)
			// Avoid setval(0, ...) for sequences with min_value=1 by using pg_sequence.min_value when the table is empty.
			// If table is non-empty, set to MAX(col) and mark is_called=true so nextval returns MAX+1.
			post.WriteString("WITH seq AS (\n")
			post.WriteString("  SELECT s.min_value\n")
			post.WriteString("    FROM pg_sequence s\n")
			post.WriteString("    JOIN pg_class c ON c.oid = s.seqrelid\n")
			post.WriteString("    JOIN pg_namespace n ON n.oid = c.relnamespace\n")
			post.WriteString("   WHERE n.nspname = '" + strings.ReplaceAll(sr.seqSchema, "'", "''") + "'\n")
			post.WriteString("     AND c.relname = '" + strings.ReplaceAll(sr.seqName, "'", "''") + "'\n")
			post.WriteString("), mx AS (\n")
			post.WriteString("  SELECT MAX(" + quoteIdent(sr.colName) + ") AS m FROM " + quoteIdent(sr.tSchema) + "." + quoteIdent(sr.tName) + "\n")
			post.WriteString(")\n")
			post.WriteString("SELECT pg_catalog.setval(" + seqLit + ",\n")
			post.WriteString("  CASE WHEN mx.m IS NULL THEN seq.min_value ELSE GREATEST(mx.m, seq.min_value) END,\n")
			post.WriteString("  (mx.m IS NOT NULL)\n")
			post.WriteString(") FROM seq, mx;\n")
			post.WriteString(
				"ALTER SEQUENCE " + quoteIdent(sr.seqSchema) + "." + quoteIdent(sr.seqName) +
					" OWNED BY " + quoteIdent(sr.tSchema) + "." + quoteIdent(sr.tName) + "." + quoteIdent(sr.colName) + ";\n",
			)
		}
		post.WriteString("\n")
	}

	if err := os.WriteFile(prePath, pre.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(postPath, post.Bytes(), 0o644); err != nil {
		return err
	}
	return nil
}

var reNextvalRegclass = regexp.MustCompile(`nextval\('([^']+)'::regclass\)`)

// extractNextvalSequence returns (schema, sequence) referenced by nextval('...::regclass) if present.
// If the regclass name is unqualified, we assume it lives in the table schema.
func extractNextvalSequence(tableSchema string, def string) (string, string, bool) {
	m := reNextvalRegclass.FindStringSubmatch(def)
	if len(m) != 2 {
		return "", "", false
	}
	raw := m[1]
	// Common shapes:
	// - events_id_seq
	// - public.events_id_seq
	// - "public"."events_id_seq"
	schema := tableSchema
	name := raw
	// Handle quoted form "schema"."seq"
	if strings.Contains(raw, "\"") {
		trim := strings.Trim(raw, "\"")
		parts := strings.Split(trim, "\".\"")
		if len(parts) == 2 {
			schema = parts[0]
			name = parts[1]
			return schema, name, true
		}
		// If it was just "seq"
		name = trim
		return schema, name, true
	}
	if strings.Contains(raw, ".") {
		parts := strings.SplitN(raw, ".", 2)
		if len(parts) == 2 {
			schema = parts[0]
			name = parts[1]
		}
	}
	return schema, name, true
}

func rewriteNextvalDefault(tableSchema string, def string) string {
	m := reNextvalRegclass.FindStringSubmatch(def)
	if len(m) != 2 {
		return def
	}
	schema, seq, ok := extractNextvalSequence(tableSchema, def)
	if !ok {
		return def
	}
	qualified := quoteIdent(schema) + "." + quoteIdent(seq)
	// Replace just the regclass literal inside nextval('...').
	return strings.Replace(def, "'"+m[1]+"'::regclass", "'"+qualified+"'::regclass", 1)
}

func regclassLiteral(schema, name string) string {
	// Returns a SQL string literal representing a qualified identifier for ::regclass lookups.
	// Example: '"public"."events_id_seq"'
	q := quoteIdent(schema) + "." + quoteIdent(name)
	return "'" + q + "'"
}

type columnInfo struct {
	name    string
	typ     string
	notNull bool
	def     string
	identity string
}

func loadTableColumns(db *sql.DB, schema, table string) ([]columnInfo, error) {
	rows, err := db.Query(
		`select a.attname::text,
		        format_type(a.atttypid, a.atttypmod)::text,
		        a.attnotnull,
		        coalesce(pg_get_expr(ad.adbin, ad.adrelid), '')::text,
		        coalesce(a.attidentity::text, '')::text
		   from pg_attribute a
		   join pg_class c on c.oid = a.attrelid
		   join pg_namespace n on n.oid = c.relnamespace
		   left join pg_attrdef ad on ad.adrelid = a.attrelid and ad.adnum = a.attnum
		  where n.nspname = $1
		    and c.relname = $2
		    and a.attnum > 0
		    and not a.attisdropped
		  order by a.attnum`,
		schema, table,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []columnInfo
	for rows.Next() {
		var c columnInfo
		if err := rows.Scan(&c.name, &c.typ, &c.notNull, &c.def, &c.identity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func appendConstraintsAndIndexes(w io.StringWriter, db *sql.DB, schema, table string) error {
	// Constraints
	rows, err := db.Query(
		`select pg_constraint.conname::text,
		        pg_constraint.contype::text,
		        pg_get_constraintdef(pg_constraint.oid, true)::text
		   from pg_constraint
		   join pg_class c on c.oid = conrelid
		   join pg_namespace n on n.oid = c.relnamespace
		  where n.nspname = $1 and c.relname = $2 and contype in ('p','u','f','c')
		  order by contype, conname`,
		schema, table,
	)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, typ, def string
		if err := rows.Scan(&name, &typ, &def); err != nil {
			_ = rows.Close()
			return err
		}
		stmt := "ALTER TABLE " + quoteIdent(schema) + "." + quoteIdent(table) +
			" ADD CONSTRAINT " + quoteIdent(name) + " " + def + ";\n"
		_, _ = w.WriteString(stmt)
	}
	_ = rows.Close()

	// Indexes (excluding primary key index)
	idxRows, err := db.Query(
		`select pg_get_indexdef(i.indexrelid)::text
		   from pg_index i
		   join pg_class t on t.oid = i.indrelid
		   join pg_namespace n on n.oid = t.relnamespace
		  where n.nspname = $1 and t.relname = $2 and not i.indisprimary
		  order by 1`,
		schema, table,
	)
	if err != nil {
		return err
	}
	for idxRows.Next() {
		var def string
		if err := idxRows.Scan(&def); err != nil {
			_ = idxRows.Close()
			return err
		}
		_, _ = w.WriteString(def + ";\n")
	}
	_ = idxRows.Close()
	_, _ = w.WriteString("\n")
	return nil
}

var reMissingRoleOID = regexp.MustCompile(`role with OID (\d+) does not exist`)

func maybeDiagnosePgDumpError(sourceDSN string, err error, verbose bool) {
	var pe pgDumpError
	if !errors.As(err, &pe) {
		return
	}
	m := reMissingRoleOID.FindStringSubmatch(pe.Stderr)
	if len(m) != 2 {
		return
	}
	oid, convErr := strconv.ParseInt(m[1], 10, 64)
	if convErr != nil {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "xata2pg: detected pg_dump missing role OID; running source diagnostics...")
	diagnoseMissingRoleOID(sourceDSN, oid, verbose)
}

func diagnoseMissingRoleOID(sourceDSN string, oid int64, verbose bool) {
	db, err := sql.Open("postgres", sourceDSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xata2pg: diagnose: failed to connect to source:", err)
		return
	}
	defer db.Close()

	// Basic context
	var version, who, dbname string
	_ = db.QueryRow("select version()").Scan(&version)
	_ = db.QueryRow("select current_user").Scan(&who)
	_ = db.QueryRow("select current_database()").Scan(&dbname)
	if version != "" {
		fmt.Fprintln(os.Stderr, "xata2pg: source version:", version)
	}
	if who != "" {
		fmt.Fprintln(os.Stderr, "xata2pg: source current_user:", who)
	}
	if dbname != "" {
		fmt.Fprintln(os.Stderr, "xata2pg: source database:", dbname)
	}

	// Does pg_roles expose this OID?
	{
		var rolname string
		qerr := db.QueryRow("select rolname from pg_roles where oid = $1", oid).Scan(&rolname)
		if qerr == nil {
			fmt.Fprintf(os.Stderr, "xata2pg: role oid %d exists as %q in pg_roles\n", oid, rolname)
		} else {
			fmt.Fprintf(os.Stderr, "xata2pg: role oid %d not visible in pg_roles (%v)\n", oid, qerr)
		}
	}

	type probe struct {
		name   string
		countQ string
		sampleQ string
	}
	probes := []probe{
		{
			name:   "pg_database.datdba",
			countQ: `select count(*) from pg_database d left join pg_roles r on r.oid = d.datdba where r.oid is null`,
			sampleQ: `select datname, datdba from pg_database d left join pg_roles r on r.oid = d.datdba where r.oid is null limit 20`,
		},
		{
			name:   "pg_namespace.nspowner",
			countQ: `select count(*) from pg_namespace n left join pg_roles r on r.oid = n.nspowner where r.oid is null`,
			sampleQ: `select nspname, nspowner from pg_namespace n left join pg_roles r on r.oid = n.nspowner where r.oid is null limit 20`,
		},
		{
			name:   "pg_class.relowner",
			countQ: `select count(*) from pg_class c left join pg_roles r on r.oid = c.relowner where r.oid is null`,
			sampleQ: `select n.nspname, c.relname, c.relkind, c.relowner from pg_class c join pg_namespace n on n.oid = c.relnamespace left join pg_roles r on r.oid = c.relowner where r.oid is null limit 20`,
		},
		{
			name:   "pg_proc.proowner",
			countQ: `select count(*) from pg_proc p left join pg_roles r on r.oid = p.proowner where r.oid is null`,
			sampleQ: `select n.nspname, p.proname, p.proowner from pg_proc p join pg_namespace n on n.oid = p.pronamespace left join pg_roles r on r.oid = p.proowner where r.oid is null limit 20`,
		},
		{
			name:   "pg_type.typowner",
			countQ: `select count(*) from pg_type t left join pg_roles r on r.oid = t.typowner where r.oid is null`,
			sampleQ: `select n.nspname, t.typname, t.typowner from pg_type t join pg_namespace n on n.oid = t.typnamespace left join pg_roles r on r.oid = t.typowner where r.oid is null limit 20`,
		},
	}

	for _, p := range probes {
		var cnt int64
		if err := db.QueryRow(p.countQ).Scan(&cnt); err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "xata2pg: probe %s: unable to query (%v)\n", p.name, err)
			}
			continue
		}
		if cnt == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "xata2pg: probe %s: %d object(s) reference a missing role\n", p.name, cnt)
		rows, err := db.Query(p.sampleQ)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "xata2pg: probe %s: sample query failed (%v)\n", p.name, err)
			}
			continue
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			parts := make([]string, 0, len(cols))
			for i, c := range cols {
				parts = append(parts, fmt.Sprintf("%s=%s", c, formatSQLValue(vals[i])))
			}
			fmt.Fprintln(os.Stderr, "  -", strings.Join(parts, " "))
		}
		_ = rows.Close()
	}

	// Focused probes for the specific OID (more actionable for support tickets).
	printOwnerObjects(db, oid, verbose)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "xata2pg: note: this usually indicates the source Postgres endpoint references internal/hidden roles.")
	fmt.Fprintln(os.Stderr, "xata2pg: if pg_dump cannot resolve the role OID, you may need Xata support to fix the catalog/view, or use a non-pg_dump export path.")
}

func formatSQLValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		// database/sql + lib/pq commonly scan text-ish columns into []byte.
		return string(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func printOwnerObjects(db *sql.DB, oid int64, verbose bool) {
	type q struct {
		name string
		sql  string
	}
	qs := []q{
		{
			name: "Objects in pg_class with relowner = missing OID",
			sql:  `select n.nspname::text as schema, c.relname::text as name, c.relkind::text as kind, c.relowner::bigint as owner_oid from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relowner = $1 order by 1,2 limit 100`,
		},
		{
			name: "Objects in pg_type with typowner = missing OID",
			sql:  `select n.nspname::text as schema, t.typname::text as name, t.typtype::text as typtype, t.typowner::bigint as owner_oid from pg_type t join pg_namespace n on n.oid = t.typnamespace where t.typowner = $1 order by 1,2 limit 100`,
		},
		{
			name: "Databases with datdba = missing OID",
			sql:  `select datname::text as database, datdba::bigint as owner_oid from pg_database where datdba = $1 order by 1 limit 100`,
		},
		{
			name: "Schemas with nspowner = missing OID",
			sql:  `select nspname::text as schema, nspowner::bigint as owner_oid from pg_namespace where nspowner = $1 order by 1 limit 100`,
		},
	}

	for _, item := range qs {
		rows, err := db.Query(item.sql, oid)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "xata2pg: focused probe failed (%s): %v\n", item.name, err)
			}
			continue
		}
		cols, _ := rows.Columns()
		count := 0
		var lines []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			parts := make([]string, 0, len(cols))
			for i, c := range cols {
				parts = append(parts, fmt.Sprintf("%s=%s", c, formatSQLValue(vals[i])))
			}
			lines = append(lines, "  - "+strings.Join(parts, " "))
			count++
		}
		_ = rows.Close()
		if count == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "xata2pg: %s (%d)\n", item.name, count)
		for _, ln := range lines {
			fmt.Fprintln(os.Stderr, ln)
		}
	}
}

func readDSNLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func redactDSN(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User == nil {
		return raw
	}
	user := u.User.Username()
	u.User = url.UserPassword(user, "***")
	return u.String()
}

func loadTargetConfig() (targetConfig, error) {
	cfg := targetConfig{
		DatabaseURL: strings.TrimSpace(os.Getenv("POSTGRESQL_DATABASE_URL")),
		Host:        strings.TrimSpace(os.Getenv("POSTGRESQL_HOST")),
		Port:        strings.TrimSpace(os.Getenv("POSTGRESQL_PORT")),
		User:        strings.TrimSpace(os.Getenv("POSTGRESQL_USER")),
		Password:    strings.TrimSpace(os.Getenv("POSTGRESQL_PASSWORD")),
		SSLMode:     strings.TrimSpace(os.Getenv("POSTGRESQL_SSLMODE")),
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}
	if cfg.DatabaseURL == "" {
		// require discrete fields
		missing := []string{}
		if cfg.Host == "" {
			missing = append(missing, "POSTGRESQL_HOST")
		}
		if cfg.Port == "" {
			missing = append(missing, "POSTGRESQL_PORT")
		}
		if cfg.User == "" {
			missing = append(missing, "POSTGRESQL_USER")
		}
		if cfg.Password == "" {
			missing = append(missing, "POSTGRESQL_PASSWORD")
		}
		if len(missing) > 0 {
			return targetConfig{}, fmt.Errorf("missing target env vars: %s (or set POSTGRESQL_DATABASE_URL)", strings.Join(missing, ", "))
		}
	}
	return cfg, nil
}

func (c targetConfig) adminDSN() (string, error) {
	// Connect to maintenance DB "postgres".
	return c.dsnFor("postgres")
}

func (c targetConfig) dsnFor(dbname string) (string, error) {
	if c.DatabaseURL != "" {
		u, err := url.Parse(c.DatabaseURL)
		if err != nil {
			return "", err
		}
		u.Path = "/" + dbname
		return u.String(), nil
	}
	u := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(c.User, c.Password),
		Host:   fmt.Sprintf("%s:%s", c.Host, c.Port),
		Path:   "/" + dbname,
	}
	q := url.Values{}
	if c.SSLMode != "" {
		q.Set("sslmode", c.SSLMode)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// loadEnvFromNearestDotEnv searches upward from cwd for .env files until a .git dir is found.
// It applies env files from repo root to leaf so closer overrides win, and it won't override
// env vars already present in the process environment.
func loadEnvFromNearestDotEnv(verbose bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	var envPaths []string
	cur := cwd
	if verbose {
		fmt.Fprintln(os.Stderr, "xata2pg: searching for .env files from", cwd)
	}
	for {
		envPath := filepath.Join(cur, ".env")
		if info, err := os.Stat(envPath); err == nil && !info.IsDir() {
			envPaths = append(envPaths, envPath)
			if verbose {
				fmt.Fprintln(os.Stderr, "xata2pg: found .env:", envPath)
			}
		}
		gitPath := filepath.Join(cur, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i := len(envPaths) - 1; i >= 0; i-- {
		if verbose {
			fmt.Fprintln(os.Stderr, "xata2pg: applying .env:", envPaths[i])
		}
		if err := applyEnvFile(envPaths[i]); err != nil {
			return err
		}
	}
	return nil
}

func applyEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		sep := strings.Index(line, "=")
		if sep <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:sep])
		val := strings.TrimSpace(line[sep+1:])
		if strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") && len(val) >= 2 {
			val = val[1 : len(val)-1]
		} else if strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") && len(val) >= 2 {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return sc.Err()
}

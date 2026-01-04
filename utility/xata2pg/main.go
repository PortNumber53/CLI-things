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

func main() {
	var (
		inputFile     = flag.String("input", "", "Path to a text file containing Xata Postgres DSNs (one per line)")
		dumpDir       = flag.String("dump-dir", "./xata2pg-dumps", "Directory to write SQL dump files")
		includeBranch = flag.Bool("include-branch", true, "Include :branch in target DB name (as __branch)")
		dropExisting  = flag.Bool("drop-existing", false, "Drop target DBs before recreating them")
		schemaOnly    = flag.Bool("schema-only", false, "Dump schema only (no data)")
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

	for _, src := range lines {
		srcInfo, err := parseSourceDSN(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip invalid DSN %q: %v\n", redactDSN(src), err)
			continue
		}

		targetDBName := buildTargetDBName(srcInfo.db, srcInfo.branch, *includeBranch)
		dumpPath := filepath.Join(*dumpDir, targetDBName+".sql")

		if *verbose {
			fmt.Fprintf(os.Stderr, "source: %s -> target db: %s\n", redactDSN(src), targetDBName)
			fmt.Fprintf(os.Stderr, "dump: %s\n", dumpPath)
		}

		if err := runPgDump(src, dumpPath, *schemaOnly, *verbose); err != nil {
			maybeDiagnosePgDumpError(src, err, *verbose)
			fmt.Fprintf(os.Stderr, "pg_dump failed for %s: %v\n", redactDSN(src), err)
			os.Exit(1)
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

		if err := runPsqlFile(targetDSN, dumpPath, *verbose); err != nil {
			fmt.Fprintf(os.Stderr, "restore failed for %q: %v\n", targetDBName, err)
			os.Exit(1)
		}

		fmt.Printf("ok: %s -> %s\n", srcInfo.fullName(), targetDBName)
	}
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

func runPgDump(sourceDSN, outPath string, schemaOnly bool, verbose bool) error {
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
		"--file", outPath,
	}
	if schemaOnly {
		args = append(args, "--schema-only")
	}
	cmd := exec.Command("pg_dump", args...)
	// Avoid leaking credentials by not echoing command; only show redacted DSN.
	if verbose {
		fmt.Fprintf(os.Stderr, "pg_dump: %s -> %s\n", redactDSN(sourceDSN), outPath)
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
	args := []string{"-d", targetDSN, "-v", "ON_ERROR_STOP=1", "-f", sqlFile}
	cmd := exec.Command("psql", args...)
	if verbose {
		fmt.Fprintf(os.Stderr, "psql: restoring into %s from %s\n", redactDSN(targetDSN), sqlFile)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
				parts = append(parts, fmt.Sprintf("%s=%v", c, vals[i]))
			}
			fmt.Fprintln(os.Stderr, "  -", strings.Join(parts, " "))
		}
		_ = rows.Close()
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "xata2pg: note: this usually indicates the source Postgres endpoint references internal/hidden roles.")
	fmt.Fprintln(os.Stderr, "xata2pg: if pg_dump cannot resolve the role OID, you may need Xata support to fix the catalog/view, or use a non-pg_dump export path.")
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

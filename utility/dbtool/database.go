package dbtool

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
)

// DBConfig holds database configuration
type DBConfig struct {
	Host          string
	Port          string
	Name          string
	User          string
	Password      string
	SSLMode       string
	MigrationsDir string
	// URL is an optional full DSN (e.g. postgres://user:pass@host:5432/db?sslmode=require)
	// If provided, it takes precedence over the discrete fields above.
	URL string
}

func firstNonEmpty(vals ...string) string {
	for _, val := range vals {
		if val != "" {
			return val
		}
	}
	return ""
}

func isXataHTTPSURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && strings.Contains(u.Host, "xata.sh")
}

func overrideDBNameInPostgresURL(original, newDBName string) (string, bool) {
	u, err := url.Parse(original)
	if err != nil {
		return "", false
	}
	if u.Path == "" {
		return "", false
	}
	u.Path = "/" + newDBName
	return u.String(), true
}

// getCurrentFolderName returns the name of the current working directory
func getCurrentFolderName() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	folderName := filepath.Base(cwd)
	return folderName, nil
}

// readConfigFile reads and parses the config.ini file
func readConfigFile(configPath string) (map[string]string, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file %s: %w", configPath, err)
	}
	defer file.Close()

	config := make(map[string]string)
	var currentSection string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			continue
		}

		// Key-value pair
		if strings.Contains(line, "=") && currentSection == "default" {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				config[key] = value
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return config, nil
}

// loadDBConfig loads database configuration. When DBTOOL_CONFIG_FILE is set,
// it reads configuration from that exact file. Otherwise it falls back to
// ~/.config/<current-folder>/config.ini to remain backward compatible.
func loadDBConfig() (*DBConfig, error) {
	configPath := strings.TrimSpace(os.Getenv("DBTOOL_CONFIG_FILE"))
	if configPath == "" {
		folderName, err := getCurrentFolderName()
		if err != nil {
			return nil, err
		}

		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}

		configPath = filepath.Join(homeDir, ".config", folderName, "config.ini")
	}

	config, err := readConfigFile(configPath)
	if err != nil {
		return nil, err
	}

	dbConfig := &DBConfig{
		Host:          firstNonEmpty(config["DB_HOST"], os.Getenv("DB_HOST")),
		Port:          firstNonEmpty(config["DB_PORT"], os.Getenv("DB_PORT")),
		Name:          firstNonEmpty(config["DB_NAME"], os.Getenv("DB_NAME")),
		User:          firstNonEmpty(config["DB_USER"], os.Getenv("DB_USER")),
		Password:      firstNonEmpty(config["DB_PASSWORD"], os.Getenv("DB_PASSWORD")),
		SSLMode:       firstNonEmpty(config["DB_SSLMODE"], os.Getenv("DB_SSLMODE")),
		MigrationsDir: firstNonEmpty(config["DB_MIGRATIONS_DIR"], os.Getenv("DB_MIGRATIONS_DIR")),
		URL:           firstNonEmpty(config["DATABASE_URL"], os.Getenv("DATABASE_URL")),
	}

	// Set defaults
	if dbConfig.SSLMode == "" {
		// lib/pq expects values like: disable, require, verify-ca, verify-full
		dbConfig.SSLMode = "disable"
	}
	if dbConfig.Port == "" {
		dbConfig.Port = "5432"
	}

	return dbConfig, nil
}

// DefaultDBName returns the database name from config: prefers DB_NAME,
// otherwise derives it from a PostgreSQL DSN in DATABASE_URL.
func DefaultDBName() (string, error) {
	cfg, err := loadDBConfig()
	if err != nil {
		return "", err
	}
	if name := strings.TrimSpace(cfg.Name); name != "" {
		return name, nil
	}
	u := strings.TrimSpace(cfg.URL)
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		pu, err := url.Parse(u)
		if err != nil {
			return "", err
		}
		// Path is like /dbname or /dbname:branch; trim leading '/'
		p := strings.TrimPrefix(pu.Path, "/")
		if p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("no default database name found; set DB_NAME or DATABASE_URL in config")
}

// createConnectionString creates a PostgreSQL connection string
func (c *DBConfig) createConnectionString() string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.URL)), "postgres://") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.URL)), "postgresql://") {
		return strings.TrimSpace(c.URL)
	}
	// If a Xata HTTPS URL is provided, we cannot use lib/pq directly.
	if isXataHTTPSURL(c.URL) {
		// Provide a helpful message by panicking here to be caught by callers.
		// Callers of this function should surface the error.
		// We still return a formatted string for completeness, though it should not be used.
		return ""
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode)
}

// createConnectionStringFor overrides db name
func (c *DBConfig) createConnectionStringFor(dbname string) string {
	// If we have a URL DSN, try to override the path component (db name)
	if u := strings.TrimSpace(c.URL); u != "" {
		lower := strings.ToLower(u)
		if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
			if newURL, ok := overrideDBNameInPostgresURL(u, dbname); ok {
				return newURL
			}
			// Fall back to the provided DSN as-is if we cannot rewrite it
			return u
		}
		if isXataHTTPSURL(u) {
			return ""
		}
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, dbname, c.SSLMode)
}

// ConnectDB establishes a connection to the PostgreSQL database
func ConnectDB() (*sql.DB, error) {
	config, err := loadDBConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}

	// Handle Xata HTTPS URL specially with a helpful error
	if isXataHTTPSURL(config.URL) {
		return nil, fmt.Errorf("detected Xata HTTPS DATABASE_URL, which is not PostgreSQL DSN. Please use Xata's PostgreSQL connection URL (postgres://...) or set DATABASE_URL to that value. For details, see Xata docs on Postgres compatibility.")
	}

	connStr := config.createConnectionString()

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return db, nil
}

// GetDBConfig returns the database configuration
func GetDBConfig() (*DBConfig, error) {
	return loadDBConfig()
}

// ConnectDBAs connects to a specific database overriding the name
func ConnectDBAs(dbname string) (*sql.DB, error) {
	config, err := loadDBConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}
	if isXataHTTPSURL(config.URL) {
		return nil, fmt.Errorf("detected Xata HTTPS DATABASE_URL, which is not PostgreSQL DSN. Please use Xata's PostgreSQL connection URL (postgres://...) or set DATABASE_URL to that value. For details, see Xata docs on Postgres compatibility.")
	}
	connStr := config.createConnectionStringFor(dbname)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return db, nil
}

// ListDatabases queries pg_database to list databases (excluding templates)
func ListDatabases() error {
	// connect to current configured DB (any DB can query pg_database)
	db, err := ConnectDB()
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname;`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		fmt.Println(name)
	}
	return rows.Err()
}

// ListTables lists tables from information_schema for a given database.
// If schema is empty, it lists all non-system schemas (excludes pg_catalog and information_schema).
func ListTables(dbname, schema string) error {
	db, err := ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()

	var rows *sql.Rows
	if strings.TrimSpace(schema) == "" {
		q := `
SELECT table_schema, table_name
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
  AND table_schema NOT IN ('pg_catalog','information_schema')
ORDER BY table_schema, table_name;`
		rows, err = db.Query(q)
	} else {
		q := `
SELECT table_schema, table_name
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
  AND table_schema = $1
ORDER BY table_schema, table_name;`
		rows, err = db.Query(q, schema)
	}
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var s, t string
		if err := rows.Scan(&s, &t); err != nil {
			return err
		}
		fmt.Printf("%s.%s\n", s, t)
	}
	return rows.Err()
}

// RunPgDump executes pg_dump with proper auth
func RunPgDump(dbname, filepath string, structureOnly bool) error {
	cfg, err := GetDBConfig()
	if err != nil {
		return err
	}
	// If we have a DSN URL, prefer using it directly with -d
	var args []string
	if u := strings.TrimSpace(cfg.URL); strings.HasPrefix(strings.ToLower(u), "postgres://") || strings.HasPrefix(strings.ToLower(u), "postgresql://") {
		// Override db name in the URL, if possible
		dsn := u
		if newURL, ok := overrideDBNameInPostgresURL(u, dbname); ok {
			dsn = newURL
		}
		args = []string{"-d", dsn, "-f", filepath}
	} else {
		args = []string{"-h", cfg.Host, "-p", cfg.Port, "-U", cfg.User, "-d", dbname, "-f", filepath}
	}
	if structureOnly {
		args = append(args, "--schema-only")
	}
	cmd := exec.Command("pg_dump", args...)
	env := os.Environ()
	// Only set PGPASSWORD when not using a DSN URL with embedded credentials
	if cfg.URL == "" {
		env = append(env, fmt.Sprintf("PGPASSWORD=%s", cfg.Password))
		if cfg.SSLMode != "" {
			env = append(env, fmt.Sprintf("PGSSLMODE=%s", cfg.SSLMode))
		}
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunPSQLFile executes a SQL file against a database using psql
func RunPSQLFile(dbname, filepath string) error {
	cfg, err := GetDBConfig()
	if err != nil {
		return err
	}
	// If we have a DSN URL, prefer using it directly with -d
	var args []string
	if u := strings.TrimSpace(cfg.URL); strings.HasPrefix(strings.ToLower(u), "postgres://") || strings.HasPrefix(strings.ToLower(u), "postgresql://") {
		dsn := u
		if newURL, ok := overrideDBNameInPostgresURL(u, dbname); ok {
			dsn = newURL
		}
		args = []string{"-d", dsn, "-f", filepath}
	} else {
		args = []string{"-h", cfg.Host, "-p", cfg.Port, "-U", cfg.User, "-d", dbname, "-f", filepath}
	}
	cmd := exec.Command("psql", args...)
	env := os.Environ()
	if cfg.URL == "" {
		env = append(env, fmt.Sprintf("PGPASSWORD=%s", cfg.Password))
		if cfg.SSLMode != "" {
			env = append(env, fmt.Sprintf("PGSSLMODE=%s", cfg.SSLMode))
		}
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ResetDatabase drops and recreates public schema
func ResetDatabase(dbname string) error {
	db, err := ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	// Drop and recreate public schema
	stmts := []string{
		"DROP SCHEMA IF EXISTS public CASCADE;",
		"CREATE SCHEMA public;",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// ImportDatabase imports SQL file, optionally after overwrite (reset)
func ImportDatabase(dbname, filepath string, overwrite bool) error {
	if overwrite {
		if err := ResetDatabase(dbname); err != nil {
			return fmt.Errorf("overwrite reset failed: %w", err)
		}
	}
	return RunPSQLFile(dbname, filepath)
}

// QueryDatabase runs a SQL statement and prints output; optionally JSON
func QueryDatabase(dbname, query string, asJSON bool) error {
	if strings.TrimSpace(query) == "" {
		return errors.New("empty query")
	}
	db, err := ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		// Try Exec for non-SELECT
		if _, exErr := db.Exec(query); exErr == nil {
			fmt.Println("OK")
			return nil
		}
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var out []map[string]any
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		rec := make(map[string]any, len(cols))
		for i, c := range cols {
			rec[c] = vals[i]
		}
		if asJSON {
			out = append(out, rec)
		} else {
			// simple table-ish print
			var parts []string
			for i, c := range cols {
				parts = append(parts, fmt.Sprintf("%s=%v", c, vals[i]))
			}
			fmt.Println(strings.Join(parts, " | "))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	return nil
}

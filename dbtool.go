//go:build dbtool

package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
)

// DBConfig holds database configuration
type DBConfig struct {
	Host           string
	Port           string
	Name           string
	User           string
	Password       string
	SSLMode        string
	MigrationsDir  string
}

func isHelpToken(s string) bool {
    switch strings.ToLower(s) {
    case "-h", "--help", "help", "h":
        return true
    default:
        return false
    }
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

// loadDBConfig loads database configuration from ~/.config/<FOLDER>/config.ini
func loadDBConfig() (*DBConfig, error) {
	folderName, err := getCurrentFolderName()
	if err != nil {
		return nil, err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, ".config", folderName, "config.ini")

	config, err := readConfigFile(configPath)
	if err != nil {
		return nil, err
	}

	dbConfig := &DBConfig{
		Host:          config["DB_HOST"],
		Port:          config["DB_PORT"],
		Name:          config["DB_NAME"],
		User:          config["DB_USER"],
		Password:      config["DB_PASSWORD"],
		SSLMode:       config["DB_SSLMODE"],
		MigrationsDir: config["DB_MIGRATIONS_DIR"],
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

// createConnectionString creates a PostgreSQL connection string
func (c *DBConfig) createConnectionString() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode)
}

// createConnectionStringFor overrides db name
func (c *DBConfig) createConnectionStringFor(dbname string) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, dbname, c.SSLMode)
}

// ConnectDB establishes a connection to the PostgreSQL database
func ConnectDB() (*sql.DB, error) {
	config, err := loadDBConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
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

// listDatabases queries pg_database to list databases (excluding templates)
func listDatabases() error {
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

// runPgDump executes pg_dump with proper auth
func runPgDump(dbname, filepath string, structureOnly bool) error {
	cfg, err := GetDBConfig()
	if err != nil {
		return err
	}
	args := []string{"-h", cfg.Host, "-p", cfg.Port, "-U", cfg.User, "-d", dbname, "-f", filepath}
	if structureOnly {
		args = append(args, "--schema-only")
	}
	cmd := exec.Command("pg_dump", args...)
	env := os.Environ()
	env = append(env, fmt.Sprintf("PGPASSWORD=%s", cfg.Password))
	if cfg.SSLMode != "" {
		env = append(env, fmt.Sprintf("PGSSLMODE=%s", cfg.SSLMode))
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runPSQLFile executes a SQL file against a database using psql
func runPSQLFile(dbname, filepath string) error {
	cfg, err := GetDBConfig()
	if err != nil {
		return err
	}
	args := []string{"-h", cfg.Host, "-p", cfg.Port, "-U", cfg.User, "-d", dbname, "-f", filepath}
	cmd := exec.Command("psql", args...)
	env := os.Environ()
	env = append(env, fmt.Sprintf("PGPASSWORD=%s", cfg.Password))
	if cfg.SSLMode != "" {
		env = append(env, fmt.Sprintf("PGSSLMODE=%s", cfg.SSLMode))
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// resetDatabase drops and recreates public schema
func resetDatabase(dbname string) error {
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

// importDatabase imports SQL file, optionally after overwrite (reset)
func importDatabase(dbname, filepath string, overwrite bool) error {
	if overwrite {
		if err := resetDatabase(dbname); err != nil {
			return fmt.Errorf("overwrite reset failed: %w", err)
		}
	}
	return runPSQLFile(dbname, filepath)
}

// queryDatabase runs a SQL statement and prints output; optionally JSON
func queryDatabase(dbname, query string, asJSON bool) error {
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

func usage() {
    fmt.Fprintf(os.Stderr, "Usage:\n")
    fmt.Fprintf(os.Stderr, "  database|db list|ls\n")
    fmt.Fprintf(os.Stderr, "  database|db dump|export <dbname> <filepath> [--structure-only]\n")
    fmt.Fprintf(os.Stderr, "  database|db import|load <dbname> <filepath> [--overwrite]\n")
    fmt.Fprintf(os.Stderr, "  database|db reset|wipe <dbname> [--noconfirm]\n")
    fmt.Fprintf(os.Stderr, "  query|q <dbname> --query=\"<sql>\" [--json]\n")
    fmt.Fprintf(os.Stderr, "  help [command] [subcommand]\n")
}

func helpSummary() {
    fmt.Println("Commands:")
    fmt.Println("  database (db)")
    fmt.Println("    list (ls)")
    fmt.Println("    dump (export) <dbname> <filepath> [--structure-only]")
    fmt.Println("    import (load) <dbname> <filepath> [--overwrite]")
    fmt.Println("    reset (wipe) <dbname> [--noconfirm]")
    fmt.Println("  query (q) <dbname> --query=\"<sql>\" [--json]")
    fmt.Println("  help [command] [subcommand]")
}

func helpFor(mainCmd, sub string) {
    mc := normalizeMain(mainCmd)
    if mc == "query" {
        fmt.Println("Usage: query|q <dbname> --query=\"<sql>\" [--json]")
        return
    }
    if mc == "database" {
        if sub == "" {
            fmt.Println("Usage: database|db <list|dump|import|reset> [args]")
            return
        }
        sc := normalizeSub(sub)
        switch sc {
        case "list":
            fmt.Println("Usage: database|db list|ls")
        case "dump":
            fmt.Println("Usage: database|db dump|export <dbname> <filepath> [--structure-only]")
        case "import":
            fmt.Println("Usage: database|db import|load <dbname> <filepath> [--overwrite]")
        case "reset":
            fmt.Println("Usage: database|db reset|wipe <dbname> [--noconfirm]")
        default:
            usage()
        }
        return
    }
    usage()
}

func normalizeMain(s string) string {
    switch strings.ToLower(s) {
    case "database", "db":
        return "database"
    case "query", "q":
        return "query"
    case "help", "h", "--help", "-h":
        return "help"
    default:
        return s
    }
}

func normalizeSub(s string) string {
    switch strings.ToLower(s) {
    case "list", "ls":
        return "list"
    case "dump", "export":
        return "dump"
    case "import", "load":
        return "import"
    case "reset", "wipe":
        return "reset"
    default:
        return s
    }
}

func main() {
    if len(os.Args) < 2 {
        usage()
        os.Exit(2)
    }
    // global help handling
    if normalizeMain(os.Args[1]) == "help" {
        if len(os.Args) == 2 {
            helpSummary()
            return
        }
        if len(os.Args) == 3 {
            topic := normalizeMain(os.Args[2])
            if topic == "database" || topic == "query" {
                helpFor(topic, "")
                return
            }
            // allow help on subcommands directly, assume database
            helpFor("database", os.Args[2])
            return
        }
        if len(os.Args) >= 4 && normalizeMain(os.Args[2]) == "database" {
            helpFor("database", os.Args[3])
            return
        }
        helpSummary()
        return
    }

    switch normalizeMain(os.Args[1]) {
    case "database":
        if len(os.Args) < 3 {
            helpFor("database", "")
            return
        }
        if isHelpToken(os.Args[2]) {
            helpFor("database", "")
            return
        }
        sub := normalizeSub(os.Args[2])
        switch sub {
        case "list":
            if len(os.Args) >= 4 && isHelpToken(os.Args[3]) {
                fmt.Println("Usage: database|db list|ls")
                return
            }
            if err := listDatabases(); err != nil {
                fmt.Fprintf(os.Stderr, "Error: %v\n", err)
                os.Exit(1)
            }
        case "dump":
            dumpFlags := flag.NewFlagSet("database dump", flag.ExitOnError)
            structureOnly := dumpFlags.Bool("structure-only", false, "Dump only schema (no data)")
            dumpFlags.Usage = func() { fmt.Println("Usage: database|db dump|export <dbname> <filepath> [--structure-only]") }
            // parse flags after the subcommand and two positional args
            if len(os.Args) >= 4 && isHelpToken(os.Args[3]) {
                dumpFlags.Usage()
                return
            }
            if len(os.Args) < 5 {
                fmt.Fprintln(os.Stderr, "Usage: database dump <dbname> <filepath> [--structure-only]")
                os.Exit(2)
            }
            dbname := os.Args[3]
            outPath := os.Args[4]
            if err := dumpFlags.Parse(os.Args[5:]); err != nil {
                fmt.Fprintf(os.Stderr, "Error: %v\n", err)
                os.Exit(2)
            }
            if err := runPgDump(dbname, outPath, *structureOnly); err != nil {
                fmt.Fprintf(os.Stderr, "dump failed: %v\n", err)
                os.Exit(1)
            }
        case "import":
            impFlags := flag.NewFlagSet("database import", flag.ExitOnError)
            overwrite := impFlags.Bool("overwrite", false, "Reset schema before import")
            impFlags.Usage = func() { fmt.Println("Usage: database|db import|load <dbname> <filepath> [--overwrite]") }
            if len(os.Args) >= 4 && isHelpToken(os.Args[3]) {
                impFlags.Usage()
                return
            }
            if len(os.Args) < 5 {
                fmt.Fprintln(os.Stderr, "Usage: database import <dbname> <filepath> [--overwrite]")
                os.Exit(2)
            }
            dbname := os.Args[3]
            inPath := os.Args[4]
            if err := impFlags.Parse(os.Args[5:]); err != nil {
                fmt.Fprintf(os.Stderr, "Error: %v\n", err)
                os.Exit(2)
            }
            if err := importDatabase(dbname, inPath, *overwrite); err != nil {
                fmt.Fprintf(os.Stderr, "import failed: %v\n", err)
                os.Exit(1)
            }
        case "reset":
            rstFlags := flag.NewFlagSet("database reset", flag.ExitOnError)
            noconfirm := rstFlags.Bool("noconfirm", false, "Do not ask for confirmation")
            rstFlags.Usage = func() { fmt.Println("Usage: database|db reset|wipe <dbname> [--noconfirm]") }
            if len(os.Args) >= 4 && isHelpToken(os.Args[3]) {
                rstFlags.Usage()
                return
            }
            if len(os.Args) < 4 {
                fmt.Fprintln(os.Stderr, "Usage: database reset <dbname> [--noconfirm]")
                os.Exit(2)
            }
            dbname := os.Args[3]
            if err := rstFlags.Parse(os.Args[4:]); err != nil {
                fmt.Fprintf(os.Stderr, "Error: %v\n", err)
                os.Exit(2)
            }
            if !*noconfirm {
                fmt.Printf("Reset database '%s'? This will drop all objects. Type 'yes' to continue: ", dbname)
                reader := bufio.NewReader(os.Stdin)
                text, _ := reader.ReadString('\n')
                text = strings.TrimSpace(text)
                if text != "yes" {
                    fmt.Println("Aborted")
                    return
                }
            }
            if err := resetDatabase(dbname); err != nil {
                fmt.Fprintf(os.Stderr, "reset failed: %v\n", err)
                os.Exit(1)
            }
        default:
            usage()
            os.Exit(2)
        }
    case "query":
        if len(os.Args) >= 3 && isHelpToken(os.Args[2]) {
            helpFor("query", "")
            return
        }
        qFlags := flag.NewFlagSet("query", flag.ExitOnError)
        q := qFlags.String("query", "", "SQL statement to execute")
        asJSON := qFlags.Bool("json", false, "Output as JSON")
        qFlags.Usage = func() { fmt.Println("Usage: query|q <dbname> --query=\"<sql>\" [--json]") }
        if len(os.Args) < 3 {
            fmt.Fprintln(os.Stderr, "Usage: query <dbname> --query=\"<sql>\" [--json]")
            os.Exit(2)
        }
        dbname := os.Args[2]
        if err := qFlags.Parse(os.Args[3:]); err != nil {
            fmt.Fprintf(os.Stderr, "Error: %v\n", err)
            os.Exit(2)
        }
        if err := queryDatabase(dbname, *q, *asJSON); err != nil {
            fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
            os.Exit(1)
        }
    default:
        usage()
        os.Exit(2)
    }
}

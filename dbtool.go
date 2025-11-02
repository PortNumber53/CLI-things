//go:build dbtool

package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	db "cli-things/utility/dbtool"
)

const version = "1.0.1"

var verbose bool

// parseAndStripGlobalFlags scans os.Args for global flags like --verbose/-v and --version,
// sets globals accordingly, and returns a cleaned slice of args without those flags.
func parseAndStripGlobalFlags(args []string) []string {
	cleaned := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--verbose", "-v":
			verbose = true
			// do not append to cleaned
		case "--version":
			fmt.Printf("dbtool version %s\n", version)
			os.Exit(0)
		default:
			cleaned = append(cleaned, a)
		}
	}
	return cleaned
}

func loadEnvFromNearestDotEnv() error {
	currentDir, err := os.Getwd()
	if err != nil {
		return err
	}

	var envPaths []string
	if verbose {
		fmt.Fprintln(os.Stderr, "dbtool: searching for .env files from", currentDir)
	}
	for {
		envPath := filepath.Join(currentDir, ".env")
		if info, err := os.Stat(envPath); err == nil && !info.IsDir() {
			envPaths = append(envPaths, envPath)
			if verbose {
				fmt.Fprintln(os.Stderr, "dbtool: found .env:", envPath)
			}
		}

		gitPath := filepath.Join(currentDir, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
			break
		}

		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}

	for i := len(envPaths) - 1; i >= 0; i-- {
		if verbose {
			fmt.Fprintln(os.Stderr, "dbtool: applying .env:", envPaths[i])
		}
		if err := applyEnvFile(envPaths[i]); err != nil {
			return err
		}
	}
	return nil
}

func applyEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		sepIndex := strings.Index(line, "=")
		if sepIndex <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:sepIndex])
		value := strings.TrimSpace(line[sepIndex+1:])
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && len(value) >= 2 {
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			value = value[1 : len(value)-1]
		}
		if key == "DBTOOL_CONFIG_FILE" && value != "" && !filepath.IsAbs(value) {
			resolved := filepath.Join(filepath.Dir(path), value)
			if verbose {
				fmt.Fprintf(os.Stderr, "dbtool: resolving DBTOOL_CONFIG_FILE relative to %s -> %s\n", path, resolved)
			}
			value = resolved
		}
		// Only set the environment variable if it doesn't already exist
		// This allows command-line environment variables to override .env file values
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		} else if verbose {
			fmt.Fprintf(os.Stderr, "dbtool: skipping %s from .env (already set in environment)\n", key)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	return nil
}

func isHelpToken(s string) bool {
	switch strings.ToLower(s) {
	case "-h", "--help", "help", "h":
		return true
	default:
		return false
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  database|db list|ls\n")
	fmt.Fprintf(os.Stderr, "  database|db dump|export <dbname> <filepath> [--structure-only]\n")
	fmt.Fprintf(os.Stderr, "  database|db import|load <dbname> <filepath> [--overwrite]\n")
	fmt.Fprintf(os.Stderr, "  database|db reset|wipe <dbname> [--noconfirm]\n")
	fmt.Fprintf(os.Stderr, "  table|tables list|ls [<dbname>] [--schema=<schema>]\n")
	fmt.Fprintf(os.Stderr, "  query|q [<dbname>] --query=\"<sql>\" [--json]\n")
	fmt.Fprintf(os.Stderr, "  help [command] [subcommand]\n")
	fmt.Fprintf(os.Stderr, "\nGlobal flags:\n")
	fmt.Fprintf(os.Stderr, "  -v, --verbose   Show diagnostics about .env and config.ini resolution\n")
	fmt.Fprintf(os.Stderr, "  --version       Show version information\n")
}

func helpSummary() {
	fmt.Println("Commands:")
	fmt.Println("  database (db)")
	fmt.Println("    list (ls)")
	fmt.Println("    dump (export) <dbname> <filepath> [--structure-only]")
	fmt.Println("    import (load) <dbname> <filepath> [--overwrite]")
	fmt.Println("    reset (wipe) <dbname> [--noconfirm]")
	fmt.Println("  table (tables)")
	fmt.Println("    list (ls) [<dbname>] [--schema=<schema>]")
	fmt.Println("  query (q) [<dbname>] --query=\"<sql>\" [--json]")
	fmt.Println("  help [command] [subcommand]")
}

func helpFor(mainCmd, sub string) {
	mc := normalizeMain(mainCmd)
	if mc == "query" {
		fmt.Println("Usage: query|q [<dbname>] --query=\"<sql>\" [--json]")
		return
	}
	if mc == "table" {
		if sub == "" {
			fmt.Println("Usage: table|tables list|ls [<dbname>] [--schema=<schema>]")
			return
		}
		sc := normalizeSub(sub)
		switch sc {
		case "list":
			fmt.Println("Usage: table|tables list|ls [<dbname>] [--schema=<schema>]")
		default:
			usage()
		}
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
	case "table", "tables":
		return "table"
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
	// Handle global flags first and strip them from os.Args so subcommands don't see them
	os.Args = parseAndStripGlobalFlags(os.Args)
	if verbose {
		// Export to the dbtool package via env var
		os.Setenv("DBTOOL_VERBOSE", "1")
	}
	if err := loadEnvFromNearestDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load .env file: %v\n", err)
		os.Exit(1)
	}
	if verbose {
		if v := strings.TrimSpace(os.Getenv("DBTOOL_CONFIG_FILE")); v != "" {
			fmt.Fprintln(os.Stderr, "dbtool: DBTOOL_CONFIG_FILE after .env:", v)
		} else {
			fmt.Fprintln(os.Stderr, "dbtool: DBTOOL_CONFIG_FILE not set; will use default search path in dbtool package")
		}
	}
	if len(os.Args) < 2 {
		fmt.Println("No command provided. Run 'dbtool help' to see available commands.")
		usage()
		return
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
			if err := db.ListDatabases(); err != nil {
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
			if err := db.RunPgDump(dbname, outPath, *structureOnly); err != nil {
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
			if err := db.ImportDatabase(dbname, inPath, *overwrite); err != nil {
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
			if err := db.ResetDatabase(dbname); err != nil {
				fmt.Fprintf(os.Stderr, "reset failed: %v\n", err)
				os.Exit(1)
			}
		default:
			usage()
			os.Exit(2)
		}
	case "table":
		if len(os.Args) < 3 {
			helpFor("table", "")
			return
		}
		if isHelpToken(os.Args[2]) {
			helpFor("table", "")
			return
		}
		sub := normalizeSub(os.Args[2])
		switch sub {
		case "list":
			tblFlags := flag.NewFlagSet("table list", flag.ExitOnError)
			schema := tblFlags.String("schema", "", "Schema to filter by (default: all non-system schemas)")
			tblFlags.Usage = func() { fmt.Println("Usage: table|tables list|ls [<dbname>] [--schema=<schema>]") }
			// Determine if a dbname positional is provided. If the next arg starts with '-' or is absent,
			// use the default DB name from config. Otherwise, treat it as dbname.
			var dbname string
			// There may be no positional dbname: args after 'list' start at index 3
			if len(os.Args) >= 4 && !strings.HasPrefix(os.Args[3], "-") {
				dbname = os.Args[3]
				if err := tblFlags.Parse(os.Args[4:]); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(2)
				}
			} else {
				// No dbname provided; parse flags from current position and then compute default
				if err := tblFlags.Parse(os.Args[3:]); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(2)
				}
				var err error
				dbname, err = db.DefaultDBName()
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(2)
				}
			}
			if err := db.ListTables(dbname, *schema); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(2)
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
		qFlags.Usage = func() { fmt.Println("Usage: query|q [<dbname>] --query=\"<sql>\" [--json]") }
		// Determine if a dbname positional is provided. If the next arg starts with '-' or is absent,
		// use the default DB name from config. Otherwise, treat it as dbname.
		var dbname string
		if len(os.Args) >= 3 && !strings.HasPrefix(os.Args[2], "-") {
			dbname = os.Args[2]
			if err := qFlags.Parse(os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(2)
			}
		} else {
			// No dbname provided; parse flags from current position and then compute default
			if err := qFlags.Parse(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(2)
			}
			var err error
			dbname, err = db.DefaultDBName()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(2)
			}
		}
		if err := db.QueryDatabase(dbname, *q, *asJSON); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

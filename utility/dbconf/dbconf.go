package dbconf

import (
	"bufio"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
)

func isVerbose() bool { return strings.TrimSpace(os.Getenv("DBTOOL_VERBOSE")) == "1" }

func vprintln(a ...any) { if isVerbose() { fmt.Fprintln(os.Stderr, a...) } }
func vprintf(format string, a ...any) { if isVerbose() { fmt.Fprintf(os.Stderr, format, a...) } }

// DBConfig holds database configuration
type DBConfig struct {
	Host          string
	Port          string
	Name          string
	User          string
	Password      string
	SSLMode       string
	MigrationsDir string
	URL           string // full DSN, takes precedence when set
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

func isXataPostgresURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "postgres" && scheme != "postgresql" {
		return false
	}
	return strings.Contains(u.Host, "xata.sh")
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

func getCurrentFolderName() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return filepath.Base(cwd), nil
}

// readConfigFile supports INI-like format with optional [default] section
func readConfigFile(configPath string) (map[string]string, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file %s: %w", configPath, err)
	}
	defer file.Close()

	config := make(map[string]string)
	var currentSection string
	hasAnySection := false
	f, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(f), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			hasAnySection = true
			continue
		}
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if currentSection == "default" || !hasAnySection || currentSection == "" {
				config[key] = value
			}
		}
	}
	return config, nil
}

// applyEnvFile reads key=value lines from a .env and sets os.Environ accordingly.
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
		if sepIndex <= 0 { continue }
		key := strings.TrimSpace(line[:sepIndex])
		value := strings.TrimSpace(line[sepIndex+1:])
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && len(value) >= 2 {
			value = value[1:len(value)-1]
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			value = value[1:len(value)-1]
		}
		if key == "DBTOOL_CONFIG_FILE" && value != "" && !filepath.IsAbs(value) {
			resolved := filepath.Join(filepath.Dir(path), value)
			vprintf("dbconf: resolving DBTOOL_CONFIG_FILE relative to %s -> %s\n", path, resolved)
			value = resolved
		}
		// Only set the environment variable if it doesn't already exist
		// This allows command-line environment variables to override .env file values
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		} else {
			vprintf("dbconf: skipping %s from .env (already set in environment)\n", key)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	return nil
}

// loadEnvFromNearestDotEnv walks up from cwd to repo root and applies all .env files found.
func loadEnvFromNearestDotEnv() error {
	currentDir, err := os.Getwd()
	if err != nil { return err }
	var envPaths []string
	vprintf("dbconf: searching for .env files from %s\n", currentDir)
	for {
		envPath := filepath.Join(currentDir, ".env")
		if info, err := os.Stat(envPath); err == nil && !info.IsDir() {
			envPaths = append(envPaths, envPath)
			vprintf("dbconf: found .env: %s\n", envPath)
		}
		gitPath := filepath.Join(currentDir, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() { break }
		parent := filepath.Dir(currentDir)
		if parent == currentDir { break }
		currentDir = parent
	}
	for i := len(envPaths) - 1; i >= 0; i-- {
		vprintf("dbconf: applying .env: %s\n", envPaths[i])
		if err := applyEnvFile(envPaths[i]); err != nil { return err }
	}
	return nil
}

// load loads DB configuration, preferring DBTOOL_CONFIG_FILE, else ~/.config/<cwd>/config.ini
func load() (*DBConfig, error) {
    // Ensure .env variables are loaded to mirror dbtool behavior
    _ = loadEnvFromNearestDotEnv()
    configPath := strings.TrimSpace(os.Getenv("DBTOOL_CONFIG_FILE"))
    var config map[string]string
    if configPath == "" {
        folderName, err := getCurrentFolderName()
        if err != nil {
            // Non-fatal; continue with empty config
            vprintln("dbconf: could not determine current folder; skipping config.ini")
            config = make(map[string]string)
        } else {
            homeDir, herr := os.UserHomeDir()
            if herr != nil {
                // When running under systemd without HOME, skip config.ini gracefully
                vprintln("dbconf: HOME not set; skipping config.ini and relying on environment variables only")
                config = make(map[string]string)
            } else {
                configPath = filepath.Join(homeDir, ".config", folderName, "config.ini")
                vprintln("dbconf: using default config.ini:", configPath)
                // Check if file exists before trying to read it
                if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
                    vprintln("dbconf: config.ini not found; relying on environment variables only")
                    config = make(map[string]string)
                } else {
                    vprintln("dbconf: reading config.ini:", configPath)
                    var rerr error
                    config, rerr = readConfigFile(configPath)
                    if rerr != nil {
                        return nil, rerr
                    }
                }
            }
        }
    } else {
        // DBTOOL_CONFIG_FILE is explicitly set, so it must exist
        vprintln("dbconf: using DBTOOL_CONFIG_FILE:", configPath)
        vprintln("dbconf: reading config.ini:", configPath)
        var rerr error
        config, rerr = readConfigFile(configPath)
        if rerr != nil {
            return nil, rerr
        }
    }

	dbConfig := &DBConfig{
		Host:          firstNonEmpty(os.Getenv("DB_HOST"), config["DB_HOST"], config["HOST"]),
		Port:          firstNonEmpty(os.Getenv("DB_PORT"), config["DB_PORT"], config["PORT"]),
		Name:          firstNonEmpty(os.Getenv("DB_NAME"), config["DB_NAME"], config["NAME"]),
		User:          firstNonEmpty(os.Getenv("DB_USER"), config["DB_USER"], config["USER"]),
		Password:      firstNonEmpty(os.Getenv("DB_PASSWORD"), config["DB_PASSWORD"], config["PASSWORD"]),
		SSLMode:       firstNonEmpty(os.Getenv("DB_SSLMODE"), config["DB_SSLMODE"], config["SSL_MODE"]),
		MigrationsDir: firstNonEmpty(os.Getenv("DB_MIGRATIONS_DIR"), config["DB_MIGRATIONS_DIR"], config["MIGRATIONS_DIR"]),
		URL:           firstNonEmpty(os.Getenv("DATABASE_URL"), config["DATABASE_URL"], config["DATABASE_URL"]),
	}

	if dbConfig.URL != "" {
		// Clear discrete fields to avoid ambiguity
		dbConfig.Host = ""
		dbConfig.Port = ""
		dbConfig.Name = ""
		dbConfig.User = ""
		dbConfig.Password = ""
		dbConfig.SSLMode = ""
	}

	if isVerbose() {
		vprintf("dbconf: parsed config keys: DB_HOST=%q DB_PORT=%q DB_NAME=%q DB_USER=%q DB_SSLMODE=%q DATABASE_URL_present=%v\n",
			dbConfig.Host, dbConfig.Port, dbConfig.Name, dbConfig.User, dbConfig.SSLMode, dbConfig.URL != "")
		if u := strings.TrimSpace(dbConfig.URL); u != "" {
			if pu, err := url.Parse(u); err == nil {
				if pu.User != nil {
					if _, has := pu.User.Password(); has {
						pu.User = url.User(pu.User.Username())
					}
				}
				vprintln("dbconf: effective DATABASE_URL:", pu.String())
			} else {
				vprintln("dbconf: effective DATABASE_URL (unparsed):", "<invalid>")
			}
		} else {
			vprintln("dbconf: effective DATABASE_URL: <empty>")
		}
	}

	if dbConfig.SSLMode == "" {
		dbConfig.SSLMode = "disable"
	}
	if dbConfig.Port == "" {
		dbConfig.Port = "5432"
	}
	return dbConfig, nil
}

// GetDBConfig returns loaded configuration
func GetDBConfig() (*DBConfig, error) { return load() }

// DefaultDBName returns DB name from config or DSN
func DefaultDBName() (string, error) {
	cfg, err := load()
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
		p := strings.TrimPrefix(pu.Path, "/")
		if p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("no default database name found; set DB_NAME or DATABASE_URL in config")
}

func (c *DBConfig) createConnectionString() string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.URL)), "postgres://") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.URL)), "postgresql://") {
		return strings.TrimSpace(c.URL)
	}
	if isXataHTTPSURL(c.URL) {
		return ""
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode)
}

func (c *DBConfig) createConnectionStringFor(dbname string) string {
	if u := strings.TrimSpace(c.URL); u != "" {
		lower := strings.ToLower(u)
		if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
			if newURL, ok := overrideDBNameInPostgresURL(u, dbname); ok {
				return newURL
			}
			return u
		}
		if isXataHTTPSURL(u) {
			return ""
		}
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, dbname, c.SSLMode)
}

func ConnectDB() (*sql.DB, error) {
	config, err := load()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}
	if isXataHTTPSURL(config.URL) {
		return nil, fmt.Errorf("detected Xata HTTPS DATABASE_URL, which is not PostgreSQL DSN. Please use a PostgreSQL connection URL (postgres://...)")
	}
	connStr := config.createConnectionString()
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}
	if !(isXataPostgresURL(strings.TrimSpace(config.URL))) {
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to ping database: %w", err)
		}
	}
	return db, nil
}

func ConnectDBAs(dbname string) (*sql.DB, error) {
	config, err := load()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}
	if isXataHTTPSURL(config.URL) {
		return nil, fmt.Errorf("detected Xata HTTPS DATABASE_URL, which is not PostgreSQL DSN. Please use a PostgreSQL connection URL (postgres://...)")
	}
	connStr := config.createConnectionStringFor(dbname)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}
	if !(isXataPostgresURL(strings.TrimSpace(config.URL))) {
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to ping database: %w", err)
		}
	}
	return db, nil
}

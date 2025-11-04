package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"cli-things/utility/dbconf"
)

// providers are simple plaintext endpoints that return the caller's public IP
var providers = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://checkip.amazonaws.com",
	"https://icanhazip.com",
	"https://ip.seeip.org",
}

type cfZoneResp struct {
	Success bool `json:"success"`
	Result  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
	Errors any `json:"errors"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied *bool  `json:"proxied"`
}

type cfDNSResp struct {
	Success bool          `json:"success"`
	Result  []cfDNSRecord `json:"result"`
}

func cfGetARecords(ctx context.Context, token, zoneID, fqdn string) ([]cfDNSRecord, error) {
	var dr cfDNSResp
	url := "https://api.cloudflare.com/client/v4/zones/" + zoneID + "/dns_records?type=A&name=" + fqdn
	if err := cfDoWithRetry(ctx, http.MethodGet, url, token, nil, &dr, 3, 500*time.Millisecond); err != nil {
		return nil, err
	}
	if !dr.Success {
		return nil, fmt.Errorf("cloudflare api returned unsuccessful response")
	}
	return dr.Result, nil
}

func cfDeleteDNSRecord(ctx context.Context, token, zoneID, recordID string) error {
	url := "https://api.cloudflare.com/client/v4/zones/" + zoneID + "/dns_records/" + recordID
	return cfDoWithRetry(ctx, http.MethodDelete, url, token, nil, nil, 3, 500*time.Millisecond)
}

func getCurrentStoredIP(ctx context.Context, dbname string) (string, error) {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return "", err
	}
	defer db.Close()
	row := db.QueryRowContext(ctx, `SELECT ip::text FROM public.public_ip_history WHERE last_use_at IS NULL ORDER BY first_use_at DESC LIMIT 1`)
	var ip string
	if err := row.Scan(&ip); err != nil {
		return "", err
	}
	if i := strings.Index(ip, "/"); i > 0 {
		ip = ip[:i]
	}
	return ip, nil
}

func cfDo(ctx context.Context, method, url, token string, body any, out any) error {
	var reqBody *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil {
		dec := json.NewDecoder(resp.Body)
		return dec.Decode(out)
	}
	return nil
}

func cfDoWithRetry(ctx context.Context, method, url, token string, body any, out any, attempts int, backoff time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		if err := cfDo(ctx, method, url, token, body, out); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func cfFindZoneID(ctx context.Context, token, zoneName string) (string, error) {
	var zr cfZoneResp
	url := "https://api.cloudflare.com/client/v4/zones?name=" + zoneName
	if err := cfDoWithRetry(ctx, http.MethodGet, url, token, nil, &zr, 3, 500*time.Millisecond); err != nil {
		return "", err
	}
	if !zr.Success || len(zr.Result) == 0 {
		return "", fmt.Errorf("zone not found")
	}
	return zr.Result[0].ID, nil
}

func cfGetARecord(ctx context.Context, token, zoneID, fqdn string) (*cfDNSRecord, error) {
	var dr cfDNSResp
	url := "https://api.cloudflare.com/client/v4/zones/" + zoneID + "/dns_records?type=A&name=" + fqdn
	if err := cfDoWithRetry(ctx, http.MethodGet, url, token, nil, &dr, 3, 500*time.Millisecond); err != nil {
		return nil, err
	}
	if !dr.Success || len(dr.Result) == 0 {
		return nil, nil
	}
	r := dr.Result[0]
	return &r, nil
}

func cfUpsertARecord(ctx context.Context, token, zoneID, fqdn, ip string, record *cfDNSRecord) error {
	ttl := 300
	proxied := false
	payload := map[string]any{"type": "A", "name": fqdn, "content": ip, "ttl": ttl, "proxied": proxied}
	if record == nil {
		url := "https://api.cloudflare.com/client/v4/zones/" + zoneID + "/dns_records"
		return cfDo(ctx, http.MethodPost, url, token, payload, nil)
	}
	url := "https://api.cloudflare.com/client/v4/zones/" + zoneID + "/dns_records/" + record.ID
	return cfDo(ctx, http.MethodPatch, url, token, payload, nil)
}

func fetchIP(ctx context.Context, client *http.Client, url string) (net.IP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cli-things-publicip/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("non-2xx status: %s", resp.Status)
	}
	s := bufio.NewScanner(resp.Body)
	s.Buffer(make([]byte, 0, 64), 256)
	if !s.Scan() {
		if err := s.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("empty response")
	}
	line := strings.TrimSpace(s.Text())
	// some providers may return extra text; split by space or other tokens
	if i := strings.IndexFunc(line, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }); i >= 0 {
		line = line[:i]
	}
	ip := net.ParseIP(line)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP in response: %q", line)
	}
	return ip, nil
}

func isFamily(ip net.IP, v4, v6 bool) bool {
	if v4 && ip.To4() != nil {
		return true
	}
	if v6 && ip.To4() == nil { // IPv6 retained as 16-byte, To4() returns nil
		return true
	}
	// if neither flag set, accept any
	if !v4 && !v6 {
		return true
	}
	return false
}

func firstIP(ctx context.Context, v4, v6 bool) (net.IP, string, error) {
	client := &http.Client{
		Timeout: 4 * time.Second, // per-request safety; overall is controlled by ctx
	}
	type result struct {
		ip  net.IP
		src string
		err error
	}
	ch := make(chan result, len(providers))

	for _, url := range providers {
		url := url // capture
		go func() {
			ip, err := fetchIP(ctx, client, url)
			if err != nil {
				ch <- result{err: err, src: url}
				return
			}
			if !isFamily(ip, v4, v6) {
				ch <- result{err: errors.New("ip family mismatch"), src: url}
				return
			}
			ch <- result{ip: ip, src: url}
		}()
	}

	var firstErr error
	for i := 0; i < len(providers); i++ {
		select {
		case <-ctx.Done():
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			return nil, "", firstErr
		case r := <-ch:
			if r.err == nil && r.ip != nil {
				return r.ip, r.src, nil
			}
			if firstErr == nil {
				firstErr = r.err
			}
		}
	}
	if firstErr == nil {
		firstErr = errors.New("no providers returned a valid IP")
	}
	return nil, "", firstErr
}

// DB schema helpers
func ensureTables(ctx context.Context, dbname string) error {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS public.public_ip_history (
           ip inet PRIMARY KEY,
           first_use_at timestamptz NOT NULL DEFAULT now(),
           last_use_at timestamptz
         )`,
		`CREATE TABLE IF NOT EXISTS public.dns_targets (
           fqdn text PRIMARY KEY,
           enabled boolean NOT NULL DEFAULT true
         )`,
		`CREATE TABLE IF NOT EXISTS public.dns_history (
           fqdn text NOT NULL,
           ip inet NOT NULL,
           first_use_at timestamptz NOT NULL DEFAULT now(),
           last_use_at timestamptz,
           PRIMARY KEY (fqdn, ip)
         )`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func seedDefaultTargets(ctx context.Context, dbname string, zoneName, host string) error {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	targets := []string{host, "*.stage." + zoneName, "*.dev." + zoneName}
	for _, fq := range targets {
		if _, err := db.ExecContext(ctx, `INSERT INTO public.dns_targets (fqdn, enabled) VALUES ($1, true)
          ON CONFLICT (fqdn) DO NOTHING`, fq); err != nil {
			return err
		}
	}
	return nil
}

func currentDNSIP(ctx context.Context, dbname, fqdn string) (string, error) {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return "", err
	}
	defer db.Close()
	row := db.QueryRowContext(ctx, `SELECT ip::text FROM public.dns_history WHERE fqdn=$1 AND last_use_at IS NULL ORDER BY first_use_at DESC LIMIT 1`, fqdn)
	var ip string
	if err := row.Scan(&ip); err != nil {
		return "", err
	}
	if i := strings.Index(ip, "/"); i > 0 {
		ip = ip[:i]
	}
	return ip, nil
}

func setCurrentDNSIP(ctx context.Context, dbname, fqdn, ip string) error {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.dns_history SET last_use_at = now() WHERE fqdn=$1 AND last_use_at IS NULL AND ip <> $2::inet`, fqdn, ip); err != nil {
		_ = tx.Rollback()
		return err
	}
	ins := `INSERT INTO public.dns_history (fqdn, ip, first_use_at, last_use_at)
            VALUES ($1, $2::inet, now(), NULL)
            ON CONFLICT (fqdn, ip) DO UPDATE SET last_use_at = EXCLUDED.last_use_at, first_use_at = LEAST(public.dns_history.first_use_at, EXCLUDED.first_use_at)`
	if _, err := tx.ExecContext(ctx, ins, fqdn, ip); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func listEnabledTargets(ctx context.Context, dbname string) ([]string, error) {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT fqdn FROM public.dns_targets WHERE enabled = true ORDER BY fqdn`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func main() {
	var (
		ipv4           bool
		ipv6           bool
		timeout        time.Duration
		showSrc        bool
		store          bool
		dbname         string
		syncCF         bool
		cfHost         string
		cfTimeout      time.Duration
		collectCF      bool
		initDNSTargets bool
		forceSync      bool
		dbTimeout      time.Duration
	)
	flag.BoolVar(&ipv4, "ipv4", false, "prefer IPv4 only")
	flag.BoolVar(&ipv6, "ipv6", false, "prefer IPv6 only")
	flag.DurationVar(&timeout, "timeout", 3*time.Second, "overall timeout")
	flag.BoolVar(&showSrc, "v", false, "print provider source to stderr")
	flag.BoolVar(&store, "store", false, "store result in database (uses dbconf)")
	flag.StringVar(&dbname, "db", "", "override database name (default from config)")
	flag.BoolVar(&syncCF, "sync-cf", false, "sync Cloudflare DNS A records to the current stored IP using DB targets and history")
	// Backward-compat alias (deprecated): --check-cf
	var deprecatedCheckCF bool
	flag.BoolVar(&deprecatedCheckCF, "check-cf", false, "DEPRECATED: use --sync-cf")
	flag.StringVar(&cfHost, "cf-host", "brain.portnumber53.com", "Cloudflare hostname to check/update")
	flag.DurationVar(&cfTimeout, "cf-timeout", 20*time.Second, "timeout for Cloudflare API operations")
	flag.DurationVar(&dbTimeout, "db-timeout", 20*time.Second, "timeout for database operations")
	flag.BoolVar(&collectCF, "collect-cf", false, "collect current Cloudflare DNS A records for targets and store in DB history")
	flag.BoolVar(&initDNSTargets, "init-dns-targets", false, "seed default DNS targets into DB")
	flag.BoolVar(&forceSync, "force", false, "force Cloudflare update even if DB history matches desired IP")
	flag.Parse()

	// Ensure tables if doing DB-related actions
	if store || syncCF || deprecatedCheckCF || collectCF || initDNSTargets {
		// Resolve DB name
		if strings.TrimSpace(dbname) == "" {
			d, err := dbconf.DefaultDBName()
			if err != nil {
				fmt.Fprintln(os.Stderr, "db error: cannot determine default db:", err)
				os.Exit(1)
			}
			dbname = d
		}
		dbCtx, cancelDB := context.WithTimeout(context.Background(), dbTimeout)
		defer cancelDB()
		if err := ensureTables(dbCtx, dbname); err != nil {
			fmt.Fprintln(os.Stderr, "db error: ensure tables:", err)
			os.Exit(1)
		}
	}

	if initDNSTargets {
		dot := strings.Index(cfHost, ".")
		if dot <= 0 || dot >= len(cfHost)-1 {
			fmt.Fprintln(os.Stderr, "cf error: invalid cf-host")
			os.Exit(2)
		}
		zoneName := cfHost[dot+1:]
		dbCtx, cancelDB := context.WithTimeout(context.Background(), dbTimeout)
		defer cancelDB()
		if err := seedDefaultTargets(dbCtx, dbname, zoneName, cfHost); err != nil {
			fmt.Fprintln(os.Stderr, "db error: seed targets:", err)
			os.Exit(1)
		}
	}

	if ipv4 && ipv6 {
		fmt.Fprintln(os.Stderr, "cannot set both -ipv4 and -ipv6")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ip, src, err := firstIP(ctx, ipv4, ipv6)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if showSrc {
		fmt.Fprintf(os.Stderr, "source: %s\n", src)
	}
	// Always print to stdout for CLI use
	fmt.Println(ip.String())

	if store {
		// Resolve DB name
		// Connect and write
		dbCtx, cancelDB := context.WithTimeout(context.Background(), dbTimeout)
		defer cancelDB()
		db, err := dbconf.ConnectDBAs(dbname)
		if err != nil {
			fmt.Fprintln(os.Stderr, "store error: connect:", err)
			os.Exit(1)
		}
		defer db.Close()
		tx, err := db.BeginTx(dbCtx, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "store error: begin:", err)
			os.Exit(1)
		}
		// Close previous current IP (if any) when it differs
		if _, err := tx.ExecContext(dbCtx, "UPDATE public.public_ip_history SET last_use_at = now() WHERE last_use_at IS NULL AND ip <> $1::inet", ip.String()); err != nil {
			_ = tx.Rollback()
			fmt.Fprintln(os.Stderr, "store error: update previous:", err)
			os.Exit(1)
		}
		// Upsert current IP with NULL last_use_at; preserve earliest first_use_at
		ins := `INSERT INTO public.public_ip_history (ip, first_use_at, last_use_at)
VALUES ($1::inet, now(), NULL)
ON CONFLICT (ip) DO UPDATE SET
  last_use_at = EXCLUDED.last_use_at,
  first_use_at = LEAST(public.public_ip_history.first_use_at, EXCLUDED.first_use_at)`
		if _, err := tx.ExecContext(dbCtx, ins, ip.String()); err != nil {
			_ = tx.Rollback()
			fmt.Fprintln(os.Stderr, "store error: upsert:", err)
			os.Exit(1)
		}
		if err := tx.Commit(); err != nil {
			fmt.Fprintln(os.Stderr, "store error: commit:", err)
			os.Exit(1)
		}
	}

	// Collect current CF DNS and store in DB
	if collectCF {
		token := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_KEY"))
		if token == "" {
			fmt.Fprintln(os.Stderr, "cf error: CLOUDFLARE_API_KEY not set")
			os.Exit(2)
		}
		dot := strings.Index(cfHost, ".")
		if dot <= 0 || dot >= len(cfHost)-1 {
			fmt.Fprintln(os.Stderr, "cf error: invalid cf-host")
			os.Exit(2)
		}
		zoneName := cfHost[dot+1:]
		cfCtx, cancelCF := context.WithTimeout(context.Background(), cfTimeout)
		defer cancelCF()
		zID, err := cfFindZoneID(cfCtx, token, zoneName)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cf error: zone lookup:", err)
			os.Exit(1)
		}
		dbCtx, cancelDB := context.WithTimeout(context.Background(), dbTimeout)
		defer cancelDB()
		targets, err := listEnabledTargets(dbCtx, dbname)
		if err != nil {
			fmt.Fprintln(os.Stderr, "db error: list targets:", err)
			os.Exit(1)
		}
		for _, fq := range targets {
			rec, err := cfGetARecord(cfCtx, token, zID, fq)
			if err != nil {
				fmt.Fprintln(os.Stderr, "cf error: get record:", fq, err)
				os.Exit(1)
			}
			if rec != nil {
				if err := setCurrentDNSIP(dbCtx, dbname, fq, strings.TrimSpace(rec.Content)); err != nil {
					fmt.Fprintln(os.Stderr, "db error: set dns ip:", fq, err)
					os.Exit(1)
				}
			}
		}
	}

	if syncCF || deprecatedCheckCF {
		if strings.TrimSpace(dbname) == "" {
			d, err := dbconf.DefaultDBName()
			if err != nil {
				fmt.Fprintln(os.Stderr, "cf error: cannot determine default db:", err)
				os.Exit(1)
			}
			dbname = d
		}
		currentIP, err := getCurrentStoredIP(ctx, dbname)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cf error: cannot get current stored ip:", err)
			os.Exit(1)
		}
		token := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_KEY"))
		if token == "" {
			fmt.Fprintln(os.Stderr, "cf error: CLOUDFLARE_API_KEY not set")
			os.Exit(2)
		}
		dot := strings.Index(cfHost, ".")
		if dot <= 0 || dot >= len(cfHost)-1 {
			fmt.Fprintln(os.Stderr, "cf error: invalid cf-host")
			os.Exit(2)
		}
		zoneName := cfHost[dot+1:]
		cfCtx, cancelCF := context.WithTimeout(context.Background(), cfTimeout)
		defer cancelCF()
		zID, err := cfFindZoneID(cfCtx, token, zoneName)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cf error: zone lookup:", err)
			os.Exit(1)
		}
		// Read desired targets from DB
		dbCtx, cancelDB := context.WithTimeout(context.Background(), dbTimeout)
		defer cancelDB()
		targets, err := listEnabledTargets(dbCtx, dbname)
		if err != nil {
			fmt.Fprintln(os.Stderr, "db error: list targets:", err)
			os.Exit(1)
		}
		changed := false
		for _, fq := range targets {
			records, err := cfGetARecords(cfCtx, token, zID, fq)
			if err != nil {
				fmt.Fprintln(os.Stderr, "cf error: list records:", fq, err)
				os.Exit(1)
			}
			var rec *cfDNSRecord
			// Determine need from DB unless force is set
			needUpdate := forceSync
			if !needUpdate {
				// Preferred: compare DB-recorded current DNS IP for fqdn
				if cfip, e := currentDNSIP(dbCtx, dbname, fq); e == nil {
					needUpdate = strings.TrimSpace(cfip) != currentIP
				} else {
					// Fallback to live query if no DB record
					rec, err = cfGetARecord(cfCtx, token, zID, fq)
					if err != nil {
						fmt.Fprintln(os.Stderr, "cf error: get record:", fq, err)
						os.Exit(1)
					}
					needUpdate = rec == nil || strings.TrimSpace(rec.Content) != currentIP
				}
			} else {
				// If forcing and no existing rec loaded, fetch to get ID for PATCH
				rec, _ = cfGetARecord(cfCtx, token, zID, fq)
			}
			if needUpdate {
				// Retry up to 3 times with exponential backoff to avoid transient timeouts
				upErr := cfDoWithRetry(cfCtx, func() string {
					if rec == nil {
						return http.MethodPost
					}
					return http.MethodPatch
				}(),
					func() string {
						if rec == nil {
							return "https://api.cloudflare.com/client/v4/zones/" + zID + "/dns_records"
						}
						return "https://api.cloudflare.com/client/v4/zones/" + zID + "/dns_records/" + rec.ID
					}(), token, map[string]any{"type": "A", "name": fq, "content": currentIP, "ttl": 300, "proxied": false}, nil, 3, 500*time.Millisecond)
				if upErr != nil {
					fmt.Fprintln(os.Stderr, "cf error: update record:", fq, upErr)
					os.Exit(1)
				}
				// Reflect the change in DB history
				if err := setCurrentDNSIP(dbCtx, dbname, fq, currentIP); err != nil {
					fmt.Fprintln(os.Stderr, "db error: set dns ip:", fq, err)
					os.Exit(1)
				}
				changed = true
			}
			for _, existing := range records {
				if strings.TrimSpace(existing.Content) == currentIP {
					continue
				}
				if err := cfDeleteDNSRecord(cfCtx, token, zID, existing.ID); err != nil {
					fmt.Fprintln(os.Stderr, "cf error: delete stale record:", fq, existing.ID, err)
					os.Exit(1)
				}
				changed = true
			}
		}
		if changed {
			fmt.Fprintln(os.Stderr, "cf: records updated")
		} else {
			fmt.Fprintln(os.Stderr, "cf: records already current")
		}
	}
}

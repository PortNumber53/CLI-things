package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"cli-things/utility/dbconf"
)

type cfListResp[T any] struct {
	Success    bool `json:"success"`
	Result     []T  `json:"result"`
	Errors     any  `json:"errors"`
	ResultInfo struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Count   int `json:"count"`
		Total   int `json:"total"`
	} `json:"result_info"`
}

type cfAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfZone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied *bool  `json:"proxied"`
	ZoneID  string `json:"zone_id"`
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

func runMigrations(ctx context.Context, dbname string) error {
	migs := []dbconf.Migration{
		{
			ID: "20251104_0001_create_cf_accounts",
			SQL: `CREATE TABLE IF NOT EXISTS public.cloudflare_accounts (
				id text PRIMARY KEY,
				name text NOT NULL,
				fetched_at timestamptz NOT NULL DEFAULT now(),
				raw jsonb NOT NULL
			)`,
		},
		{
			ID: "20251104_0002_create_cf_zones",
			SQL: `CREATE TABLE IF NOT EXISTS public.cloudflare_zones (
				id text PRIMARY KEY,
				account_id text,
				name text NOT NULL,
				status text,
				fetched_at timestamptz NOT NULL DEFAULT now(),
				raw jsonb NOT NULL
			)`,
		},
		{
			ID: "20251104_0003_create_cf_dns_records",
			SQL: `CREATE TABLE IF NOT EXISTS public.cloudflare_dns_records (
				zone_id text NOT NULL,
				id text NOT NULL,
				name text NOT NULL,
				type text NOT NULL,
				content text,
				ttl integer,
				proxied boolean,
				fetched_at timestamptz NOT NULL DEFAULT now(),
				raw jsonb NOT NULL,
				PRIMARY KEY (zone_id, id)
			)`,
		},
		{
			ID: "20251104_0004_create_cf_backup_runs",
			SQL: `CREATE TABLE IF NOT EXISTS public.cloudflare_backup_runs (
				id bigserial PRIMARY KEY,
				run_at timestamptz NOT NULL DEFAULT now(),
				accounts_collected integer NOT NULL DEFAULT 0,
				zones_collected integer NOT NULL DEFAULT 0,
				records_collected integer NOT NULL DEFAULT 0,
				success boolean NOT NULL DEFAULT true,
				error text
			)`,
		},
	}
	return dbconf.ApplyMigrations(ctx, dbname, migs)
}

func insertAccount(ctx context.Context, dbname string, acct json.RawMessage) error {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	var parsed cfAccount
	if err := json.Unmarshal(acct, &parsed); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO public.cloudflare_accounts (id, name, fetched_at, raw)
		VALUES ($1, $2, now(), $3::jsonb)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, fetched_at = EXCLUDED.fetched_at, raw = EXCLUDED.raw`, parsed.ID, parsed.Name, string(acct))
	return err
}

func insertZone(ctx context.Context, dbname string, acctID string, zone json.RawMessage) error {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	var parsed cfZone
	if err := json.Unmarshal(zone, &parsed); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO public.cloudflare_zones (id, account_id, name, status, fetched_at, raw)
		VALUES ($1, $2, $3, $4, now(), $5::jsonb)
		ON CONFLICT (id) DO UPDATE SET account_id = EXCLUDED.account_id, name = EXCLUDED.name, status = EXCLUDED.status, fetched_at = EXCLUDED.fetched_at, raw = EXCLUDED.raw`, parsed.ID, acctID, parsed.Name, parsed.Status, string(zone))
	return err
}

func insertDNSRecord(ctx context.Context, dbname string, zoneID string, rec json.RawMessage) error {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	var parsed cfDNSRecord
	if err := json.Unmarshal(rec, &parsed); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO public.cloudflare_dns_records (zone_id, id, name, type, content, ttl, proxied, fetched_at, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8::jsonb)
		ON CONFLICT (zone_id, id) DO UPDATE SET name = EXCLUDED.name, type = EXCLUDED.type, content = EXCLUDED.content, ttl = EXCLUDED.ttl, proxied = EXCLUDED.proxied, fetched_at = EXCLUDED.fetched_at, raw = EXCLUDED.raw`, zoneID, parsed.ID, parsed.Name, parsed.Type, parsed.Content, parsed.TTL, parsed.Proxied, string(rec))
	return err
}

func recordRun(ctx context.Context, dbname string, accounts, zones, records int, success bool, errMsg string) {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cf-backup: run record error:", err)
		return
	}
	defer db.Close()
	_, _ = db.ExecContext(ctx, `INSERT INTO public.cloudflare_backup_runs (run_at, accounts_collected, zones_collected, records_collected, success, error)
		VALUES (now(), $1, $2, $3, $4, $5)`, accounts, zones, records, success, errMsg)
}

func main() {
	var dbname string
	var timeout time.Duration
	flag.StringVar(&dbname, "db", "", "database name (default from dbconf)")
	flag.DurationVar(&timeout, "timeout", 45*time.Second, "overall timeout for Cloudflare backup")
	flag.Parse()

	token := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_KEY"))
	if token == "" {
		fmt.Fprintln(os.Stderr, "cf-backup: CLOUDFLARE_API_KEY not set")
		os.Exit(2)
	}
	if strings.TrimSpace(dbname) == "" {
		d, err := dbconf.DefaultDBName()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cf-backup: cannot determine default db:", err)
			os.Exit(1)
		}
		dbname = d
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// try shared migrations dir first (if present)
	_ = dbconf.ApplyMigrationsFromDir(ctx, dbname, "./migrations")
	if err := runMigrations(ctx, dbname); err != nil {
		fmt.Fprintln(os.Stderr, "cf-backup: migrations failed:", err)
		os.Exit(1)
	}

	accounts := 0
	zones := 0
	records := 0
	var runErr string
	success := true
	defer func() {
		recordRun(context.Background(), dbname, accounts, zones, records, success, runErr)
	}()

	// 1) accounts
	var acctResp cfListResp[json.RawMessage]
	if err := cfDo(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4/accounts", token, nil, &acctResp); err != nil {
		success = false
		runErr = err.Error()
		fmt.Fprintln(os.Stderr, "cf-backup: accounts list failed:", err)
		return
	}
	for _, rawAcct := range acctResp.Result {
		if err := insertAccount(ctx, dbname, rawAcct); err != nil {
			success = false
			runErr = err.Error()
			fmt.Fprintln(os.Stderr, "cf-backup: insert account failed:", err)
			return
		}
		accounts++
	}

	// 2) zones (paginated)
	page := 1
	for {
		var zResp cfListResp[json.RawMessage]
		url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?page=%d&per_page=50", page)
		if err := cfDo(ctx, http.MethodGet, url, token, nil, &zResp); err != nil {
			success = false
			runErr = err.Error()
			fmt.Fprintln(os.Stderr, "cf-backup: zones list failed:", err)
			return
		}
		if !zResp.Success {
			success = false
			runErr = "cloudflare zones api returned unsuccessful"
			fmt.Fprintln(os.Stderr, "cf-backup: zones api unsuccessful")
			return
		}
		if len(zResp.Result) == 0 {
			break
		}
		for _, rawZone := range zResp.Result {
			var zoneObj cfZone
			if err := json.Unmarshal(rawZone, &zoneObj); err != nil {
				success = false
				runErr = err.Error()
				fmt.Fprintln(os.Stderr, "cf-backup: zone unmarshal failed:", err)
				return
			}
			if err := insertZone(ctx, dbname, "", rawZone); err != nil {
				success = false
				runErr = err.Error()
				fmt.Fprintln(os.Stderr, "cf-backup: insert zone failed:", err)
				return
			}
			zones++
			// 3) records per zone (paginated)
			recPage := 1
			for {
				var rResp cfListResp[json.RawMessage]
				recURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?page=%d&per_page=100", zoneObj.ID, recPage)
				if err := cfDo(ctx, http.MethodGet, recURL, token, nil, &rResp); err != nil {
					success = false
					runErr = err.Error()
					fmt.Fprintln(os.Stderr, "cf-backup: records list failed:", err)
					return
				}
				if len(rResp.Result) == 0 {
					break
				}
				for _, rawRec := range rResp.Result {
					if err := insertDNSRecord(ctx, dbname, zoneObj.ID, rawRec); err != nil {
						success = false
						runErr = err.Error()
						fmt.Fprintln(os.Stderr, "cf-backup: insert record failed:", err)
						return
					}
					records++
				}
				recPage++
			}
		}
		page++
	}

	fmt.Fprintf(os.Stderr, "cf-backup: done (accounts=%d zones=%d records=%d)\n", accounts, zones, records)
}

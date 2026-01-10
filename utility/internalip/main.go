package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"cli-things/utility/dbconf"
)

// InternalIPInfo represents information about an internal IP address
type InternalIPInfo struct {
	IP         string    `json:"ip"`
	Interface  string    `json:"interface"`
	IsIPv6     bool      `json:"is_ipv6"`
	Hostname   string    `json:"hostname"`
	Timestamp  time.Time `json:"timestamp"`
	MACAddress string    `json:"mac_address,omitempty"`
}

// DeviceInfo represents information about the device
type DeviceInfo struct {
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	User       string `json:"user,omitempty"`
}

func getHostname() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return hostname, nil
}

func getDeviceInfo() DeviceInfo {
	hostname, _ := getHostname()
	if hostname == "" {
		hostname = "unknown"
	}

	return DeviceInfo{
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		User:     os.Getenv("USER"),
	}
}

// getInternalIPs retrieves all non-loopback internal IP addresses
func getInternalIPs() ([]InternalIPInfo, error) {
	var ips []InternalIPInfo

	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get network interfaces: %w", err)
	}

	hostname, _ := getHostname()
	if hostname == "" {
		hostname = "unknown"
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}

			// Skip loopback and link-local addresses
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}

			ipInfo := InternalIPInfo{
				IP:        ip.String(),
				Interface: iface.Name,
				IsIPv6:    ip.To4() == nil,
				Hostname:  hostname,
				Timestamp: time.Now(),
			}

			// Add MAC address if available
			if mac := iface.HardwareAddr; len(mac) > 0 {
				ipInfo.MACAddress = mac.String()
			}

			ips = append(ips, ipInfo)
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("no internal IP addresses found")
	}

	return ips, nil
}

// getPreferredInternalIP returns the "best" internal IP for typical use
func getPreferredInternalIP(preferIPv6 bool) (*InternalIPInfo, error) {
	ips, err := getInternalIPs()
	if err != nil {
		return nil, err
	}

	var bestIP *InternalIPInfo

	for _, ip := range ips {
		// Skip IPv6 if not preferred and IPv4 is available
		if !preferIPv6 && ip.IsIPv6 {
			continue
		}

		// Prefer non-IPv6 if IPv4 is preferred
		if preferIPv6 && !ip.IsIPv6 {
			continue
		}

		// Common interface preferences
		if strings.Contains(ip.Interface, "en0") ||
		   strings.Contains(ip.Interface, "eth0") ||
		   strings.Contains(ip.Interface, "wlan0") ||
		   strings.Contains(ip.Interface, "wifi") {
			bestIP = &ip
			break
		}

		// If no preferred interface found, use first valid one
		if bestIP == nil {
			bestIP = &ip
		}
	}

	if bestIP == nil && len(ips) > 0 {
		bestIP = &ips[0]
	}

	return bestIP, nil
}

func storeInternalIP(ctx context.Context, dbname string, ipInfo InternalIPInfo) error {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Close previous current IP for this hostname and interface
	if _, err := tx.ExecContext(ctx,
		`UPDATE public.internal_ip_history SET last_use_at = now()
		 WHERE hostname = $1 AND interface_name = $2 AND last_use_at IS NULL AND ip <> $3::inet`,
		ipInfo.Hostname, ipInfo.Interface, ipInfo.IP); err != nil {
		return fmt.Errorf("failed to update previous IP: %w", err)
	}

	// Upsert current IP
	ins := `INSERT INTO public.internal_ip_history
		(hostname, interface_name, ip, is_ipv6, mac_address, first_use_at, last_use_at)
		VALUES ($1, $2, $3::inet, $4, $5, now(), NULL)
		ON CONFLICT (hostname, interface_name, ip) DO UPDATE SET
			last_use_at = EXCLUDED.last_use_at,
			first_use_at = LEAST(public.internal_ip_history.first_use_at, EXCLUDED.first_use_at)`

	if _, err := tx.ExecContext(ctx, ins,
		ipInfo.Hostname, ipInfo.Interface, ipInfo.IP, ipInfo.IsIPv6, ipInfo.MACAddress); err != nil {
		return fmt.Errorf("failed to upsert IP: %w", err)
	}

	return tx.Commit()
}

func listStoredIPs(ctx context.Context, dbname string, hostname string) ([]InternalIPInfo, error) {
	db, err := dbconf.ConnectDBAs(dbname)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	query := `SELECT hostname, interface_name, ip::text, is_ipv6, mac_address, first_use_at
			  FROM public.internal_ip_history
			  WHERE last_use_at IS NULL`
	args := []interface{}{}

	if hostname != "" {
		query += " AND hostname = $1"
		args = append(args, hostname)
	}

	query += " ORDER BY hostname, interface_name"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query stored IPs: %w", err)
	}
	defer rows.Close()

	var ips []InternalIPInfo
	for rows.Next() {
		var ip InternalIPInfo
		var firstUseAt time.Time

		err := rows.Scan(&ip.Hostname, &ip.Interface, &ip.IP, &ip.IsIPv6, &ip.MACAddress, &firstUseAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		ip.Timestamp = firstUseAt
		ips = append(ips, ip)
	}

	return ips, rows.Err()
}

func main() {
	var (
		ipv6          bool
		timeout       time.Duration
		showAll       bool
		store         bool
		dbname        string
		list          bool
		hostname      string
		jsonOutput    bool
		dbTimeout     time.Duration
		interfaceName string
	)

	flag.BoolVar(&ipv6, "ipv6", false, "prefer IPv6 addresses")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "overall timeout")
	flag.BoolVar(&showAll, "all", false, "show all internal IPs instead of preferred one")
	flag.BoolVar(&store, "store", false, "store result in database (uses dbconf)")
	flag.StringVar(&dbname, "db", "", "override database name (default from config)")
	flag.BoolVar(&list, "list", false, "list stored IPs from database")
	flag.StringVar(&hostname, "hostname", "", "filter by hostname (for -list)")
	flag.BoolVar(&jsonOutput, "json", false, "output in JSON format")
	flag.DurationVar(&dbTimeout, "db-timeout", 20*time.Second, "timeout for database operations")
	flag.StringVar(&interfaceName, "interface", "", "prefer specific interface name")

	flag.Parse()

	// Setup context
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Handle database operations
	if store || list {
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

		// Apply migrations
		if err := dbconf.ApplyConfiguredMigrations(dbCtx, dbname); err != nil {
			fmt.Fprintln(os.Stderr, "db error: migrations failed:", err)
			os.Exit(1)
		}
	}

	// List stored IPs
	if list {
		ips, err := listStoredIPs(ctx, dbname, hostname)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error listing stored IPs:", err)
			os.Exit(1)
		}

		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(ips); err != nil {
				fmt.Fprintln(os.Stderr, "error encoding JSON:", err)
				os.Exit(1)
			}
		} else {
			for _, ip := range ips {
				fmt.Printf("%s\t%s\t%s\t%s", ip.Hostname, ip.Interface, ip.IP, ip.Timestamp.Format(time.RFC3339))
				if ip.MACAddress != "" {
					fmt.Printf("\t%s", ip.MACAddress)
				}
				fmt.Println()
			}
		}
		return
	}

	// Get internal IPs
	var ips []InternalIPInfo
	var err error

	if showAll {
		ips, err = getInternalIPs()
	} else {
		preferredIP, err := getPreferredInternalIP(ipv6)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		ips = []InternalIPInfo{*preferredIP}
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// Filter by interface if specified
	if interfaceName != "" {
		var filtered []InternalIPInfo
		for _, ip := range ips {
			if ip.Interface == interfaceName {
				filtered = append(filtered, ip)
			}
		}
		ips = filtered
		if len(ips) == 0 {
			fmt.Fprintln(os.Stderr, "error: no IPs found for interface", interfaceName)
			os.Exit(1)
		}
	}

	// Output
	if jsonOutput {
		if showAll {
			output := map[string]interface{}{
				"device": getDeviceInfo(),
				"ips":    ips,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(output); err != nil {
				fmt.Fprintln(os.Stderr, "error encoding JSON:", err)
				os.Exit(1)
			}
		} else {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(ips[0]); err != nil {
				fmt.Fprintln(os.Stderr, "error encoding JSON:", err)
				os.Exit(1)
			}
		}
	} else {
		if showAll {
			deviceInfo := getDeviceInfo()
			fmt.Printf("# Device: %s (%s/%s) User: %s\n", deviceInfo.Hostname, deviceInfo.OS, deviceInfo.Arch, deviceInfo.User)
			fmt.Println("# Interface\tIP Address\tIPv6\tMAC Address\tTimestamp")
			for _, ip := range ips {
				ipv6Flag := "No"
				if ip.IsIPv6 {
					ipv6Flag = "Yes"
				}
				mac := ip.MACAddress
				if mac == "" {
					mac = "N/A"
				}
				fmt.Printf("%s\t%s\t%s\t%s\t%s\n", ip.Interface, ip.IP, ipv6Flag, mac, ip.Timestamp.Format(time.RFC3339))
			}
		} else {
			// Simple output for scripting
			fmt.Println(ips[0].IP)
		}
	}

	// Store in database
	if store {
		dbCtx, cancelDB := context.WithTimeout(context.Background(), dbTimeout)
		defer cancelDB()

		for _, ip := range ips {
			if err := storeInternalIP(dbCtx, dbname, ip); err != nil {
				fmt.Fprintln(os.Stderr, "store error:", err)
				os.Exit(1)
			}
		}
		fmt.Fprintf(os.Stderr, "Stored %d IP address(es) for hostname %s\n", len(ips), ips[0].Hostname)
	}
}

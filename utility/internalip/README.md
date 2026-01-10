# internalip

A CLI utility for capturing and storing internal IP addresses from laptops and other devices. This tool mimics the structure of the `publicip` utility but focuses on internal network addressing.

## Features

- Automatically discovers all non-loopback internal IP addresses
- Supports both IPv4 and IPv6 addresses
- Identifies network interfaces and MAC addresses
- Stores IP history in PostgreSQL database
- JSON output support for integration with other tools
- Device information collection (hostname, OS, architecture)

## Usage

### Basic Usage

```bash
# Get the preferred internal IP (simple output for scripting)
go run utility/internalip/main.go

# Get all internal IPs with detailed information
go run utility/internalip/main.go -all

# Prefer IPv6 addresses
go run utility/internalip/main.go -ipv6

# Output in JSON format
go run utility/internalip/main.go -json

# Get all IPs in JSON format with device info
go run utility/internalip/main.go -all -json
```

### Database Operations

```bash
# Store current IP in database
go run utility/internalip/main.go -store

# Store all internal IPs
go run utility/internalip/main.go -all -store

# List stored IPs from database
go run utility/internalip/main.go -list

# List IPs for specific hostname
go run utility/internalip/main.go -list -hostname=my-laptop

# List stored IPs in JSON format
go run utility/internalip/main.go -list -json
```

### Interface Selection

```bash
# Get IP for specific interface
go run utility/internalip/main.go -interface=en0

# Store IP for specific interface
go run utility/internalip/main.go -interface=wlan0 -store
```

## Configuration

The tool uses the same configuration system as other CLI utilities:

1. **Environment Variables**: Set `DBTOOL_CONFIG_FILE` to specify config location
2. **.env Files**: Automatically discovered from current directory up to git root
3. **config.ini**: Database connection settings

Example `config.ini`:
```ini
[default]
DB_HOST=localhost
DB_PORT=5432
DB_NAME=yourdbname
DB_USER=youruser
DB_PASSWORD=yourpassword
DB_SSLMODE=disable
DB_MIGRATIONS_DIR=/path/to/migrations
```

## Database Schema

The tool creates an `internal_ip_history` table that tracks:

- **hostname**: Device hostname
- **interface_name**: Network interface (e.g., en0, wlan0)
- **ip**: IP address (stored as INET type)
- **is_ipv6**: Boolean flag for IPv6 addresses
- **mac_address**: Hardware MAC address (when available)
- **first_use_at**: When this IP was first seen
- **last_use_at**: When this IP was last active (NULL for current IPs)

The migration file is located at `migrations/20251104_0003_internal_ip_history.sql` and will be automatically applied when using the `-store` flag.

## Automation Setup

### Systemd Timer

Create `/etc/systemd/system/internalip-capture.service`:
```ini
[Unit]
Description=Capture Internal IP Address
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/internalip -store
User=youruser
WorkingDirectory=/path/to/CLI-things
Environment=GOOS=linux
```

Create `/etc/systemd/system/internalip-capture.timer`:
```ini
[Unit]
Description=Run internal IP capture every 5 minutes
Requires=internalip-capture.service

[Timer]
OnCalendar=*:0/5
Persistent=true

[Install]
WantedBy=timers.target
```

Enable and start:
```bash
sudo systemctl enable internalip-capture.timer
sudo systemctl start internalip-capture.timer
```

### Cron Job

Add to crontab (`crontab -e`):
```bash
# Capture internal IP every 5 minutes
*/5 * * * * cd /path/to/CLI-things && go run utility/internalip/main.go -store

# Or with compiled binary
*/5 * * * * /usr/local/bin/internalip -store
```

### Ansible Integration

Example Ansible task:
```yaml
- name: Capture internal IP
  command: go run utility/internalip/main.go -all -json
  args:
    chdir: /path/to/CLI-things
  register: internal_ip_result

- name: Parse internal IP data
  set_fact:
    internal_ips: "{{ internal_ip_result.stdout | from_json }}"

- name: Update inventory with internal IP
  add_host:
    name: "{{ internal_ips.device.hostname }}"
    ansible_host: "{{ internal_ips.ips[0].ip }}"
    groups: laptops
  when: internal_ips.ips | length > 0
```

## Output Examples

### Simple Output
```
192.168.1.100
```

### Detailed Output (-all)
```
# Device: macbook-pro (darwin/arm64) User: john
# Interface	IP Address	IPv6	MAC Address	Timestamp
en0	192.168.1.100	No	aa:bb:cc:dd:ee:ff	2024-01-10T11:47:00Z
en1	fe80::1	Yes	aa:bb:cc:dd:ee:ff	2024-01-10T11:47:00Z
```

### JSON Output
```json
{
  "ip": "192.168.1.100",
  "interface": "en0",
  "is_ipv6": false,
  "hostname": "macbook-pro",
  "timestamp": "2024-01-10T11:47:00Z",
  "mac_address": "aa:bb:cc:dd:ee:ff"
}
```

## Building

```bash
# Build binary
go build -o internalip utility/internalip/main.go

# Build with version info
go build -ldflags "-X main.version=1.0.0" -o internalip utility/internalip/main.go
```

## Integration with Infrastructure

The stored IP data can be used for:

- **Dynamic DNS updates** for internal services
- **Ansible inventory** generation
- **Network monitoring** and change tracking
- **Device location** tracking across subnets
- **Security monitoring** for unexpected IP changes

Query examples:
```sql
-- Get current IPs for all devices
SELECT * FROM public.current_internal_ips;

-- Get IP history for a specific device
SELECT * FROM public.internal_ip_history
WHERE hostname = 'my-laptop'
ORDER BY first_use_at DESC;

-- Find devices that recently changed IPs
SELECT hostname, COUNT(*) as ip_changes
FROM public.internal_ip_history
WHERE first_use_at > NOW() - INTERVAL '24 hours'
GROUP BY hostname
HAVING COUNT(*) > 1;
```

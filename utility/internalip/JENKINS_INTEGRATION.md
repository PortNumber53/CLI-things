# Jenkins Integration for internalip Utility

## Overview

The `internalip` utility has been fully integrated into the existing Jenkins pipeline for automated building and deployment across all specified hosts.

## Jenkinsfile Changes

### 1. Installation Targets
Added `internalip` to the `INSTALL_TARGETS` mapping:
```groovy
def INSTALL_TARGETS = [
  'dbtool'           : ['brain', 'crash', 'pinky', 'zenbook'],
  'cloudflare-backup': ['crash'],
  'internalip'       : ['brain', 'crash', 'pinky', 'zenbook'],  // Added
]
```

### 2. Environment Variables
Added build configuration for internalip:
```groovy
INTERNALIP_BINARY_NAME = 'internalip'
INTERNALIP_BUILD_DIR   = 'utility/internalip'
INTERNALIP_BUILD_OUT   = 'bin/internalip'
```

### 3. Build Stage
Added internalip to the build process:
```groovy
sh 'go build -o ${INTERNALIP_BUILD_OUT} ./${INTERNALIP_BUILD_DIR}'
sh 'file ${INTERNALIP_BUILD_OUT} || true'
```

### 4. Primary Deploy Stage
Enhanced the main deployment to include internalip:
- Binary deployment to `/opt/cli-things/bin/internalip`
- Systemd service and timer installation
- Timer activation alongside other utilities

### 5. Dedicated Deploy Stage
Added new `Deploy internalip` stage that:
- Deploys to all hosts: `brain`, `crash`, `pinky`, `zenbook`
- Uses custom SSH ports from `HOST_SSH_PORTS`
- Installs systemd service and timer
- Enables and starts the timer immediately

### 6. Artifacts
Updated archiving to include internalip binary:
```groovy
archiveArtifacts artifacts: 'bin/publicip,bin/internalip', allowEmptyArchive: true
```

## Deployment Targets

The utility will be deployed to the following hosts:

| Host | SSH Port | Deployment Path |
|------|---------|----------------|
| brain | 22040 | `/opt/cli-things/bin/internalip` |
| crash | 22 | `/opt/cli-things/bin/internalip` |
| pinky | 22050 | `/opt/cli-things/bin/internalip` |
| zenbook | 22070 | `/opt/cli-things/bin/internalip` |

## Systemd Integration

### Service: `internalip-capture.service`
- **Type**: oneshot
- **Exec**: `/opt/cli-things/bin/internalip -store`
- **User**: grimlock
- **Security**: Hardened with systemd security features

### Timer: `internalip-capture.timer`
- **Schedule**: Every 5 minutes (`*:0/5`)
- **Random Delay**: 30 seconds
- **Persistence**: Enabled

## Deployment Process

1. **Build Stage**: Compiles internalip binary along with other utilities
2. **Primary Deploy**: Deploys to main host (crash) with full systemd setup
3. **Distributed Deploy**: Deploys to all other hosts with systemd configuration
4. **Timer Activation**: Automatically enables and starts IP capture timers

## Database Integration

The utility automatically applies database migrations when first run:
- Uses centralized migrations in `/migrations/20251104_0003_internal_ip_history.sql`
- Shares database configuration with other utilities
- Applies migrations via `dbconf.ApplyConfiguredMigrations()`

## Monitoring

After deployment, the utility will:
- Capture internal IPs every 5 minutes on all hosts
- Store data in the shared PostgreSQL database
- Maintain history of IP changes per device
- Track interface information and MAC addresses

## Verification

To verify deployment after Jenkins run:

```bash
# Check service status on any host
ssh grimlock@host "sudo systemctl status internalip-capture.timer"

# Check recent logs
ssh grimlock@host "sudo journalctl -u internalip-capture.service --since '1 hour ago'"

# Check stored IPs
ssh grimlock@host "/opt/cli-things/bin/internalip -list"
```

## Rollback

If needed, the utility can be disabled without affecting other tools:

```bash
# Stop and disable timer
sudo systemctl stop internalip-capture.timer
sudo systemctl disable internalip-capture.timer

# Remove binary (optional)
sudo rm /opt/cli-things/bin/internalip
```

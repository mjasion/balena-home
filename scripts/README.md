# Balena Deployment Scripts

This directory contains helper scripts for managing Balena deployments.

## sync-balena-secrets.sh

Synchronizes environment variables (secrets) from YAML/ENV files to Balena fleet or device.

**Recommended**: Use YAML format for structured, service-specific secret management.

### Prerequisites

**Required tools:**

1. Balena CLI:
```bash
npm install -g balena-cli
balena login
```

2. yq (for YAML parsing):
```bash
# macOS
brew install yq

# Linux
snap install yq

# Or download from: https://github.com/mikefarah/yq
```

3. jq (for JSON parsing):
```bash
# macOS
brew install jq

# Linux
apt-get install jq  # or snap install jq
```

### Quick Start

1. **Create your secrets file:**
```bash
cp secrets.example.yaml secrets.yaml
# Edit secrets.yaml with your actual values
```

2. **Sync to Balena (dry-run first):**
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation secrets.yaml --dry-run
```

3. **Apply for real:**
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation secrets.yaml
```

### Usage Examples

#### Sync all services with YAML
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation secrets.yaml
```

#### Sync with production environment overrides
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation secrets.yaml --env production
```

#### Sync only specific service
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation secrets.yaml --service home-controller
```

#### Sync to specific device
```bash
./scripts/sync-balena-secrets.sh a1b2c3d4e5f6 secrets.yaml
```

#### Preview changes without applying (dry-run)
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation secrets.yaml --dry-run --verbose
```

#### Legacy: Sync from .env file
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation .env.prod
```

### How it works

#### YAML Format (Recommended)

**Structure:**
```yaml
shared:
  # Variables without service prefix (global)
  GRAFANA_CLOUD_API_KEY: "your-key"

services:
  home-controller:
    # Prefixed as: home-controller_NETATMO_CLIENT_ID
    NETATMO_CLIENT_ID: "your-id"

  datadog:
    # Prefixed as: datadog_DD_API_KEY
    DD_API_KEY: "your-key"

environments:
  production:
    shared:
      LOG_LEVEL: "info"
    services:
      home-controller:
        THERMOSTAT_CONTROL_DRY_RUN: "false"
```

**Variable Naming in Balena:**
- **Shared secrets**: `VARIABLE_NAME` (no prefix)
- **Service-specific**: `SERVICE_NAME_VARIABLE_NAME`
  - Example: `home-controller_NETATMO_CLIENT_ID`
  - Example: `datadog_DD_API_KEY`

**Benefits:**
- ✅ Organized by service
- ✅ Environment-specific overrides (dev/staging/prod)
- ✅ Comments and structure
- ✅ Validation and type safety
- ✅ Selective sync (--service flag)

#### ENV Format (Legacy)

Standard `.env` file:
```bash
# Comment
PROMETHEUS_PASSWORD=glc_abc123xyz
NETATMO_CLIENT_ID=your-client-id

# Empty and placeholders skipped
PLACEHOLDER=your-value-here
```

All variables use flat naming (no service prefixes).

### Notes

- **Fleet variables**: Apply to all devices in the fleet. Devices need to restart to pick up changes.
- **Device variables**: Apply only to the specific device.
- **Secrets**: Balena encrypts sensitive environment variables automatically.
- **Idempotent**: Running the script multiple times is safe - it updates existing values.

### Balena CLI Commands

For manual management, you can also use these Balena CLI commands:

#### List environment variables
```bash
# Fleet variables
balena env list --fleet <fleet-name>

# Device variables
balena env list --device <device-uuid>
```

#### Set a variable
```bash
# Fleet variable
balena env add MY_VAR "my-value" --fleet <fleet-name>

# Device variable
balena env add MY_VAR "my-value" --device <device-uuid>
```

#### Remove a variable
```bash
# Fleet variable
balena env rm <variable-id> --fleet <fleet-name>

# Device variable
balena env rm <variable-id> --device <device-uuid>
```

### Security Best Practices

1. **Never commit `.env.prod`** to git - add it to `.gitignore`
2. **Rotate secrets regularly** - especially OAuth tokens
3. **Use fleet variables** for shared secrets across devices
4. **Use device variables** for device-specific configurations (e.g., device-specific API keys)
5. **Review variables** periodically: `balena env list --fleet <fleet-name>`

### Troubleshooting

**Error: "Balena CLI is not installed"**
- Install: `npm install -g balena-cli`

**Error: "Not logged in"**
- Run: `balena login`

**Error: "Fleet not found"**
- List your fleets: `balena fleets`
- Use full fleet name: `org/fleet-name`

**Error: "Device not found"**
- List your devices: `balena devices`
- Use full device UUID

**Variables not taking effect**
- Restart the device: `balena device reboot <device-uuid>`
- Or restart specific service: `balena restart <service-name> --device <device-uuid>`

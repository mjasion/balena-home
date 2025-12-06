# Balena Deployment Scripts

This directory contains helper scripts for managing Balena deployments.

## sync-balena-secrets.sh

Synchronizes environment variables (secrets) from a `.env.prod` file to Balena fleet or device.

### Prerequisites

Install Balena CLI:
```bash
npm install -g balena-cli
```

Login to Balena:
```bash
balena login
```

### Usage

#### Sync to entire fleet (all devices)
```bash
./scripts/sync-balena-secrets.sh <org>/<fleet-name> .env.prod
```

Example:
```bash
./scripts/sync-balena-secrets.sh myorg/home-automation .env.prod
```

#### Sync to specific device only
```bash
./scripts/sync-balena-secrets.sh <device-uuid> .env.prod
```

Example:
```bash
./scripts/sync-balena-secrets.sh a1b2c3d4e5f6 .env.prod
```

### How it works

1. Reads `.env.prod` file (or specified file)
2. Parses environment variables (supports `KEY=value` and `export KEY=value`)
3. Skips comments, empty lines, and placeholder values
4. Uses Balena CLI to set each variable on the target fleet/device
5. Provides summary of successes, skips, and errors

### Environment File Format

The script supports standard `.env` file format:

```bash
# This is a comment
PROMETHEUS_PASSWORD=glc_abc123xyz
NETATMO_CLIENT_ID=your-client-id
NETATMO_CLIENT_SECRET=your-secret

# Empty values and placeholders are skipped
EMPTY_VALUE=
PLACEHOLDER=your-value-here
```

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

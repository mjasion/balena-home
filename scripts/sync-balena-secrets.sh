#!/bin/bash
# Sync secrets from .env.prod to Balena device/fleet
# Usage: ./sync-balena-secrets.sh <fleet-name-or-device-uuid> [.env.prod]

set -e

# Check if balena CLI is installed
if ! command -v balena &> /dev/null; then
    echo "Error: Balena CLI is not installed"
    echo "Install with: npm install -g balena-cli"
    exit 1
fi

# Check arguments
if [ -z "$1" ]; then
    echo "Usage: $0 <fleet-name-or-device-uuid> [.env-file]"
    echo ""
    echo "Examples:"
    echo "  $0 myfleet/myapp .env.prod"
    echo "  $0 a1b2c3d4 .env.prod"
    echo ""
    echo "Target can be either:"
    echo "  - Fleet name (e.g., myorg/myfleet) - applies to all devices in fleet"
    echo "  - Device UUID - applies to specific device only"
    exit 1
fi

TARGET="$1"
ENV_FILE="${2:-.env.prod}"

# Check if env file exists
if [ ! -f "$ENV_FILE" ]; then
    echo "Error: Environment file '$ENV_FILE' not found"
    exit 1
fi

echo "==> Reading secrets from: $ENV_FILE"
echo "==> Target: $TARGET"
echo ""

# Determine if target is a fleet or device
if [[ "$TARGET" == *"/"* ]]; then
    TARGET_TYPE="fleet"
    BALENA_CMD="balena env add"
else
    TARGET_TYPE="device"
    BALENA_CMD="balena env add"
fi

# Read .env file and set variables
# Skip comments and empty lines
# Handle both formats: KEY=value and export KEY=value
success_count=0
skip_count=0
error_count=0

while IFS= read -r line || [ -n "$line" ]; do
    # Skip empty lines and comments
    if [[ -z "$line" ]] || [[ "$line" =~ ^[[:space:]]*# ]]; then
        continue
    fi

    # Remove 'export ' prefix if present
    line="${line#export }"

    # Extract key and value
    if [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]]; then
        key="${BASH_REMATCH[1]}"
        value="${BASH_REMATCH[2]}"

        # Remove quotes if present
        value="${value%\"}"
        value="${value#\"}"
        value="${value%\'}"
        value="${value#\'}"

        # Skip if value is empty or placeholder
        if [[ -z "$value" ]] || [[ "$value" == "your-"* ]] || [[ "$value" == "glc_"* ]]; then
            echo "⏭️  Skipping $key (empty or placeholder value)"
            ((skip_count++))
            continue
        fi

        # Set the environment variable
        echo "📝 Setting $key..."
        if $BALENA_CMD "$key" "$value" --$TARGET_TYPE "$TARGET" 2>&1; then
            ((success_count++))
        else
            echo "❌ Failed to set $key"
            ((error_count++))
        fi
    fi
done < "$ENV_FILE"

echo ""
echo "==> Summary:"
echo "    ✅ Successfully set: $success_count"
echo "    ⏭️  Skipped: $skip_count"
echo "    ❌ Errors: $error_count"
echo ""

if [ $error_count -eq 0 ]; then
    echo "✅ All secrets synced successfully!"
    if [[ "$TARGET_TYPE" == "fleet" ]]; then
        echo ""
        echo "Note: Fleet variables apply to all devices in the fleet."
        echo "Devices will need to restart to pick up new values."
    fi
else
    echo "⚠️  Some secrets failed to sync. Check the errors above."
    exit 1
fi

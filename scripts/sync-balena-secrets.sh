#!/bin/bash
# Sync secrets from YAML/TOML/ENV file to Balena device/fleet
# Supports both shared secrets (all services) and service-specific secrets
# Usage: ./sync-balena-secrets.sh <fleet-name-or-device-uuid> <config-file> [options]

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Check if balena CLI is installed
if ! command -v balena &> /dev/null; then
    echo -e "${RED}Error: Balena CLI is not installed${NC}"
    echo "Install with: npm install -g balena-cli"
    exit 1
fi

# Check if yq is installed (for YAML parsing)
if ! command -v yq &> /dev/null; then
    echo -e "${RED}Error: yq is not installed (required for YAML parsing)${NC}"
    echo "Install with: brew install yq (macOS) or snap install yq (Linux)"
    exit 1
fi

# Default values
DRY_RUN=false
SERVICE_FILTER=""
ENVIRONMENT=""
VERBOSE=false

# Show usage
show_usage() {
    cat <<EOF
Usage: $0 <fleet-or-device> <config-file> [options]

Arguments:
  fleet-or-device    Balena fleet name (org/fleet) or device UUID
  config-file        Path to secrets file (.yaml, .yml, .toml, or .env)

Options:
  --dry-run          Preview changes without applying
  --service NAME     Only sync specific service
  --env NAME         Use environment-specific overrides (dev/prod)
  --verbose          Show detailed output
  -h, --help         Show this help message

Examples:
  # Sync all services from YAML
  $0 myorg/home-automation secrets.yaml

  # Sync with production overrides
  $0 myorg/home-automation secrets.yaml --env production

  # Sync only home-controller service
  $0 myorg/home-automation secrets.yaml --service home-controller

  # Dry run to preview changes
  $0 myorg/home-automation secrets.yaml --dry-run

  # Sync from .env file (legacy)
  $0 myorg/home-automation .env.prod

File Format:
  YAML (.yaml, .yml):
    shared:
      VARIABLE: value
    services:
      service-name:
        VARIABLE: value
    environments:
      production:
        shared:
          VARIABLE: override-value

  ENV (.env):
    VARIABLE=value

EOF
}

# Parse arguments
if [ $# -lt 2 ]; then
    show_usage
    exit 1
fi

TARGET="$1"
CONFIG_FILE="$2"
shift 2

while [[ $# -gt 0 ]]; do
    case $1 in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --service)
            SERVICE_FILTER="$2"
            shift 2
            ;;
        --env)
            ENVIRONMENT="$2"
            shift 2
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            show_usage
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            show_usage
            exit 1
            ;;
    esac
done

# Check if config file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo -e "${RED}Error: Config file not found: $CONFIG_FILE${NC}"
    exit 1
fi

# Detect file type
FILE_EXT="${CONFIG_FILE##*.}"
if [[ "$FILE_EXT" =~ ^(yaml|yml)$ ]]; then
    FILE_TYPE="yaml"
elif [[ "$FILE_EXT" == "toml" ]]; then
    FILE_TYPE="toml"
elif [[ "$FILE_EXT" == "env" ]]; then
    FILE_TYPE="env"
else
    echo -e "${RED}Error: Unsupported file type: $FILE_EXT${NC}"
    echo "Supported: .yaml, .yml, .toml, .env"
    exit 1
fi

echo -e "${BLUE}==> Balena Secret Sync${NC}"
echo -e "${BLUE}==> Target: $TARGET${NC}"
echo -e "${BLUE}==> Config: $CONFIG_FILE ($FILE_TYPE)${NC}"
if [ "$DRY_RUN" = true ]; then
    echo -e "${YELLOW}==> DRY RUN MODE (no changes will be applied)${NC}"
fi
if [ -n "$SERVICE_FILTER" ]; then
    echo -e "${BLUE}==> Service filter: $SERVICE_FILTER${NC}"
fi
if [ -n "$ENVIRONMENT" ]; then
    echo -e "${BLUE}==> Environment: $ENVIRONMENT${NC}"
fi
echo ""

# Determine if target is a fleet or device
if [[ "$TARGET" == *"/"* ]]; then
    TARGET_TYPE="fleet"
else
    TARGET_TYPE="device"
fi

# Counters
total_set=0
total_skip=0
total_error=0

# Function to set an environment variable in Balena
set_balena_var() {
    local var_name="$1"
    local value="$2"
    local source="$3"
    local service_name="${4:-}"  # Optional service name

    # Skip empty or placeholder values
    if [[ -z "$value" ]] || [[ "$value" == "REPLACE_"* ]] || [[ "$value" == "your-"* ]] || [[ "$value" == "example-"* ]]; then
        if [ "$VERBOSE" = true ]; then
            echo -e "${YELLOW}  ⏭️  Skipping $var_name (placeholder/empty)${NC}"
        fi
        ((total_skip++))
        return 0
    fi

    if [ "$DRY_RUN" = true ]; then
        echo -e "${YELLOW}  [DRY RUN] $var_name${NC}"
        if [ "$VERBOSE" = true ]; then
            echo -e "${YELLOW}            = $value${NC}"
            if [ -n "$service_name" ]; then
                echo -e "${YELLOW}            (service: $service_name)${NC}"
            fi
        fi
        ((total_set++))
        return 0
    fi

    # Build service filter for querying existing variables
    local service_filter=""
    if [ -n "$service_name" ]; then
        service_filter="and .serviceName == \"$service_name\""
    else
        service_filter="and .serviceName == \"*\""
    fi

    # Check if variable already exists and get its current value
    local env_json=$(balena env list --$TARGET_TYPE "$TARGET" --json 2>/dev/null || echo "[]")
    local existing_id=$(echo "$env_json" | jq -r ".[] | select(.name == \"$var_name\" $service_filter) | .id" 2>/dev/null || echo "")
    local existing_value=$(echo "$env_json" | jq -r ".[] | select(.name == \"$var_name\" $service_filter) | .value" 2>/dev/null || echo "")

    # Build the balena env set command with optional service flag
    local set_cmd="balena env set \"$var_name\" \"$value\" --$TARGET_TYPE \"$TARGET\""
    if [ -n "$service_name" ]; then
        set_cmd="$set_cmd --service \"$service_name\""
    fi

    if [ -n "$existing_id" ]; then
        # Check if value has changed
        if [ "$existing_value" = "$value" ]; then
            if [ "$VERBOSE" = true ]; then
                echo -e "${YELLOW}  ⏭️  Unchanged: $var_name${NC}"
            fi
            ((total_skip++))
            return 0
        fi

        # Update existing variable (remove and re-add)
        if balena env rm "$existing_id" --yes &>/dev/null && \
           eval "$set_cmd" &>/dev/null; then
            echo -e "${GREEN}  ✅ Updated: $var_name${NC}"
            ((total_set++))
        else
            echo -e "${RED}  ❌ Failed: $var_name${NC}"
            ((total_error++))
        fi
    else
        # Add new variable
        if eval "$set_cmd" &>/dev/null; then
            echo -e "${GREEN}  ✅ Added: $var_name${NC}"
            ((total_set++))
        else
            echo -e "${RED}  ❌ Failed: $var_name${NC}"
            ((total_error++))
        fi
    fi
}

# Function to process YAML file
process_yaml() {
    local config="$1"

    # Process shared secrets
    echo -e "${BLUE}=== Shared Secrets ===${NC}"
    local shared_vars=$(yq eval '.shared // {} | to_entries | .[] | .key + "=" + .value' "$config" 2>/dev/null || echo "")

    if [ -n "$shared_vars" ]; then
        while IFS='=' read -r key value || [ -n "$key" ]; do
            if [ -n "$key" ] && [ -n "$value" ]; then
                set_balena_var "$key" "$value" "shared" || true
            fi
        done <<< "$shared_vars"
    else
        echo -e "${YELLOW}  No shared secrets found${NC}"
    fi
    echo ""

    # Apply environment overrides to shared if specified
    if [ -n "$ENVIRONMENT" ]; then
        local env_shared=$(yq eval ".environments.${ENVIRONMENT}.shared // {} | to_entries | .[] | .key + \"=\" + .value" "$config" 2>/dev/null || echo "")
        if [ -n "$env_shared" ]; then
            echo -e "${BLUE}=== Shared Overrides ($ENVIRONMENT) ===${NC}"
            while IFS='=' read -r key value || [ -n "$key" ]; do
                if [ -n "$key" ] && [ -n "$value" ]; then
                    set_balena_var "$key" "$value" "shared-override" || true
                fi
            done <<< "$env_shared"
            echo ""
        fi
    fi

    # Process service-specific secrets
    echo -e "${BLUE}=== Service-Specific Secrets ===${NC}"
    local services=$(yq eval '.services // {} | keys | .[]' "$config" 2>/dev/null || echo "")

    if [ -z "$services" ]; then
        echo -e "${YELLOW}  No services found${NC}"
        return
    fi

    while IFS= read -r service; do
        # Skip if service filter is set and doesn't match
        if [ -n "$SERVICE_FILTER" ] && [ "$SERVICE_FILTER" != "$service" ]; then
            continue
        fi

        echo -e "${GREEN}[$service]${NC}"

        # Get service variables
        local service_vars=$(yq eval ".services.${service} // {} | to_entries | .[] | .key + \"=\" + .value" "$config" 2>/dev/null || echo "")

        if [ -n "$service_vars" ]; then
            while IFS='=' read -r key value || [ -n "$key" ]; do
                if [ -n "$key" ] && [ -n "$value" ] && [ "$key" != "pass" ]; then
                    # Set service-specific variable (using --service flag, not prefix)
                    set_balena_var "$key" "$value" "$service" "$service" || true
                fi
            done <<< "$service_vars"
        fi

        # Apply environment overrides if specified
        if [ -n "$ENVIRONMENT" ]; then
            local env_service_vars=$(yq eval ".environments.${ENVIRONMENT}.services.${service} // {} | to_entries | .[] | .key + \"=\" + .value" "$config" 2>/dev/null || echo "")
            if [ -n "$env_service_vars" ]; then
                while IFS='=' read -r key value || [ -n "$key" ]; do
                    if [ -n "$key" ] && [ -n "$value" ]; then
                        set_balena_var "$key" "$value" "$service-override" "$service" || true
                    fi
                done <<< "$env_service_vars"
            fi
        fi

        echo ""
    done <<< "$services"
}

# Function to process ENV file (legacy support)
process_env() {
    local env_file="$1"

    echo -e "${BLUE}=== Processing .env file ===${NC}"

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

            set_balena_var "$key" "$value" "env"
        fi
    done < "$env_file"
    echo ""
}

# Process the config file based on type
if [ "$FILE_TYPE" == "yaml" ]; then
    process_yaml "$CONFIG_FILE"
elif [ "$FILE_TYPE" == "env" ]; then
    process_env "$CONFIG_FILE"
else
    echo -e "${RED}Error: File type $FILE_TYPE not yet implemented${NC}"
    exit 1
fi

# Summary
echo -e "${BLUE}===================================${NC}"
echo -e "${GREEN}✅ Set/Updated: $total_set${NC}"
echo -e "${YELLOW}⏭️  Skipped: $total_skip${NC}"
if [ $total_error -gt 0 ]; then
    echo -e "${RED}❌ Errors: $total_error${NC}"
fi
echo ""

if [ "$DRY_RUN" = true ]; then
    echo -e "${YELLOW}Note: This was a DRY RUN. Run without --dry-run to apply changes.${NC}"
fi

if [[ "$TARGET_TYPE" == "fleet" ]]; then
    echo -e "${YELLOW}Note: Fleet variables apply to all devices.${NC}"
    echo -e "${YELLOW}      Devices may need restart to pick up new values.${NC}"
fi

echo ""
echo "To view all variables:"
echo "  balena env list --$TARGET_TYPE $TARGET"
echo ""
echo "To restart services:"
echo "  balena device reboot <device-uuid>"

if [ $total_error -gt 0 ]; then
    exit 1
fi

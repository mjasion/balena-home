# Grafana Cloud Setup Guide

This guide explains how to set up Grafana Cloud access for the pstryk_metric service to push energy meter metrics.

## Step 1: Create Grafana Cloud Account

1. Go to https://grafana.com/auth/sign-up/create-user
2. Sign up for a free account
3. Verify your email and complete the setup

## Step 2: Find Your Prometheus Instance Details

1. **Log in to Grafana Cloud**
   - Go to https://grafana.com/ and sign in
   - You'll be redirected to your organization's portal

2. **Navigate to Prometheus**
   - In the left sidebar, click on "Connections" → "Data sources"
   - Or go directly to "My Account" → "Stacks" and click on your stack

3. **Get Your Prometheus Endpoint URL**
   - Click on "Prometheus" under "Metrics"
   - Copy the **Remote Write Endpoint URL**
   - It looks like: `https://prometheus-prod-XX-YY-ZZ.grafana.net/api/prom/push`
   - Example: `https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push`

4. **Note Your Instance ID (Username)**
   - On the same page, you'll see **"User"** or **"Instance ID"**
   - This is typically a numeric ID like `123456` or `654321`
   - This will be your `prometheusUsername`

## Step 3: Create an Access Policy (API Token)

### Option A: Using Cloud Access Policies (Recommended)

1. **Navigate to Access Policies**
   - Go to your Grafana Cloud portal
   - Click on "Security" → "Access Policies"
   - Or go to: https://grafana.com/orgs/YOUR_ORG/access-policies

2. **Create New Access Policy**
   - Click **"Create access policy"**
   - Give it a name: `pstryk-metric-pusher`
   - Add a description: `Allows pstryk_metric service to push energy meter metrics`

3. **Configure Scopes**
   - Under "Scopes", add:
     - **metrics:write** ✓ (Required for pushing metrics)
   - Click **"Create"**

4. **Create Access Policy Token**
   - After creating the policy, click **"Add token"**
   - Name: `pstryk-metric-token`
   - Expiration: Choose your preference (e.g., "1 year" or "No expiration")
   - Click **"Create"**

5. **Copy the Token**
   - **IMPORTANT**: Copy the generated token immediately
   - It looks like: `glc_eyJrIjoixxxxxxxxxxxxxx...`
   - You won't be able to see it again!
   - This is your `prometheusPassword`

### Option B: Using API Keys (Legacy Method)

1. **Navigate to API Keys**
   - Go to your Grafana Cloud portal
   - Click on your profile → "Security" → "API Keys"
   - Or directly: https://grafana.com/orgs/YOUR_ORG/api-keys

2. **Create New API Key**
   - Click **"Add API key"**
   - Key name: `pstryk-metric-pusher`
   - Role: **MetricsPublisher** or **Editor**
   - Time to live: Choose expiration (e.g., "1y" or leave blank for no expiration)

3. **Copy the Key**
   - Copy the generated key immediately
   - This is your `prometheusPassword`

## Step 4: Configure pstryk_metric Service

### Method 1: Using Environment Variables (Recommended for Production)

Create a `.env` file or export variables:

```bash
export SCRAPE_URL="http://192.168.1.100/api/sensor"
export PROMETHEUS_URL="https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
export PROMETHEUS_USERNAME="123456"
export PROMETHEUS_PASSWORD="glc_eyJrIjoixxxxxxxxxxxxxx..."
export LOG_FORMAT="json"
export LOG_LEVEL="info"
```

Run the service:
```bash
./pstryk_metric -c config.yaml
```

### Method 2: Using config.yaml

**⚠️ Warning**: Only use this for testing. Don't commit passwords to git!

```yaml
# Energy Meter Scraper Configuration
scrapeUrl: "http://192.168.1.100/api/sensor"
scrapeIntervalSeconds: 2
scrapeTimeoutSeconds: 1.5
pushIntervalSeconds: 15

# Prometheus / Grafana Cloud settings
prometheusUrl: "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
prometheusUsername: "123456"
prometheusPassword: "glc_eyJrIjoixxxxxxxxxxxxxx..."  # USE ENV VAR IN PRODUCTION!

# Metric configuration
metricName: "active_power_watts"
startAtEvenSecond: true
healthCheckPort: 8080

# Logging
logFormat: "console"
logLevel: "info"
```

### Method 3: Docker with Environment Variables

```bash
docker run -d \
  -e SCRAPE_URL="http://192.168.1.100/api/sensor" \
  -e PROMETHEUS_URL="https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push" \
  -e PROMETHEUS_USERNAME="123456" \
  -e PROMETHEUS_PASSWORD="glc_eyJrIjoixxxxxxxxxxxxxx..." \
  -e LOG_FORMAT="json" \
  -e LOG_LEVEL="info" \
  -p 8080:8080 \
  --name pstryk_metric \
  pstryk_metric
```

Or use docker-compose.yml:

```yaml
version: '3.8'
services:
  pstryk_metric:
    image: pstryk_metric
    container_name: pstryk_metric
    environment:
      - SCRAPE_URL=http://192.168.1.100/api/sensor
      - PROMETHEUS_URL=https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push
      - PROMETHEUS_USERNAME=123456
      - PROMETHEUS_PASSWORD=${GRAFANA_API_KEY}  # Set in .env file
      - LOG_FORMAT=json
      - LOG_LEVEL=info
    ports:
      - "8080:8080"
    restart: unless-stopped
```

Create `.env` file:
```bash
GRAFANA_API_KEY=glc_eyJrIjoixxxxxxxxxxxxxx...
```

## Step 5: Test the Connection

### 1. Start the Service

```bash
./pstryk_metric -c config.yaml
```

### 2. Check Logs

You should see:
```
{"level":"info","ts":1729841234.123,"msg":"Configuration loaded successfully",...}
{"level":"info","ts":1729841234.456,"msg":"Starting health check server","addr":":8080"}
{"level":"info","ts":1729841234.789,"msg":"Service started","scrapeIntervalSeconds":2,"pushIntervalSeconds":15}
{"level":"debug","ts":1729841236.123,"msg":"Scrape successful","duration":"123ms","readingCount":4}
{"level":"info","ts":1729841251.456,"msg":"Push successful","duration":"234ms"}
```

### 3. Check Health Endpoint

```bash
curl http://localhost:8080/health
```

Expected response:
```json
{
  "status": "healthy",
  "lastScrapeTime": "2025-10-25T06:30:00Z",
  "lastPushTime": "2025-10-25T06:30:15Z",
  "bufferedSamples": 0
}
```

### 4. Verify Metrics in Grafana Cloud

1. **Open Grafana Cloud Explore**
   - Go to your Grafana Cloud instance
   - Click "Explore" in the left sidebar

2. **Query Your Metrics**
   - Select your Prometheus data source
   - In the query builder or PromQL editor, type:
     ```
     active_power_watts
     ```
   - Click "Run Query"

3. **You Should See:**
   - Metrics with labels: `sensor_id="0"`, `sensor_id="1"`, `sensor_id="2"`, `sensor_id="3"`
   - Values showing current power consumption
   - Timestamps from the last push

## Step 6: Create a Dashboard

### Quick Dashboard

1. **Create New Dashboard**
   - In Grafana Cloud, click "Dashboards" → "New Dashboard"
   - Click "Add visualization"

2. **Configure Panel**
   - Data source: Select your Prometheus instance
   - Query: `active_power_watts`
   - Legend: `{{sensor_id}}`
   - Panel title: "Active Power by Sensor"

3. **Add More Panels**
   - Total power: `sum(active_power_watts)`
   - Power by sensor (table):
     ```
     active_power_watts{sensor_id=~".*"}
     ```

4. **Save Dashboard**
   - Click "Save dashboard"
   - Name: "Energy Meter Monitoring"

## Troubleshooting

### Authentication Error (401)

**Symptom:**
```
{"level":"error","msg":"Push failed","error":"push failed with status 401"}
```

**Solution:**
- Verify your `prometheusUsername` (Instance ID) is correct
- Regenerate your API token/key
- Ensure the token has `metrics:write` scope
- Check token hasn't expired

### Connection Error

**Symptom:**
```
{"level":"error","msg":"Push failed","error":"HTTP request failed: connection refused"}
```

**Solution:**
- Verify `prometheusUrl` is correct
- Check internet connectivity
- Ensure firewall allows outbound HTTPS (port 443)

### No Data in Grafana

**Symptom:** Service runs but no metrics visible in Grafana

**Solution:**
1. Check service logs for successful pushes:
   ```
   {"level":"info","msg":"Push successful"}
   ```

2. Verify scraping is working:
   ```
   curl http://localhost:8080/health
   ```

3. In Grafana Explore, check time range includes recent data

4. Verify query syntax:
   ```
   active_power_watts{sensor_id="0"}
   ```

### Invalid URL Error

**Symptom:**
```
{"level":"error","msg":"Failed to load configuration","error":"invalid prometheusUrl"}
```

**Solution:**
- Ensure URL includes `/api/prom/push` at the end
- Use HTTPS (not HTTP)
- Don't include username/password in URL
- Correct format: `https://prometheus-prod-XX-YY-ZZ.grafana.net/api/prom/push`

## Security Best Practices

1. **Never commit API tokens to git**
   - Add `.env` to `.gitignore`
   - Use environment variables in production

2. **Use Access Policies over API Keys**
   - More granular permissions
   - Can be scoped to specific actions
   - Easier to rotate

3. **Set Token Expiration**
   - Use 1 year expiration
   - Set calendar reminder to rotate

4. **Limit Token Scope**
   - Only grant `metrics:write` permission
   - Don't use `Admin` or `Editor` roles unless needed

5. **Rotate Tokens Regularly**
   - Create new token
   - Update service configuration
   - Delete old token
   - Verify service still works

## Additional Resources

- [Grafana Cloud Docs](https://grafana.com/docs/grafana-cloud/)
- [Prometheus Remote Write](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write)
- [PromQL Query Language](https://prometheus.io/docs/prometheus/latest/querying/basics/)
- [Grafana Dashboards](https://grafana.com/docs/grafana/latest/dashboards/)

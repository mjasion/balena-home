FROM gcr.io/datadoghq/agent:7

# Copy custom Datadog configuration
COPY conf.d /etc/datadog-agent/conf.d

# Labels for Balena
LABEL io.balena.features.balena-socket="1"

FROM public.ecr.aws/datadog/agent:7.72.4

# Copy custom Datadog configuration
COPY conf.d /etc/datadog-agent/conf.d
COPY datadog.yaml /etc/datadog-agent/datadog.yaml

# Labels for Balena
LABEL io.balena.features.balena-socket="1"

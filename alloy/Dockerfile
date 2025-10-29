FROM grafana/alloy:v1.11.3

# Copy Alloy configuration into the image
COPY config.alloy /etc/alloy/config.alloy

# Set the default command
CMD ["run", "/etc/alloy/config.alloy", "--server.http.listen-addr=0.0.0.0:12345", "--storage.path=/var/lib/alloy/data"]

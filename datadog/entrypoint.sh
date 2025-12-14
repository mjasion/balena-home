#!/bin/bash
set -e

echo "Setup: Creating Balena compatibility symlinks..."

# 1. Fix Host Filesystem (Procfs/Sysfs)
mkdir -p /host
ln -sfn /proc /host/proc
ln -sfn /sys /host/sys

echo "Setup complete. Starting Agent..."
exec /init

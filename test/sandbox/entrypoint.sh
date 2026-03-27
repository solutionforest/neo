#!/bin/bash
set -e

# Start Docker daemon in background (DinD mode — requires --privileged)
dockerd --host=unix:///var/run/docker.sock --storage-driver=overlay2 &

# Wait for Docker to be ready (max 30s)
echo "Waiting for Docker daemon..."
for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then
        echo "Docker daemon ready."
        break
    fi
    sleep 1
done

if ! docker info >/dev/null 2>&1; then
    echo "WARNING: Docker daemon failed to start. DinD requires --privileged."
fi

# Start sshd in foreground
exec /usr/sbin/sshd -D -e

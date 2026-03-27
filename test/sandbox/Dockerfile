# Neo Docker Sandbox — simulates a fresh Ubuntu 24.04 VPS for integration testing.
# Runs sshd + supports Docker-in-Docker (requires --privileged).
#
# Usage:
#   docker compose up -d --build
#   ./run-tests.sh
#   docker compose down -v

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Core packages that a real VPS would have
RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-server \
    curl \
    sudo \
    iproute2 \
    procps \
    ca-certificates \
    gnupg \
    lsb-release \
    iptables \
    kmod \
    && rm -rf /var/lib/apt/lists/*

# SSH configuration — allow root login with key auth
RUN mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh
RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config && \
    echo "root:neotest" | chpasswd

# Ensure /etc/os-release looks like a real Ubuntu 24.04 (it already does from base image)
# Verify:
RUN grep 'Ubuntu 24.04' /etc/os-release

# Pre-install Docker so `neo init` detects it and skips the slow `get.docker.com` install.
# This saves ~60s per test run. Neo init will still create the network and start Caddy.
RUN curl -fsSL https://get.docker.com | sh && \
    rm -rf /var/lib/apt/lists/*

# Entrypoint: start dockerd in background, then sshd in foreground
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 22

ENTRYPOINT ["/entrypoint.sh"]

# Neo Docker Sandbox — simulates a fresh Linux VPS for integration testing.
# Supports multiple distros via BASE_IMAGE build arg.
# Runs sshd + supports Docker-in-Docker (requires --privileged).
#
# Usage:
#   docker build --build-arg BASE_IMAGE=ubuntu:24.04 -t neo-sandbox:ubuntu-24.04 .
#   docker build --build-arg BASE_IMAGE=debian:12 -t neo-sandbox:debian-12 .

ARG BASE_IMAGE=ubuntu:24.04
FROM ${BASE_IMAGE}

ENV DEBIAN_FRONTEND=noninteractive

# Core packages that a real VPS would have.
# Works on all Debian/Ubuntu variants.
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
# Handle both old-style and new-style sshd_config
RUN sed -i 's/^#*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && \
    sed -i 's/^#*PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config && \
    echo "root:neotest" | chpasswd

# Pre-install Docker so neo init skips the slow get.docker.com step.
# This saves ~60s per test run.
RUN curl -fsSL https://get.docker.com | sh && \
    rm -rf /var/lib/apt/lists/*

# Entrypoint: start dockerd in background, then sshd in foreground
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 22

ENTRYPOINT ["/entrypoint.sh"]

#!/bin/bash
# Neo Docker Sandbox — Integration Test Runner
#
# Spins up a Docker container that simulates a fresh Ubuntu 24.04 VPS,
# runs neo integration tests against it, then tears everything down.
#
# Usage:
#   ./test/sandbox/run-tests.sh          # full cycle: build neo, start sandbox, test, destroy
#   ./test/sandbox/run-tests.sh --keep   # keep sandbox running after tests
#   ./test/sandbox/run-tests.sh --down   # just tear down an existing sandbox

set -euo pipefail
cd "$(dirname "$0")/../.."  # cd to neo/ root

KEEP=false
DOWN_ONLY=false

for arg in "$@"; do
    case "$arg" in
        --keep) KEEP=true ;;
        --down) DOWN_ONLY=true ;;
    esac
done

# ── Tear down ──
if [ "$DOWN_ONLY" = true ]; then
    echo "  Tearing down sandbox..."
    docker compose -f test/sandbox/docker-compose.yml down -v 2>/dev/null || true
    echo "  Done."
    exit 0
fi

echo
echo "  Neo Docker Sandbox Tests"
echo "  ────────────────────────"
echo

# ── Step 1: Build neo binary ──
echo "  [1/5] Building neo binary..."
make build 2>&1 | tail -1
if [ ! -f bin/neo ]; then
    echo "  ERROR: bin/neo not found after build"
    exit 1
fi
echo "  Built bin/neo"
echo

# ── Step 2: Build + start sandbox container ──
echo "  [2/5] Starting sandbox container..."
docker compose -f test/sandbox/docker-compose.yml up -d --build 2>&1 | tail -3

# Wait for Docker-in-Docker to be ready
echo "  Waiting for Docker-in-Docker..."
for i in $(seq 1 60); do
    if docker exec neo-sandbox docker info >/dev/null 2>&1; then
        echo "  Docker-in-Docker ready (${i}s)"
        break
    fi
    if [ "$i" -eq 60 ]; then
        echo "  ERROR: Docker-in-Docker not ready after 60s"
        echo "  Make sure you're running with --privileged"
        docker logs neo-sandbox 2>&1 | tail -10
        exit 1
    fi
    sleep 1
done
echo

# ── Step 3: Generate + inject SSH key ──
echo "  [3/5] Setting up SSH key..."
KEY_DIR=$(mktemp -d)
ssh-keygen -t ed25519 -f "$KEY_DIR/id_ed25519" -N "" -q
docker exec neo-sandbox bash -c "mkdir -p /root/.ssh && chmod 700 /root/.ssh"
docker cp "$KEY_DIR/id_ed25519.pub" neo-sandbox:/root/.ssh/authorized_keys
docker exec neo-sandbox chmod 600 /root/.ssh/authorized_keys

# Verify SSH connectivity
for i in $(seq 1 15); do
    if ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
           -i "$KEY_DIR/id_ed25519" -p 2222 root@localhost "echo ok" 2>/dev/null | grep -q ok; then
        echo "  SSH ready"
        break
    fi
    if [ "$i" -eq 15 ]; then
        echo "  ERROR: SSH not reachable after 15s"
        exit 1
    fi
    sleep 1
done
echo

# ── Step 4: Run programmatic tests ──
echo "  [4/5] Running sandbox tests..."
echo

# Build the sandbox test binary
make build-sandbox-test 2>&1 | tail -1

# Run it
bin/neo-sandbox-test \
    --host "root@localhost" \
    --port 2222 \
    --key "$KEY_DIR/id_ed25519"
TEST_EXIT=$?

echo

# ── Step 5: Cleanup ──
if [ "$KEEP" = true ]; then
    echo "  [5/5] Keeping sandbox alive."
    echo
    echo "  ┌──────────────────────────────────────────────────────┐"
    echo "  │  SSH:   ssh -i $KEY_DIR/id_ed25519 -p 2222 root@localhost"
    echo "  │  Down:  ./test/sandbox/run-tests.sh --down           │"
    echo "  └──────────────────────────────────────────────────────┘"
    echo
else
    echo "  [5/5] Tearing down sandbox..."
    docker compose -f test/sandbox/docker-compose.yml down -v 2>/dev/null || true
    rm -rf "$KEY_DIR"
    echo "  Sandbox destroyed."
fi

exit $TEST_EXIT

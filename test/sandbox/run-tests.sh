#!/bin/bash
# Neo Docker Sandbox — Multi-Distro Integration Test Runner
#
# Spins up Docker containers simulating different Linux VPS distros,
# runs neo integration tests against each, then tears everything down.
#
# Usage:
#   ./test/sandbox/run-tests.sh                         # test all distros
#   ./test/sandbox/run-tests.sh --distro ubuntu-24.04   # test one distro
#   ./test/sandbox/run-tests.sh --supported             # test only supported distros
#   ./test/sandbox/run-tests.sh --unsupported           # test only unsupported distros
#   ./test/sandbox/run-tests.sh --keep                  # keep containers after tests
#   ./test/sandbox/run-tests.sh --down                  # tear down everything
#   ./test/sandbox/run-tests.sh --list                  # list all distros

set -euo pipefail
cd "$(dirname "$0")/../.."  # cd to neo/ root

COMPOSE="docker compose -f test/sandbox/docker-compose.yml"
KEEP=false
DOWN_ONLY=false
LIST_ONLY=false
DISTRO=""
FILTER=""  # "supported" or "unsupported"

for arg in "$@"; do
    case "$arg" in
        --keep)          KEEP=true ;;
        --down)          DOWN_ONLY=true ;;
        --list)          LIST_ONLY=true ;;
        --supported)     FILTER="supported" ;;
        --unsupported)   FILTER="unsupported" ;;
        --distro=*)      DISTRO="${arg#--distro=}" ;;
        --distro)        ;; # next arg is the distro name
        *)
            if [ "${PREV_ARG:-}" = "--distro" ]; then
                DISTRO="$arg"
            fi
            ;;
    esac
    PREV_ARG="$arg"
done

# ── List distros ──
if [ "$LIST_ONLY" = true ]; then
    make -s build-sandbox-test 2>/dev/null
    bin/neo-sandbox-test --list
    exit 0
fi

# ── Tear down ──
if [ "$DOWN_ONLY" = true ]; then
    echo "  Tearing down all sandbox containers..."
    $COMPOSE down -v 2>/dev/null || true
    echo "  Done."
    exit 0
fi

echo
echo "  Neo Docker Sandbox Tests"
echo "  ────────────────────────"
echo

# ── Determine which services to start ──
SERVICES=""
if [ -n "$DISTRO" ]; then
    SERVICES="$DISTRO"
elif [ "$FILTER" = "supported" ]; then
    SERVICES="ubuntu-24.04 ubuntu-24.10 debian-12 debian-11"
elif [ "$FILTER" = "unsupported" ]; then
    SERVICES="ubuntu-22.04 ubuntu-20.04"
else
    # All services
    SERVICES=""
fi

# ── Step 1: Build neo binaries ──
echo "  [1/5] Building neo + sandbox test binary..."
make build build-sandbox-test 2>&1 | tail -1
if [ ! -f bin/neo ] || [ ! -f bin/neo-sandbox-test ]; then
    echo "  ERROR: build failed"
    exit 1
fi
echo "  Built bin/neo + bin/neo-sandbox-test"
echo

# ── Step 2: Build + start sandbox containers ──
echo "  [2/5] Starting sandbox containers..."
if [ -n "$SERVICES" ]; then
    $COMPOSE up -d --build $SERVICES 2>&1 | tail -5
else
    $COMPOSE up -d --build 2>&1 | tail -5
fi

# Wait for Docker-in-Docker to be ready in all containers
echo "  Waiting for Docker-in-Docker in all containers..."
CONTAINERS=$($COMPOSE ps --format '{{.Name}}' 2>/dev/null)
ALL_READY=true
for container in $CONTAINERS; do
    ready=false
    for i in $(seq 1 60); do
        if docker exec "$container" docker info >/dev/null 2>&1; then
            ready=true
            break
        fi
        sleep 1
    done
    if [ "$ready" = true ]; then
        echo "  $container: Docker ready"
    else
        echo "  $container: Docker FAILED"
        ALL_READY=false
    fi
done
if [ "$ALL_READY" = false ]; then
    echo "  ERROR: Some containers failed to start Docker."
    echo "  Ensure you're running with --privileged."
    exit 1
fi
echo

# ── Step 3: Generate + inject SSH key into all containers ──
echo "  [3/5] Setting up SSH keys..."
KEY_DIR=$(mktemp -d)
ssh-keygen -t ed25519 -f "$KEY_DIR/id_ed25519" -N "" -q

for container in $CONTAINERS; do
    docker exec "$container" bash -c "mkdir -p /root/.ssh && chmod 700 /root/.ssh"
    docker cp "$KEY_DIR/id_ed25519.pub" "$container:/root/.ssh/authorized_keys"
    docker exec "$container" chmod 600 /root/.ssh/authorized_keys
done

# Verify SSH connectivity on each container
for container in $CONTAINERS; do
    port=$(docker port "$container" 22/tcp 2>/dev/null | head -1 | cut -d: -f2)
    for i in $(seq 1 15); do
        if ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
               -i "$KEY_DIR/id_ed25519" -p "$port" root@localhost "echo ok" 2>/dev/null | grep -q ok; then
            echo "  $container (port $port): SSH ready"
            break
        fi
        if [ "$i" -eq 15 ]; then
            echo "  $container (port $port): SSH FAILED"
        fi
        sleep 1
    done
done
echo

# ── Step 4: Run tests ──
echo "  [4/5] Running sandbox tests..."
echo

TEST_ARGS="--host root@localhost --key $KEY_DIR/id_ed25519"
if [ -n "$DISTRO" ]; then
    TEST_ARGS="$TEST_ARGS --distro $DISTRO"
fi

bin/neo-sandbox-test $TEST_ARGS
TEST_EXIT=$?

echo

# ── Step 5: Cleanup ──
if [ "$KEEP" = true ]; then
    echo "  [5/5] Keeping sandbox containers alive."
    echo
    echo "  ┌─────────────────────────────────────────────────────────────┐"
    echo "  │  Key:   $KEY_DIR/id_ed25519"
    for container in $CONTAINERS; do
        port=$(docker port "$container" 22/tcp 2>/dev/null | head -1 | cut -d: -f2)
        printf "  │  %-5s  ssh -i %s -p %s root@localhost\n" "" "$KEY_DIR/id_ed25519" "$port"
    done
    echo "  │  Down:  ./test/sandbox/run-tests.sh --down                  │"
    echo "  └─────────────────────────────────────────────────────────────┘"
    echo
else
    echo "  [5/5] Tearing down sandbox..."
    $COMPOSE down -v 2>/dev/null || true
    rm -rf "$KEY_DIR"
    echo "  Sandbox destroyed."
fi

exit $TEST_EXIT

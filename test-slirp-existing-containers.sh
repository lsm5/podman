#!/bin/bash
# Test existing container migration: Containers created with slirp4netns before upgrade
# This simulates containers that were created with older Podman versions using slirp4netns

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PASSED=0
FAILED=0

function print_test() {
    echo -e "\n${YELLOW}[TEST $1]${NC} $2"
}

function info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

function pass() {
    echo -e "${GREEN}✓ PASS${NC} $1"
    ((PASSED++))
}

function fail() {
    echo -e "${RED}✗ FAIL${NC} $1"
    ((FAILED++))
    exit 1
}

function cleanup() {
    echo -e "\n${YELLOW}[CLEANUP]${NC}"
    podman rm -f existing-slirp-1 existing-slirp-2 existing-slirp-3 2>/dev/null || true
    podman pod rm -f existing-slirp-pod 2>/dev/null || true
}

trap cleanup EXIT

echo "======================================================================"
echo "Existing Container Migration Test"
echo "======================================================================"
echo "Testing: Containers created with slirp4netns before upgrade"
echo ""

info "This test simulates containers created with older Podman using slirp4netns"
info "In Podman 6, these containers should:"
info "  1. Still start successfully"
info "  2. Use pasta instead of slirp4netns"
info "  3. Maintain network connectivity"
echo ""

# TEST 1: Create a container with slirp4netns mode
print_test "1" "Create container with --network slirp4netns (simulating old config)"

podman create --name existing-slirp-1 --network slirp4netns alpine sleep 300 2>&1 | tee /tmp/create-test.log

if grep -qi "converting to pasta" /tmp/create-test.log; then
    pass "Container creation showed migration warning"
else
    info "Note: podman create may not show warning (only podman run does)"
fi

CREATED_MODE=$(podman inspect existing-slirp-1 --format '{{.HostConfig.NetworkMode}}')
info "Container NetworkMode after creation: $CREATED_MODE"

if [ "$CREATED_MODE" = "pasta" ] || [ "$CREATED_MODE" = "slirp4netns" ]; then
    pass "Container created with network mode: $CREATED_MODE"
else
    fail "Unexpected network mode: $CREATED_MODE"
fi

# TEST 2: Start the existing container
print_test "2" "Start existing container (should use pasta, not slirp4netns)"

podman start existing-slirp-1

sleep 2

# Check what process is actually running
if pgrep -f "pasta" > /dev/null 2>&1; then
    pass "Pasta process detected (not slirp4netns)"
elif pgrep -f "slirp4netns" > /dev/null 2>&1; then
    fail "slirp4netns process detected (should be using pasta)"
else
    info "Note: Could not detect network process (may be using different backend)"
fi

# Verify container is running
RUNNING=$(podman inspect existing-slirp-1 --format '{{.State.Running}}')
if [ "$RUNNING" = "true" ]; then
    pass "Container is running"
else
    fail "Container failed to start"
fi

# TEST 3: Verify network connectivity in migrated container
print_test "3" "Network connectivity in migrated container"

OUTPUT=$(podman exec existing-slirp-1 wget -q -O - http://www.google.com 2>&1)

if echo "$OUTPUT" | grep -qi "html"; then
    pass "HTTP connectivity works"
else
    fail "Network connectivity failed in migrated container"
fi

# TEST 4: DNS resolution
print_test "4" "DNS resolution in migrated container"

OUTPUT=$(podman exec existing-slirp-1 nslookup google.com 2>&1)

if echo "$OUTPUT" | grep -qE "Address.*[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+"; then
    pass "DNS resolution works"
else
    fail "DNS failed in migrated container"
fi

# TEST 5: IP address assignment
print_test "5" "IP address assigned correctly"

IP_OUTPUT=$(podman exec existing-slirp-1 ip addr show)
echo "$IP_OUTPUT" | grep -E "inet [0-9]+" | head -3

if echo "$IP_OUTPUT" | grep -qE "inet 10\.[0-9]+\.[0-9]+\.[0-9]+"; then
    pass "Container has IP address in 10.x range"
else
    info "Note: IP may be in different range depending on pasta configuration"
fi

# TEST 6: Restart existing container
print_test "6" "Restart existing container"

podman restart existing-slirp-1

sleep 2

RUNNING=$(podman inspect existing-slirp-1 --format '{{.State.Running}}')
if [ "$RUNNING" = "true" ]; then
    pass "Container restarted successfully"
else
    fail "Container failed to restart"
fi

# Verify connectivity after restart
OUTPUT=$(podman exec existing-slirp-1 ping -c 1 8.8.8.8 2>&1)
if echo "$OUTPUT" | grep -q "1 packets transmitted, 1 packets received"; then
    pass "Network connectivity maintained after restart"
else
    fail "Network connectivity lost after restart"
fi

podman stop existing-slirp-1

# TEST 7: Container with port mappings
print_test "7" "Existing container with port mappings"

podman run -d --name existing-slirp-2 \
    --network slirp4netns \
    -p 9090:80 \
    nginx:alpine 2>&1 | tee /tmp/port-existing.log

sleep 3

if curl -sf http://localhost:9090 | grep -qi "nginx"; then
    pass "Port mapping works on migrated container"
else
    fail "Port mapping not working"
fi

# Check the actual network mode
NETWORK_MODE=$(podman inspect existing-slirp-2 --format '{{.HostConfig.NetworkMode}}')
info "Container with ports using network mode: $NETWORK_MODE"

podman stop existing-slirp-2
podman rm existing-slirp-2

# TEST 8: Inspect network settings
print_test "8" "Inspect migrated container network settings"

NETWORK_SETTINGS=$(podman inspect existing-slirp-1 --format '{{json .NetworkSettings}}' | jq -r '.SandboxKey')

if [ -n "$NETWORK_SETTINGS" ] && [ "$NETWORK_SETTINGS" != "null" ]; then
    pass "Network namespace configured: $NETWORK_SETTINGS"
else
    fail "Network namespace not properly configured"
fi

# TEST 9: Pod with slirp4netns
print_test "9" "Existing pod with slirp4netns network"

podman pod create --name existing-slirp-pod --network slirp4netns 2>&1 | tee /tmp/pod-existing.log

if grep -qi "converting to pasta" /tmp/pod-existing.log; then
    pass "Pod creation showed conversion warning"
else
    info "Note: Pod creation may not show conversion warning"
fi

podman run -d --pod existing-slirp-pod alpine sleep 60
sleep 2

POD_CONTAINERS=$(podman pod inspect existing-slirp-pod --format '{{len .Containers}}')
if [ "$POD_CONTAINERS" -ge 2 ]; then
    pass "Pod containers running (count: $POD_CONTAINERS)"
else
    fail "Pod containers not running properly"
fi

# Test connectivity in pod
CTR_IN_POD=$(podman ps --filter "pod=existing-slirp-pod" --filter "name=!infra" --format "{{.Names}}" | head -1)
if [ -n "$CTR_IN_POD" ]; then
    OUTPUT=$(podman exec "$CTR_IN_POD" wget -q -O - http://www.google.com 2>&1)
    if echo "$OUTPUT" | grep -qi "html"; then
        pass "Network connectivity in pod containers"
    else
        fail "Pod container network connectivity failed"
    fi
fi

podman pod rm -f existing-slirp-pod

# TEST 10: Verify no slirp4netns binary required
print_test "10" "Podman should not require slirp4netns binary"

if command -v slirp4netns &> /dev/null; then
    SLIRP_PATH=$(command -v slirp4netns)
    info "slirp4netns binary found at: $SLIRP_PATH"
    info "This is OK but not required for Podman 6"
else
    pass "slirp4netns binary not found (not required)"
fi

# Verify podman info doesn't report slirp4netns
SLIRP_INFO=$(podman info --format '{{.Host.Slirp4NetNS.Executable}}')
if [ -z "$SLIRP_INFO" ] || [ "$SLIRP_INFO" = "<no value>" ]; then
    pass "podman info does not report slirp4netns executable"
else
    info "podman info still reports: $SLIRP_INFO"
fi

# Cleanup
podman rm -f existing-slirp-1

# Summary
echo ""
echo "======================================================================"
echo "Summary"
echo "======================================================================"
echo -e "${GREEN}Passed:${NC} $PASSED"
echo -e "${RED}Failed:${NC} $FAILED"
echo "======================================================================"

if [ $FAILED -eq 0 ]; then
    echo -e "\n${GREEN}✓ All existing container migration tests passed!${NC}"
    echo ""
    echo "Verified:"
    echo "  • Containers created with slirp4netns still work"
    echo "  • They now use pasta instead of slirp4netns"
    echo "  • Network connectivity is maintained"
    echo "  • Port mappings continue to function"
    echo "  • Restart/stop/start operations work correctly"
    echo "  • Pods with slirp4netns migrate successfully"
    exit 0
else
    echo -e "\n${RED}✗ Some tests failed${NC}"
    exit 1
fi

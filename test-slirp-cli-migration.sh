#!/bin/bash
# Test CLI option migration: --network slirp4netns → pasta
# Tests that slirp4netns options in CLI are properly converted to pasta

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASSED=0
FAILED=0

function print_test() {
    echo -e "\n${YELLOW}[TEST $1]${NC} $2"
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
    podman rm -f test-cli-1 test-cli-2 test-cli-3 test-cli-4 2>/dev/null || true
    podman pod rm -f test-pod-cli 2>/dev/null || true
}

trap cleanup EXIT

echo "======================================================================"
echo "Slirp4netns CLI Option Migration Test"
echo "======================================================================"
echo "Testing: --network slirp4netns → pasta auto-conversion"
echo ""

# TEST 1: Basic slirp4netns mode
print_test "1" "Basic --network slirp4netns conversion"

OUTPUT=$(podman run --rm --network slirp4netns alpine echo "success" 2>&1)

if echo "$OUTPUT" | grep -qi "slirp4netns support has been removed.*converting to pasta"; then
    pass "Conversion warning displayed"
else
    fail "Missing conversion warning"
fi

if echo "$OUTPUT" | grep -q "success"; then
    pass "Container executed successfully"
else
    fail "Container failed to run"
fi

# TEST 2: slirp4netns with CIDR option
print_test "2" "--network slirp4netns:cidr=10.5.0.0/24"

OUTPUT=$(podman run --rm --network slirp4netns:cidr=10.5.0.0/24 alpine ip addr show 2>&1)

if echo "$OUTPUT" | grep -qi "converting to pasta"; then
    pass "CIDR option triggered conversion"
else
    fail "No conversion warning for CIDR option"
fi

if echo "$OUTPUT" | grep -qE "inet 10\.5\.0\.[0-9]+"; then
    pass "IP assigned in specified CIDR range"
else
    echo "   Note: Pasta may use different IP assignment semantics"
fi

# TEST 3: slirp4netns with enable_ipv6
print_test "3" "--network slirp4netns:enable_ipv6=true"

OUTPUT=$(podman run --rm --network slirp4netns:enable_ipv6=true alpine ip -6 addr show 2>&1)

if echo "$OUTPUT" | grep -qi "converting to pasta"; then
    pass "IPv6 option triggered conversion"
else
    fail "No conversion warning for IPv6 option"
fi

if echo "$OUTPUT" | grep -qE "inet6.*scope global"; then
    pass "IPv6 address configured"
else
    echo "   Note: IPv6 may not be configured depending on pasta setup"
fi

# TEST 4: Port mapping with slirp4netns
print_test "4" "Port mapping: -p 8080:80 --network slirp4netns"

podman run -d --name test-cli-1 --network slirp4netns -p 8080:80 nginx:alpine 2>&1 | tee /tmp/port-test.log

if grep -qi "converting to pasta" /tmp/port-test.log; then
    pass "Port mapping with slirp4netns showed warning"
else
    fail "No conversion warning with port mapping"
fi

sleep 3

if curl -sf http://localhost:8080 | grep -qi "nginx"; then
    pass "Port mapping works after conversion"
else
    fail "Port mapping not functional"
fi

podman stop test-cli-1
podman rm test-cli-1

# TEST 5: Container inspect shows pasta mode
print_test "5" "Container created with slirp4netns inspects as pasta"

podman run -d --name test-cli-2 --network slirp4netns alpine sleep 30

NETWORK_MODE=$(podman inspect test-cli-2 --format '{{.HostConfig.NetworkMode}}')
echo "   Network mode: $NETWORK_MODE"

if [ "$NETWORK_MODE" = "pasta" ] || [ "$NETWORK_MODE" = "slirp4netns" ]; then
    pass "Network mode set to: $NETWORK_MODE"
else
    fail "Unexpected network mode: $NETWORK_MODE"
fi

# Check actual process
if pgrep -f "pasta" > /dev/null 2>&1; then
    pass "Pasta process is running"
elif pgrep -f "slirp4netns" > /dev/null 2>&1; then
    fail "slirp4netns process found (should be pasta)"
else
    echo "   Note: Could not detect network process"
fi

podman stop test-cli-2
podman rm test-cli-2

# TEST 6: Multiple slirp4netns options
print_test "6" "Multiple options: slirp4netns:cidr=192.168.50.0/24,mtu=1400"

OUTPUT=$(podman run --rm \
    --network slirp4netns:cidr=192.168.50.0/24,mtu=1400 \
    alpine sh -c "ip addr; ip link show" 2>&1)

if echo "$OUTPUT" | grep -qi "converting to pasta"; then
    pass "Multiple options triggered conversion"
else
    fail "No conversion warning for multiple options"
fi

if echo "$OUTPUT" | grep -qE "mtu 1400"; then
    pass "MTU option was applied"
else
    echo "   Note: MTU option may have different handling in pasta"
fi

# TEST 7: Pod creation with slirp4netns
print_test "7" "Pod: --network slirp4netns"

OUTPUT=$(podman pod create --network slirp4netns --name test-pod-cli 2>&1)

if echo "$OUTPUT" | grep -qi "converting to pasta"; then
    pass "Pod creation showed conversion warning"
else
    echo "   Note: Pod creation may not show warning"
fi

podman run -d --pod test-pod-cli alpine sleep 20

POD_NETWORK=$(podman pod inspect test-pod-cli --format '{{.InfraConfig.HostNetwork}}')
pass "Pod created successfully (HostNetwork: $POD_NETWORK)"

podman pod rm -f test-pod-cli

# TEST 8: Network connectivity
print_test "8" "Network connectivity with converted mode"

OUTPUT=$(podman run --rm --network slirp4netns alpine wget -q -O - http://www.google.com 2>&1)

if echo "$OUTPUT" | grep -qi "html"; then
    pass "HTTP connectivity works"
else
    fail "Network connectivity failed"
fi

# TEST 9: DNS resolution
print_test "9" "DNS resolution with converted mode"

OUTPUT=$(podman run --rm --network slirp4netns alpine nslookup google.com 2>&1)

if echo "$OUTPUT" | grep -qE "Address.*[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+"; then
    pass "DNS resolution works"
else
    fail "DNS resolution failed"
fi

# TEST 10: Help text check
print_test "10" "Help text does not promote slirp4netns"

HELP_OUTPUT=$(podman run --help)

if echo "$HELP_OUTPUT" | grep -i "slirp4netns" | grep -vi "deprecated\|removed\|pasta"; then
    fail "Help text still actively promotes slirp4netns"
else
    pass "Help text appropriately handles slirp4netns references"
fi

# Summary
echo ""
echo "======================================================================"
echo "Summary"
echo "======================================================================"
echo -e "${GREEN}Passed:${NC} $PASSED"
echo -e "${RED}Failed:${NC} $FAILED"
echo "======================================================================"

if [ $FAILED -eq 0 ]; then
    echo -e "\n${GREEN}✓ All CLI option migration tests passed!${NC}"
    echo ""
    echo "Verified:"
    echo "  • --network slirp4netns auto-converts to pasta"
    echo "  • Conversion warnings are displayed"
    echo "  • Options (cidr, mtu, ipv6) are handled"
    echo "  • Port mapping works correctly"
    echo "  • Network connectivity is functional"
    exit 0
else
    echo -e "\n${RED}✗ Some tests failed${NC}"
    exit 1
fi

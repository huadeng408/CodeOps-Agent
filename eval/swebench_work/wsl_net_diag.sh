#!/usr/bin/env bash
# Network diagnostics for WSL - check all possible paths to reach Clash proxy
set -euo pipefail

echo "=== WSL network diagnostics ==="
echo "WSL IP: $(hostname -I)"

# 1. Try to reach Windows host through various methods
GW=$(ip route show default | awk '{print $3}')
echo "Default gateway: $GW"

# 2. Check if we can actually reach gateway at all
echo ""
echo "=== TCP reachability tests ==="
for port in 7890 7891 8080 3128; do
    if timeout 2 bash -c "echo >/dev/tcp/$GW/$port" 2>/dev/null; then
        echo "Gateway $GW:$port -> OPEN"
    else
        echo "Gateway $GW:$port -> CLOSED"
    fi
done

# 3. Test localhost ports (in mirrored mode, localhost == Windows)
for port in 7890 7891; do
    if timeout 2 bash -c "echo >/dev/tcp/127.0.0.1/$port" 2>/dev/null; then
        echo "localhost:$port -> OPEN (WSL might be in mirrored mode!)"
    else
        echo "localhost:$port -> CLOSED"
    fi
done

# 4. Check what modes are available
echo ""
echo "=== WSL config ==="
cat /etc/wsl.conf 2>/dev/null || echo "No wsl.conf"

# 5. DNS resolution test
echo ""
echo "=== DNS ==="
cat /etc/resolv.conf

# 6. Windows hostname resolution
echo ""
echo "=== Host resolution ==="
getent hosts host.docker.internal 2>/dev/null || echo "host.docker.internal not resolvable"
echo "..."
cat /etc/hosts | head -20

# 7. Current proxy env
echo ""
echo "=== Proxy env ==="
echo "http_proxy=$http_proxy"
echo "https_proxy=$https_proxy"
echo "HTTP_PROXY=$HTTP_PROXY"
echo "HTTPS_PROXY=$HTTPS_PROXY"

# 8. Test raw internet access
echo ""
echo "=== Internet tests ==="
curl -s --max-time 5 -o /dev/null -w "baidu.com: %{http_code}\n" https://www.baidu.com || echo "baidu.com: FAIL"
curl -s --max-time 5 -o /dev/null -w "github.com: %{http_code}\n" https://github.com || echo "github.com: FAIL"

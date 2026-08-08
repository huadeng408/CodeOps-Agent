#!/usr/bin/env bash
# Create Docker daemon config with proxy
set -euo pipefail

DOCKER_CONF_DIR="/etc/docker"
sudo mkdir -p "$DOCKER_CONF_DIR"

cat <<'EOF' | sudo tee "$DOCKER_CONF_DIR/daemon.json"
{
  "proxies": {
    "http-proxy": "http://127.0.0.1:7890",
    "https-proxy": "http://127.0.0.1:7890",
    "no-proxy": "localhost,127.0.0.1,*.internal"
  }
}
EOF

echo ""
echo "=== daemon.json contents ==="
cat "$DOCKER_CONF_DIR/daemon.json"
echo ""
echo "=== Restarting Docker ==="
sudo systemctl restart docker 2>&1 || sudo service docker restart 2>&1
echo "Done"

#!/usr/bin/env bash
# qWDTT server installer: binary from latest GitHub release + systemd unit.
# Usage: curl -fsSL https://raw.githubusercontent.com/konchul/proxy-turn-vk-android/master/install.sh | bash
set -euo pipefail
REPO="konchul/proxy-turn-vk-android"
BIN=/usr/local/bin/wdtt-server
CFG=/etc/wdtt
[ "$(id -u)" -eq 0 ] || { echo "run as root"; exit 1; }
echo "⬇ downloading wdtt-server (latest release)..."
curl -fsSL "https://github.com/$REPO/releases/latest/download/wdtt-server-linux-amd64" -o "$BIN"
chmod +x "$BIN"
mkdir -p "$CFG"
[ -f "$CFG/main.password" ] || { openssl rand -hex 12 > "$CFG/main.password"; chmod 600 "$CFG/main.password"; }
[ -f "$CFG/admin.token" ] || { openssl rand -hex 32 > "$CFG/admin.token"; chmod 600 "$CFG/admin.token"; }
[ -f "$CFG/admin.key" ] || {
  openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 3650 \
    -keyout "$CFG/admin.key" -out "$CFG/admin.crt" \
    -subj "/CN=qwdtt-admin" >/dev/null 2>&1
  chmod 600 "$CFG/admin.key" "$CFG/admin.crt"
}
cat > /etc/systemd/system/wdtt.service <<UNIT
[Unit]
Description=qWDTT VPN Server
After=network-online.target
Wants=network-online.target

[Service]
ExecStartPre=-/bin/sh -c "ip link del wdtt0 2>/dev/null; ip link del wdttraw0 2>/dev/null; true"
ExecStart=$BIN -listen 0.0.0.0:56000 -wg-port 56001 -config-dir $CFG -password-file $CFG/main.password -dns 8.8.8.8,1.1.1.1 -admin-listen 0.0.0.0:56002 -admin-token-file $CFG/admin.token -admin-cert $CFG/admin.crt -admin-key $CFG/admin.key -listen-direct 0.0.0.0:56002 -listen-raw 0.0.0.0:56003 -listen-raw-aes 0.0.0.0:46000 -raw-mtu 1350
ExecStartPost=-/bin/sh -c "for rule in 'udp 56000' 'tcp 56002' 'udp 56002' 'udp 56003' 'udp 46000'; do proto=\${rule%% *}; port=\${rule##* }; iptables -C INPUT -p \$proto --dport \$port -j ACCEPT 2>/dev/null || iptables -I INPUT -p \$proto --dport \$port -j ACCEPT; done; exit 0"
Restart=always
RestartSec=5
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now wdtt
echo "✓ qWDTT server installed. Password:"
cat "$CFG/main.password"

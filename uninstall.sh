#!/bin/sh
set -eu

[ "$(id -u)" -eq 0 ] || { echo "Run as root" >&2; exit 1; }

for service in frigotehnica-tunnel-ui cloudflared-frigotehnica; do
	rc-service "$service" stop 2>/dev/null || true
	rc-update del "$service" default 2>/dev/null || true
done

rm -f /etc/init.d/frigotehnica-tunnel-ui
rm -f /etc/init.d/cloudflared-frigotehnica
rm -f /opt/frigotehnica/frigotehnica-tunnel-ui

echo "Services and UI binary removed."
echo "The cloudflared binary, token, authentication file, logs, and backups were retained in /opt/frigotehnica."
echo "Review them manually before removing sensitive configuration."


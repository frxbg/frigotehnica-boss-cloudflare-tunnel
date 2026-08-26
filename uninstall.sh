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

AJENTI_SERVICE=
for candidate in /opt/ajenti/bin/python3 /opt/ajenti/bin/python /usr/bin/python3 /usr/bin/python; do
	[ -x "$candidate" ] || continue
	plugin_dir=$($candidate -c 'import os, ajenti_plugin_core; print(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(ajenti_plugin_core.__file__))), "ajenti_plugin_frigotehnica"))' 2>/dev/null || true)
	case "$plugin_dir" in
		*/site-packages/ajenti_plugin_frigotehnica|*/dist-packages/ajenti_plugin_frigotehnica)
			[ -d "$plugin_dir" ] && rm -rf "$plugin_dir"
			break
			;;
	esac
done
[ -x /etc/init.d/ajenti ] && AJENTI_SERVICE=ajenti
[ -z "$AJENTI_SERVICE" ] && [ -x /etc/init.d/plugins ] && AJENTI_SERVICE=plugins

echo "Services, UI binary, and Ajenti plugin removed."
echo "The cloudflared binary, token, authentication files, logs, and backups were retained in /opt/frigotehnica."
echo "Review them manually before removing sensitive configuration."

if [ -n "$AJENTI_SERVICE" ]; then
	nohup sh -c "sleep 3; rc-service '$AJENTI_SERVICE' restart" >/dev/null 2>&1 &
fi


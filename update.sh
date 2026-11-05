#!/bin/sh
set -eu

UI_BINARY=/opt/frigotehnica/frigotehnica-tunnel-ui
UPDATE_CONFIG=/opt/frigotehnica/config/updates.json
UPDATE_SERVICE=frigotehnica-cloudflared-update
operation=update-cloudflared
configure=no

usage() {
	cat <<'EOF'
Frigotehnica cloudflared updater (run as root)
  sh update.sh                  Check and install a newer stable cloudflared
  sh update.sh --check          Check only; do not install or restart
  sh update.sh --status         Show schedule and last update result
  sh update.sh --interval 48    Check every 48 hours
  sh update.sh --enable         Enable scheduled updates
  sh update.sh --disable        Disable scheduled updates
  sh update.sh --legacy-ca      Allow unverified TLS for future updates
  sh update.sh --strict-ca      Restore TLS certificate verification

Configuration options may be combined. To update the complete Frigotehnica
application and Ajenti plugin, run the latest install.sh again.
EOF
}

# Build Go flags as individual positional arguments, never as shell code.
while [ "$#" -gt 0 ]; do
	case "$1" in
		--check) operation=check-cloudflared; shift ;;
		--status) operation=update-status; shift ;;
		--enable) enabled=true; configure=yes; shift ;;
		--disable) enabled=false; configure=yes; shift ;;
		--interval)
			[ "$#" -ge 2 ] || { echo '--interval requires hours' >&2; exit 1; }
			hours=$2; configure=yes; shift 2 ;;
		--legacy-ca) legacy=true; configure=yes; shift ;;
		--strict-ca) legacy=false; configure=yes; shift ;;
		--help|-h) usage; exit 0 ;;
		*) echo "Unknown option: $1" >&2; exit 1 ;;
	esac
done

[ "$(id -u)" -eq 0 ] || { echo 'Run as root' >&2; exit 1; }
[ -x "$UI_BINARY" ] || { echo 'Run the full Frigotehnica installer first' >&2; exit 1; }
set -- -update-config "$UPDATE_CONFIG"
if [ "$configure" = yes ]; then
	[ -z "${enabled:-}" ] || set -- "$@" "-update-enabled=$enabled"
	[ -z "${hours:-}" ] || set -- "$@" -update-interval-hours "$hours"
	[ -z "${legacy:-}" ] || set -- "$@" "-update-legacy-ca=$legacy"
	"$UI_BINARY" "$@" configure-updates
	if [ "${enabled:-}" = false ]; then
		if rc-service "$UPDATE_SERVICE" status >/dev/null 2>&1; then rc-service "$UPDATE_SERVICE" stop; fi
		rc-update del "$UPDATE_SERVICE" default >/dev/null 2>&1 || true
	elif [ "${enabled:-}" = true ]; then
		rc-update add "$UPDATE_SERVICE" default >/dev/null
		rc-service "$UPDATE_SERVICE" status >/dev/null 2>&1 || rc-service "$UPDATE_SERVICE" start
	fi
	# The running daemon rereads configuration at most one minute later.
	"$UI_BINARY" -update-config "$UPDATE_CONFIG" update-status
else
	exec "$UI_BINARY" "$@" "$operation"
fi

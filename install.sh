#!/bin/sh
set -eu

APP_DIR=/opt/frigotehnica
CONFIG_DIR=$APP_DIR/config
LOG_DIR=$APP_DIR/logs
UI_BINARY=$APP_DIR/frigotehnica-tunnel-ui
CLOUDFLARED_BINARY=$APP_DIR/cloudflared
TUNNEL_SERVICE=cloudflared-frigotehnica
UI_SERVICE=frigotehnica-tunnel-ui
UI_ARMV7_SHA256=695557831093239b92744afa1295d481c043d64b44c1e86cf0c6169caf78b9be
UI_AMD64_SHA256=PINNED_BY_RELEASE_WORKFLOW
CLOUDFLARED_ARMV7_SHA256=8e17268b7033061f505cd560eeafb04fdf020a354c975d1f0197bb63e9d0e0e5
CLOUDFLARED_AMD64_SHA256=fcfb02b575a52ca1af2e3267af4e1517bcdeb30ac48c834c69abaed3c0576ad2
RELEASE_BASE_URL=${FRIGOTEHNICA_RELEASE_BASE_URL:-https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/download/v1.0.0}
LISTEN_ADDRESS=
SITE_NAME="BOSS Site"
PUBLIC_HOSTNAME="Not configured"
TOKEN_FILE_SOURCE=
ENABLE_BOOT=yes
START_SERVICES=yes
INSECURE_DOWNLOADS=${FRIGOTEHNICA_INSECURE_DOWNLOADS:-no}

usage() {
	cat <<'EOF'
Frigotehnica Tunnel Control installer

Usage:
  sh install.sh [options]

Options:
  --listen IP[:PORT]       UI address (default: detected LAN IP on port 9080)
  --site-name NAME         Displayed site name
  --hostname HOSTNAME      Displayed Cloudflare public hostname
  --token-file PATH        Read tunnel token from a protected local file
  --release-base-url URL   Download missing release assets from URL
  --no-enable              Do not add services to the OpenRC default runlevel
  --no-start               Install files without starting services
  --help                   Show this help

Secrets are never accepted as command-line arguments. If --token-file is not
provided, the installer reads the token securely from /dev/tty.
EOF
}

die() { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

while [ "$#" -gt 0 ]; do
	case "$1" in
		--listen) [ "$#" -ge 2 ] || die "--listen requires a value"; LISTEN_ADDRESS=$2; shift 2 ;;
		--site-name) [ "$#" -ge 2 ] || die "--site-name requires a value"; SITE_NAME=$2; shift 2 ;;
		--hostname) [ "$#" -ge 2 ] || die "--hostname requires a value"; PUBLIC_HOSTNAME=$2; shift 2 ;;
		--token-file) [ "$#" -ge 2 ] || die "--token-file requires a value"; TOKEN_FILE_SOURCE=$2; shift 2 ;;
		--release-base-url) [ "$#" -ge 2 ] || die "--release-base-url requires a value"; RELEASE_BASE_URL=${2%/}; shift 2 ;;
		--no-enable) ENABLE_BOOT=no; shift ;;
		--no-start) START_SERVICES=no; shift ;;
		--help|-h) usage; exit 0 ;;
		*) die "unknown option: $1" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die "run this installer as root"
case "$(uname -m)" in
	armv7l|armv7*)
		UI_ASSET=frigotehnica-tunnel-ui-linux-armv7
		UI_SHA256=$UI_ARMV7_SHA256
		CLOUDFLARED_ASSET=cloudflared-linux-armhf-2026.8.2
		CLOUDFLARED_SHA256=$CLOUDFLARED_ARMV7_SHA256
		;;
	x86_64|amd64)
		UI_ASSET=frigotehnica-tunnel-ui-linux-amd64
		UI_SHA256=$UI_AMD64_SHA256
		CLOUDFLARED_ASSET=cloudflared-linux-amd64-2026.8.2
		CLOUDFLARED_SHA256=$CLOUDFLARED_AMD64_SHA256
		;;
	*) die "unsupported architecture: $(uname -m); supported: ARMv7 and x86_64" ;;
esac
command -v rc-service >/dev/null 2>&1 || die "OpenRC rc-service is required"
command -v rc-update >/dev/null 2>&1 || die "OpenRC rc-update is required"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"
command -v install >/dev/null 2>&1 || die "install is required"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || pwd)
STAGE_DIR=$APP_DIR/.install-stage.$$
BACKUP_DIR=$APP_DIR/backups/$(date +%Y%m%d-%H%M%S)
umask 077

cleanup() {
	case "$STAGE_DIR" in
		/opt/frigotehnica/.install-stage.[0-9]*) rm -f "$STAGE_DIR"/* 2>/dev/null || true; rmdir "$STAGE_DIR" 2>/dev/null || true ;;
	esac
}
trap cleanup EXIT HUP INT TERM

detect_lan_ip() {
	if command -v ip >/dev/null 2>&1; then
		ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}'
	elif command -v hostname >/dev/null 2>&1; then
		hostname -I 2>/dev/null | awk '{print $1}'
	fi
}

if [ -z "$LISTEN_ADDRESS" ]; then
	detected_ip=$(detect_lan_ip || true)
	[ -n "$detected_ip" ] || die "cannot detect LAN IP; pass --listen IP:9080"
	LISTEN_ADDRESS=$detected_ip:9080
elif ! echo "$LISTEN_ADDRESS" | grep -q ':'; then
	LISTEN_ADDRESS=$LISTEN_ADDRESS:9080
fi

case "$LISTEN_ADDRESS" in *[!0-9.:]*) die "listen address must be an IPv4 address with an optional port" ;; esac
case "$SITE_NAME" in *[!A-Za-z0-9_.\ -]*) die "site name contains unsupported characters" ;; esac
case "$PUBLIC_HOSTNAME" in *[!A-Za-z0-9.-]*) die "hostname contains unsupported characters" ;; esac

case "$LISTEN_ADDRESS" in
	127.*|localhost:*) info "UI will be available only locally at $LISTEN_ADDRESS" ;;
	*) info "WARNING: UI will use unencrypted HTTP on trusted LAN address $LISTEN_ADDRESS" ;;
esac

install -d -m 0755 "$APP_DIR"
install -d -m 0700 "$CONFIG_DIR"
install -d -m 0750 "$LOG_DIR"
install -d -m 0700 "$STAGE_DIR"

fetch_asset() {
	asset=$1
	destination=$2
	if [ -f "$SCRIPT_DIR/$asset" ]; then
		cp "$SCRIPT_DIR/$asset" "$destination"
	elif [ -n "$RELEASE_BASE_URL" ]; then
		command -v curl >/dev/null 2>&1 || die "curl is required to download release assets"
		info "Downloading $asset"
		if [ "$INSECURE_DOWNLOADS" = yes ]; then
			info "Legacy CA mode: TLS certificate verification is disabled; SHA-256 verification remains mandatory"
			curl -kfL --proto '=https' --tlsv1.2 --connect-timeout 20 --max-time 300 \
				-o "$destination" "$RELEASE_BASE_URL/$asset"
		else
			curl -fL --proto '=https' --tlsv1.2 --connect-timeout 20 --max-time 300 \
				-o "$destination" "$RELEASE_BASE_URL/$asset"
		fi
	else
		die "$asset is missing; place it next to install.sh or pass --release-base-url"
	fi
}

verify_asset() {
	file=$1
	expected=$2
	actual=$(sha256sum "$file" | awk '{print $1}')
	[ "$actual" = "$expected" ] || die "SHA-256 mismatch for $(basename "$file")"
}

fetch_asset "$UI_ASSET" "$STAGE_DIR/$UI_ASSET"
fetch_asset "$CLOUDFLARED_ASSET" "$STAGE_DIR/$CLOUDFLARED_ASSET"
verify_asset "$STAGE_DIR/$UI_ASSET" "$UI_SHA256"
verify_asset "$STAGE_DIR/$CLOUDFLARED_ASSET" "$CLOUDFLARED_SHA256"
info "Release asset checksums verified"

if [ -e "$UI_BINARY" ] || [ -e /etc/init.d/$UI_SERVICE ] || [ -e /etc/init.d/$TUNNEL_SERVICE ]; then
	install -d -m 0700 "$BACKUP_DIR"
	[ -e "$UI_BINARY" ] && cp -p "$UI_BINARY" "$BACKUP_DIR/"
	[ -e /etc/init.d/$UI_SERVICE ] && cp -p /etc/init.d/$UI_SERVICE "$BACKUP_DIR/"
	[ -e /etc/init.d/$TUNNEL_SERVICE ] && cp -p /etc/init.d/$TUNNEL_SERVICE "$BACKUP_DIR/"
	info "Existing program files backed up to $BACKUP_DIR"
fi

if [ -n "$TOKEN_FILE_SOURCE" ]; then
	[ -s "$TOKEN_FILE_SOURCE" ] || die "token file is missing or empty"
	cp "$TOKEN_FILE_SOURCE" "$STAGE_DIR/tunnel.token"
elif [ -s "$CONFIG_DIR/tunnel.token" ]; then
	cp "$CONFIG_DIR/tunnel.token" "$STAGE_DIR/tunnel.token"
	info "Existing Cloudflare tunnel token retained"
else
	[ -r /dev/tty ] || die "interactive token entry requires /dev/tty or --token-file"
	printf "Cloudflare tunnel token: " > /dev/tty
	stty -echo < /dev/tty
	IFS= read -r token < /dev/tty || true
	stty echo < /dev/tty
	printf "\n" > /dev/tty
	[ ${#token} -ge 80 ] || die "token is too short"
	case "$token" in *[!A-Za-z0-9._-]*) die "token contains unsupported characters" ;; esac
	printf '%s\n' "$token" > "$STAGE_DIR/tunnel.token"
	unset token
fi

if [ -s "$CONFIG_DIR/admin.auth" ]; then
	cp "$CONFIG_DIR/admin.auth" "$STAGE_DIR/admin.auth"
	info "Existing UI administrator password retained"
else
	[ -r /dev/tty ] || die "interactive password entry requires /dev/tty"
	printf "New UI administrator password (12+ characters): " > /dev/tty
	stty -echo < /dev/tty
	IFS= read -r admin_password < /dev/tty || true
	stty echo < /dev/tty
	printf "\nRepeat UI administrator password: " > /dev/tty
	stty -echo < /dev/tty
	IFS= read -r admin_password_repeat < /dev/tty || true
	stty echo < /dev/tty
	printf "\n" > /dev/tty
	[ "$admin_password" = "$admin_password_repeat" ] || die "passwords do not match"
	[ ${#admin_password} -ge 12 ] || die "administrator password is too short"
	printf '%s\n' "$admin_password" | "$STAGE_DIR/$UI_ASSET" hash-password > "$STAGE_DIR/admin.auth"
	unset admin_password admin_password_repeat
fi

escape_sed() { printf '%s' "$1" | sed 's/[&|]/\\&/g'; }
listen_escaped=$(escape_sed "$LISTEN_ADDRESS")
site_escaped=$(escape_sed "$SITE_NAME")
hostname_escaped=$(escape_sed "$PUBLIC_HOSTNAME")

cat > "$STAGE_DIR/$TUNNEL_SERVICE" <<'EOF'
#!/sbin/openrc-run
description="Cloudflare Tunnel for Carel BOSS"
command="/opt/frigotehnica/cloudflared"
command_args="tunnel --no-autoupdate --loglevel info --logfile /opt/frigotehnica/logs/cloudflared.log run --token-file /opt/frigotehnica/config/tunnel.token"
command_background=true
pidfile="/var/run/cloudflared-frigotehnica.pid"
output_log="/opt/frigotehnica/logs/cloudflared.log"
error_log="/opt/frigotehnica/logs/cloudflared.log"
retry="TERM/15/KILL/5"
depend() { need net; use dns logger; after localmount; }
start_pre() {
	[ -x "${command}" ] || { eerror "Missing cloudflared binary"; return 1; }
	[ -s /opt/frigotehnica/config/tunnel.token ] || { eerror "Missing tunnel token"; return 1; }
	touch "${output_log}" && chmod 0600 "${output_log}"
}
EOF

cat > "$STAGE_DIR/$UI_SERVICE" <<EOF
#!/sbin/openrc-run
description="Local Frigotehnica Cloudflare Tunnel control panel"
command="/opt/frigotehnica/frigotehnica-tunnel-ui"
command_args="-listen $listen_escaped -auth-file /opt/frigotehnica/config/admin.auth -token-file /opt/frigotehnica/config/tunnel.token -cloudflared /opt/frigotehnica/cloudflared -service cloudflared-frigotehnica -log-file /opt/frigotehnica/logs/cloudflared.log -site-name '$site_escaped' -hostname '$hostname_escaped'"
command_background=true
pidfile="/var/run/frigotehnica-tunnel-ui.pid"
output_log="/opt/frigotehnica/logs/tunnel-ui.log"
error_log="/opt/frigotehnica/logs/tunnel-ui.log"
retry="TERM/10/KILL/5"
depend() { need localmount; use net logger; after cloudflared-frigotehnica; }
start_pre() {
	[ -x "\${command}" ] || { eerror "Missing Tunnel Control binary"; return 1; }
	[ -s /opt/frigotehnica/config/admin.auth ] || { eerror "Missing admin authentication file"; return 1; }
	touch "\${output_log}" && chmod 0600 "\${output_log}"
}
EOF

if rc-service "$UI_SERVICE" status >/dev/null 2>&1; then rc-service "$UI_SERVICE" stop; fi
if rc-service "$TUNNEL_SERVICE" status >/dev/null 2>&1; then rc-service "$TUNNEL_SERVICE" stop; fi

install -m 0755 "$STAGE_DIR/$UI_ASSET" "$UI_BINARY"
install -m 0755 "$STAGE_DIR/$CLOUDFLARED_ASSET" "$CLOUDFLARED_BINARY"
install -m 0600 "$STAGE_DIR/tunnel.token" "$CONFIG_DIR/tunnel.token"
install -m 0600 "$STAGE_DIR/admin.auth" "$CONFIG_DIR/admin.auth"
install -m 0755 "$STAGE_DIR/$TUNNEL_SERVICE" /etc/init.d/$TUNNEL_SERVICE
install -m 0755 "$STAGE_DIR/$UI_SERVICE" /etc/init.d/$UI_SERVICE

if [ "$ENABLE_BOOT" = yes ]; then
	rc-update add "$TUNNEL_SERVICE" default >/dev/null
	rc-update add "$UI_SERVICE" default >/dev/null
	info "OpenRC boot services enabled"
fi

if [ "$START_SERVICES" = yes ]; then
	rc-service "$TUNNEL_SERVICE" start
	sleep 5
	rc-service "$TUNNEL_SERVICE" status >/dev/null 2>&1 || die "tunnel service did not start"
	rc-service "$UI_SERVICE" start
	sleep 2
	rc-service "$UI_SERVICE" status >/dev/null 2>&1 || die "UI service did not start"
	if command -v curl >/dev/null 2>&1; then
		curl -fsS --max-time 5 "http://$LISTEN_ADDRESS/login" >/dev/null || die "UI health check failed"
	fi
	info "Tunnel Control is available at http://$LISTEN_ADDRESS"
fi

info "Installation completed"

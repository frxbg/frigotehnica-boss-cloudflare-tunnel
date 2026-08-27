#!/bin/sh
set -eu

APP_DIR=/opt/frigotehnica
CONFIG_DIR=$APP_DIR/config
LOG_DIR=$APP_DIR/logs
UI_BINARY=$APP_DIR/frigotehnica-tunnel-ui
CLOUDFLARED_BINARY=$APP_DIR/cloudflared
TUNNEL_SERVICE=cloudflared-frigotehnica
UI_SERVICE=frigotehnica-tunnel-ui
UI_ARMV7_SHA256=a492ed5a5c136addce780806ae1fa8cd95b734f0c527029f604e51fa02839628
UI_AMD64_SHA256=PINNED_BY_RELEASE_WORKFLOW
CLOUDFLARED_ARMV7_SHA256=8e17268b7033061f505cd560eeafb04fdf020a354c975d1f0197bb63e9d0e0e5
CLOUDFLARED_AMD64_SHA256=fcfb02b575a52ca1af2e3267af4e1517bcdeb30ac48c834c69abaed3c0576ad2
RELEASE_BASE_URL=${FRIGOTEHNICA_RELEASE_BASE_URL:-https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/latest/download}
LISTEN_ADDRESS=
SITE_NAME="BOSS Site"
PUBLIC_HOSTNAME="not-configured"
TOKEN_FILE_SOURCE=
ENABLE_BOOT=yes
START_SERVICES=yes
INSECURE_DOWNLOADS=${FRIGOTEHNICA_INSECURE_DOWNLOADS:-no}
NON_INTERACTIVE=no
BOOTSTRAP_PASSWORD=
AJENTI_MODE=auto
AJENTI_ENABLED=no
AJENTI_PYTHON=
AJENTI_PLUGIN_ROOT=
AJENTI_PLUGIN_DIR=
AJENTI_SERVICE=
AJENTI_VARIANT=generic
PLUGIN_ONLY=no

usage() {
	cat <<'EOF'
Frigotehnica Tunnel Control installer

Usage:
  sh install.sh [options]

Options:
  --listen IP[:PORT]       Standalone UI address (default: detected LAN IP:9080)
  --site-name NAME         Displayed site name
  --hostname HOSTNAME      Displayed Cloudflare public hostname
  --token-file PATH        Read tunnel token from a protected local file
  --non-interactive        Install UI without prompting; print a one-time password
  --ajenti                 Require authenticated Ajenti integration on port 8443
  --plugin-only            Update only the Ajenti plugin; keep binaries, secrets, and services
  --no-ajenti              Disable automatic Ajenti integration
  --release-base-url URL   Download missing release assets from URL
  --no-enable              Do not add services to the OpenRC default runlevel
  --no-start               Install files without starting services
  --help                   Show this help

Secrets are never accepted as command-line arguments. In non-interactive mode,
configure the token and replace the generated one-time password in the UI.
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
		--non-interactive) NON_INTERACTIVE=yes; shift ;;
		--ajenti) AJENTI_MODE=yes; shift ;;
		--plugin-only) PLUGIN_ONLY=yes; AJENTI_MODE=yes; shift ;;
		--no-ajenti) AJENTI_MODE=no; shift ;;
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
		/opt/frigotehnica/.install-stage.[0-9]*) rm -rf "$STAGE_DIR" 2>/dev/null || true ;;
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

detect_ajenti() {
	# CAREL BOSS Ajenti profile.
	# BOSS runs Ajenti under Python 2.7 and loads CAREL plugins from
	# /home/webui/pvshell-web/plugins. The Ajenti plugin namespace is
	# created by ajenti-panel at runtime, so direct ajenti_plugin_core
	# imports are not a valid detection method here.
	if [ -x /usr/bin/ajenti-panel ] \
		&& [ -x /usr/bin/python2.7 ] \
		&& [ -d /home/webui/pvshell-web/plugins ] \
		&& [ -f /home/webui/pvshell-web/plugins/pvshell_settings/plugin.yml ]; then

		AJENTI_VARIANT=carel-boss
		AJENTI_PYTHON=/usr/bin/python2.7
		AJENTI_PLUGIN_ROOT=/home/webui/pvshell-web/plugins
		AJENTI_PLUGIN_DIR=$AJENTI_PLUGIN_ROOT/frigotehnica

		if [ -x /etc/init.d/web-interface ]; then
			AJENTI_SERVICE=web-interface
		elif [ -x /etc/init.d/plugins ]; then
			AJENTI_SERVICE=plugins
		elif [ -x /etc/init.d/ajenti ]; then
			AJENTI_SERVICE=ajenti
		fi

		return 0
	fi

	# Generic Ajenti detection.
	for candidate in /opt/ajenti/bin/python3 /opt/ajenti/bin/python /usr/bin/python3 /usr/bin/python; do
		[ -x "$candidate" ] || continue

		if "$candidate" -c 'import aj, ajenti_plugin_core' >/dev/null 2>&1; then
			root=$("$candidate" -c 'import os, ajenti_plugin_core; print(os.path.dirname(os.path.dirname(os.path.abspath(ajenti_plugin_core.__file__))))' 2>/dev/null || true)

			if [ -n "$root" ] && [ -d "$root" ]; then
				AJENTI_PYTHON=$candidate
				AJENTI_PLUGIN_ROOT=$root
				AJENTI_PLUGIN_DIR=$AJENTI_PLUGIN_ROOT/ajenti_plugin_frigotehnica
				AJENTI_VARIANT=generic
				return 0
			fi
		fi
	done

	return 1
}

if [ "$AJENTI_MODE" != no ] && detect_ajenti; then
	AJENTI_ENABLED=yes

	[ -n "$AJENTI_PLUGIN_DIR" ] || \
		AJENTI_PLUGIN_DIR=$AJENTI_PLUGIN_ROOT/ajenti_plugin_frigotehnica

	if [ -z "$AJENTI_SERVICE" ]; then
		[ -x /etc/init.d/ajenti ] && AJENTI_SERVICE=ajenti
		[ -z "$AJENTI_SERVICE" ] && [ -x /etc/init.d/plugins ] && AJENTI_SERVICE=plugins
	fi

	if [ "$AJENTI_VARIANT" = carel-boss ]; then
		info "CAREL BOSS Ajenti detected"
		info "Ajenti Python: $AJENTI_PYTHON"
		info "Ajenti plugin directory: $AJENTI_PLUGIN_DIR"
	else
		info "Ajenti detected"
	fi

	info "The UI will be added to Ajenti's authenticated Tools menu"
elif [ "$AJENTI_MODE" = yes ]; then
	die "Ajenti was requested but its plugin runtime was not detected"
fi

if [ -z "$LISTEN_ADDRESS" ]; then
	if [ "$AJENTI_ENABLED" = yes ]; then
		LISTEN_ADDRESS=127.0.0.1:9080
	else
		detected_ip=$(detect_lan_ip || true)
		[ -n "$detected_ip" ] || die "cannot detect LAN IP; pass --listen IP:9080"
		LISTEN_ADDRESS=$detected_ip:9080
	fi
elif ! echo "$LISTEN_ADDRESS" | grep -q ':'; then
	LISTEN_ADDRESS=$LISTEN_ADDRESS:9080
fi

if [ "$AJENTI_ENABLED" = yes ]; then
	case "$LISTEN_ADDRESS" in 127.*) : ;; *) die "Ajenti mode requires a loopback --listen address" ;; esac
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

if [ "$PLUGIN_ONLY" != yes ]; then
	fetch_asset "$UI_ASSET" "$STAGE_DIR/$UI_ASSET"
	fetch_asset "$CLOUDFLARED_ASSET" "$STAGE_DIR/$CLOUDFLARED_ASSET"
	verify_asset "$STAGE_DIR/$UI_ASSET" "$UI_SHA256"
	verify_asset "$STAGE_DIR/$CLOUDFLARED_ASSET" "$CLOUDFLARED_SHA256"
	chmod 0755 "$STAGE_DIR/$UI_ASSET" "$STAGE_DIR/$CLOUDFLARED_ASSET"
	info "Release asset checksums verified"
fi

stage_ajenti_plugin() {
	plugin_stage=$STAGE_DIR/ajenti_plugin_frigotehnica
	install -d -m 0755 "$plugin_stage"
if [ "$AJENTI_VARIANT" = carel-boss ]; then
	# CAREL BOSS runs Ajenti 2.1.21 on Python 2.7 with implicit sibling
	# imports. Findings from testing on a real BOSS unit:
	# - The gate XSRF middleware rejects authenticated requests outside a
	#   small prefix allowlist; '/view' is exempt.
	# - The core plugin registers a catch-all '/view/.*' SPA handler before
	#   this plugin, so an HttpPlugin route would never match. A
	#   HttpMiddleware runs before the central dispatcher, so the API proxy
	#   is mounted there under '/view/frigotehnica-tunnel/api/'.
	# - '/resources/<plugin>/<file>' file serving is restricted to core and
	#   pvshell_customizations. Templates must ship as prebuilt bundles:
	#   'resources/build/all.js' is aggregated into '/resources/all.js' and
	#   every '.html' resource from plugin.yml is inlined into
	#   '/resources/all.partials.js' via $templateCache.
	# The UI is a native Angular view (token field + connection status);
	# only the JSON API is proxied to the loopback backend.
	cat > "$plugin_stage/__init__.py" <<'PY'
import main
PY
	cat > "$plugin_stage/main.py" <<'PY'
from jadi import component
from aj.plugins.core.api.sidebar import SidebarItemProvider
from aj.api.http import HttpMiddleware

try:
    from urllib2 import Request, urlopen, HTTPError, URLError
except ImportError:
    from urllib.request import Request, urlopen
    from urllib.error import HTTPError, URLError

UPSTREAM = 'http://127.0.0.1:9080'
PROXY_PREFIX = '/view/frigotehnica-tunnel'
SECRET_FILE = '/opt/frigotehnica/config/ajenti-proxy.secret'

RESPONSE_HEADERS = ('Content-Type', 'Cache-Control', 'Content-Disposition')


@component(SidebarItemProvider)
class FrigotehnicaSidebarItem(SidebarItemProvider):
    def __init__(self, context):
        self.context = context

    def provide(self):
        return [{
            'attach': 'category:tools',
            'name': 'Cloudflare Tunnel',
            'icon': 'cloud',
            'url': PROXY_PREFIX,
            'children': [],
        }]


def _load_secret():
    try:
        with open(SECRET_FILE, 'rb') as f:
            return f.read(256).strip()
    except (IOError, OSError):
        return b''


def _rewrite_location(location):
    if not location:
        return None
    if location.startswith(UPSTREAM):
        location = location[len(UPSTREAM):]
    if location.startswith('/'):
        return PROXY_PREFIX + location
    return location


@component(HttpMiddleware)
class FrigotehnicaProxy(HttpMiddleware):
    """
    API reverse proxy for the Frigotehnica Tunnel Control backend.

    Runs as a middleware so it executes before the core plugin's catch-all
    '/view/.*' SPA handler. Only '/view/frigotehnica-tunnel/api/*' is
    proxied; the bare '/view/frigotehnica-tunnel' path falls through to the
    Angular SPA, which hosts this plugin's native view. The '/view' prefix
    is exempt from the CAREL gate XSRF check. Authentication is delegated to
    Ajenti: the proxy only runs when the worker session has an identity.
    """

    def handle(self, http_context):
        path = http_context.path
        if not path.startswith(PROXY_PREFIX + '/api/'):
            return None

        if not getattr(self.context, 'identity', None):
            http_context.respond('401 Unauthorized')
            http_context.add_header('Content-Type', 'text/plain; charset=utf-8')
            return 'Unauthenticated'

        secret = _load_secret()
        if not secret:
            http_context.respond_server_error()
            return 'Ajenti proxy secret is unavailable'

        subpath = path[len(PROXY_PREFIX):]
        target = UPSTREAM + subpath
        query = http_context.env.get('QUERY_STRING', '')
        if query:
            target += '?' + query

        method = http_context.method
        body = http_context.body if method in ('POST', 'PUT', 'PATCH', 'DELETE') else None
        headers = {
            'X-Frigotehnica-Ajenti': secret,
            'Accept': http_context.env.get('HTTP_ACCEPT', '*/*'),
        }
        content_type = http_context.env.get('CONTENT_TYPE')
        if content_type:
            headers['Content-Type'] = content_type

        request = Request(target, data=body, headers=headers)
        request.get_method = lambda: method
        try:
            response = urlopen(request, timeout=25)
        except HTTPError as error:
            response = error
        except (URLError, IOError, OSError):
            http_context.respond('502 Bad Gateway')
            http_context.add_header('Content-Type', 'text/plain; charset=utf-8')
            return 'Frigotehnica Tunnel Control is unavailable'

        data = response.read()
        http_context.respond('%d Proxy Response' % response.getcode())
        for name in RESPONSE_HEADERS:
            value = response.headers.get(name)
            if value:
                http_context.add_header(name, value)
        location = _rewrite_location(response.headers.get('Location'))
        if location:
            http_context.add_header('Location', location)
        return data
PY
	cat > "$plugin_stage/plugin.yml" <<'YAML'
name: frigotehnica
author: Frigotehnica
email: office@frigotehnica.com
url: https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel
version: '1.3.1-carel'
title: 'Frigotehnica Cloudflare Tunnel'
icon: cloud
dependencies:
    - !!python/object:aj.plugins.PluginDependency { plugin_name: core }
resources:
    - 'resources/module.js'
    - 'resources/view.html'
    - 'resources/style.css'
    - 'ng:ajenti.frigotehnica'
YAML
	install -d -m 0755 "$plugin_stage/resources/build"
	cat > "$plugin_stage/resources/module.js" <<'JS'
angular.module('ajenti.frigotehnica', ['core']);

angular.module('core').config(['$routeProvider', function ($routeProvider) {
  $routeProvider.when('/view/frigotehnica-tunnel', {
    templateUrl: '/frigotehnica:resources/view.html',
    controller: 'FrigotehnicaTunnelController'
  });
}]);

angular.module('ajenti.frigotehnica').controller('FrigotehnicaTunnelController', ['$scope', '$http', '$timeout', 'pageTitle', function ($scope, $http, $timeout, pageTitle) {
  pageTitle.set('Cloudflare Tunnel');

  var BASE = '/view/frigotehnica-tunnel';

  $scope.status = null;
  $scope.token = '';
  $scope.busy = false;
  $scope.message = null;
  $scope.error = null;

  function fail(resp) {
    $scope.error = (resp && typeof resp.data === 'string' && resp.data) ? resp.data : 'Request failed';
  }

  $scope.refresh = function () {
    $scope.error = null;
    $http.get(BASE + '/api/status').then(function (resp) {
      $scope.status = resp.data;
    }, fail);
  };

  $scope.saveToken = function () {
    $scope.busy = true;
    $scope.error = null;
    $scope.message = null;
    $http.post(BASE + '/api/token', {token: $scope.token}).then(function () {
      $scope.message = 'Token saved.';
      $scope.token = '';
      $scope.refresh();
    }, fail)['finally'](function () {
      $scope.busy = false;
    });
  };

  $scope.service = function (action, confirm) {
    $scope.busy = true;
    $scope.error = null;
    $scope.message = null;
    $http.post(BASE + '/api/service/' + action, confirm ? {confirm: true} : {}).then(function () {
      $scope.message = 'Done.';
      $timeout($scope.refresh, 4000);
    }, fail)['finally'](function () {
      $scope.busy = false;
    });
  };

  $scope.refresh();
}]);
JS
	cat > "$plugin_stage/resources/view.html" <<'HTML'
<br/>

<div class="frigotehnica-tunnel">
    <div class="alert alert-danger" ng:if="error">
        <i class="fa fa-warning"></i> {{error}}
    </div>
    <div class="alert alert-success" ng:if="message">{{message}}</div>

    <label>Connection status</label>
    <progress-spinner ng:hide="status != null"></progress-spinner>

    <div class="list-group" ng:if="status">
        <div class="list-group-item">
            <b>{{status.stateLabel}}</b>
            &mdash; {{status.siteName}} ({{status.hostname}})
        </div>
        <div class="list-group-item">
            cloudflared {{status.version}} ({{status.architecture}}),
            connections: {{status.connections}},
            uptime: {{status.uptime}},
            last check: {{status.lastCheck}}
        </div>
        <div class="list-group-item">{{status.serviceDetail}}</div>
        <div class="list-group-item">
            Tunnel token:
            <span class="text-success" ng:if="status.tokenPresent">configured</span>
            <span class="text-danger" ng:if="!status.tokenPresent">missing</span>
        </div>
    </div>

    <div class="form-group frigotehnica-token-group">
        <label>Cloudflare tunnel token</label>
        <div class="input-group">
            <input type="password" ng:model="token" class="form-control" placeholder="eyJ..." autocomplete="off"/>
            <div class="input-group-btn">
                <button class="btn btn-primary" ng:click="saveToken()" ng:disabled="busy || token.length < 80">Save</button>
            </div>
        </div>
    </div>

    <button class="btn btn-default" ng:click="refresh()" ng:disabled="busy">
        <i class="fa fa-refresh"></i> Refresh
    </button>
    <button class="btn btn-default" ng:click="service('restart')" ng:disabled="busy || !status || !status.tokenPresent">
        <i class="fa fa-refresh"></i> Restart tunnel
    </button>
    <button class="btn btn-danger" ng:click="service('stop', true)" ng:disabled="busy || !status || status.state != 'connected'">
        <i class="fa fa-stop"></i> Stop tunnel
    </button>
</div>
HTML
	cat > "$plugin_stage/resources/style.css" <<'CSS'
.frigotehnica-tunnel{max-width:720px}
.frigotehnica-token-group{margin-top:20px}
CSS
	# Prebuilt bundles: CAREL serves 'resources/build/all.*' directly and
	# inlines plugin.yml HTML resources into all.partials.js. Plain JS/CSS
	# sources need no compilation, so the bundles are copies of the sources.
	cp "$plugin_stage/resources/module.js" "$plugin_stage/resources/build/all.js"
	cp "$plugin_stage/resources/style.css" "$plugin_stage/resources/build/all.css"
	: > "$plugin_stage/resources/build/all.vendor.js"
	: > "$plugin_stage/resources/build/all.vendor.css"
else
	install -d -m 0755 "$plugin_stage/resources"
	cat > "$plugin_stage/__init__.py" <<'PY'
from .main import *
from .views import *
PY
	cat > "$plugin_stage/main.py" <<'PY'
from jadi import component
from aj.plugins.core.api.sidebar import SidebarItemProvider

@component(SidebarItemProvider)
class FrigotehnicaSidebarItem(SidebarItemProvider):
    def __init__(self, context):
        self.context = context
    def provide(self):
        return [{'attach': 'category:tools', 'name': 'Cloudflare Tunnel', 'icon': 'cloud', 'url': '/view/frigotehnica-tunnel', 'children': []}]
PY
	cat > "$plugin_stage/views.py" <<'PY'
try:
    from urllib.request import Request, urlopen
    from urllib.error import HTTPError, URLError
except ImportError:
    from urllib2 import Request, urlopen, HTTPError, URLError
from jadi import component
from aj.api.http import url, HttpPlugin
from aj.api.endpoint import endpoint

UPSTREAM = 'http://127.0.0.1:9080/'
PROXY_PREFIX = '/api/frigotehnica/proxy'
SECRET_FILE = '/opt/frigotehnica/config/ajenti-proxy.secret'

def _load_secret():
    try:
        with open(SECRET_FILE, 'rb') as f:
            value = f.read(256).strip()
        if not isinstance(value, str):
            value = value.decode('ascii')
        return value
    except (IOError, OSError, UnicodeError):
        return ''

PROXY_SECRET = _load_secret()

def _rewrite_location(location):
    if not location:
        return location
    if location.startswith(UPSTREAM):
        location = '/' + location[len(UPSTREAM):]
    if location.startswith('/'):
        return PROXY_PREFIX + location
    return location

@component(HttpPlugin)
class FrigotehnicaProxy(HttpPlugin):
    def __init__(self, context):
        self.context = context
    @url(r'/api/frigotehnica/proxy(?:/(?P<path>.*))?')
    @endpoint(page=True, auth=True)
    def handle_proxy(self, http_context, path=None):
        if not PROXY_SECRET:
            http_context.respond_server_error()
            return b'Ajenti proxy secret is unavailable'
        target = UPSTREAM + (path or '')
        query = http_context.env.get('QUERY_STRING', '')
        if query:
            target += '?' + query
        body = http_context.body if http_context.method in ('POST', 'PUT', 'PATCH') else None
        headers = {'X-Frigotehnica-Ajenti': PROXY_SECRET, 'Accept': http_context.env.get('HTTP_ACCEPT', '*/*')}
        content_type = http_context.env.get('CONTENT_TYPE')
        if content_type:
            headers['Content-Type'] = content_type
        request = Request(target, data=body, headers=headers)
        request.get_method = lambda: http_context.method
        try:
            response = urlopen(request, timeout=25)
        except HTTPError as error:
            response = error
        except (URLError, IOError, OSError):
            http_context.respond('502 Bad Gateway')
            http_context.add_header('Content-Type', 'text/plain; charset=utf-8')
            return b'Frigotehnica Tunnel Control is unavailable'
        data = response.read()
        http_context.respond('%d Proxy Response' % response.getcode())
        for name in ('Content-Type', 'Cache-Control', 'Content-Disposition'):
            value = response.headers.get(name)
            if value:
                http_context.add_header(name, value)
        location = _rewrite_location(response.headers.get('Location'))
        if location:
            http_context.add_header('Location', location)
        http_context.add_header('Content-Length', str(len(data)))
        return data
PY
	cat > "$plugin_stage/plugin.yml" <<'YAML'
name: frigotehnica
author: Frigotehnica
email: office@frigotehnica.com
url: https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel
version: '1.3.1'
title: 'Frigotehnica Cloudflare Tunnel'
icon: cloud
dependencies:
    - !PluginDependency { plugin_name: core }
resources:
    - 'resources/module.js'
    - 'resources/view.html'
    - 'resources/style.css'
    - 'ng:ajenti.frigotehnica'
YAML
	cat > "$plugin_stage/resources/module.js" <<'JS'
angular.module('ajenti.frigotehnica', [])
  .config(['$routeProvider', function ($routeProvider) {
    $routeProvider.when('/view/frigotehnica-tunnel', {templateUrl: '/frigotehnica:resources/view.html', controller: 'FrigotehnicaTunnelController'});
  }])
  .controller('FrigotehnicaTunnelController', ['$scope', 'pageTitle', function ($scope, pageTitle) { pageTitle.set('Cloudflare Tunnel'); }]);
JS
	cat > "$plugin_stage/resources/view.html" <<'HTML'
<div class="frigotehnica-tunnel-view"><iframe class="frigotehnica-tunnel-frame" src="/api/frigotehnica/proxy/" title="Frigotehnica Cloudflare Tunnel Control"></iframe></div>
HTML
	cat > "$plugin_stage/resources/style.css" <<'CSS'
.frigotehnica-tunnel-view{height:calc(100vh - 90px);min-height:620px;margin:-15px;background:#f3f6fb}.frigotehnica-tunnel-frame{display:block;width:100%;height:100%;border:0;background:#f3f6fb}
CSS
fi
	find "$plugin_stage" -type f -exec chmod 0644 {} \;
}

if [ "$PLUGIN_ONLY" = yes ]; then
	[ "$AJENTI_ENABLED" = yes ] || die "--plugin-only requires a detected Ajenti installation"
	[ -x "$UI_BINARY" ] || die "Tunnel Control is not installed; run the full installer first"
	[ -s "$CONFIG_DIR/ajenti-proxy.secret" ] || die "Ajenti proxy secret is missing; run the full installer first"
	[ -f /etc/init.d/$UI_SERVICE ] || die "Tunnel Control service is missing; run the full installer first"
	grep -q -- '-proxy-secret-file' /etc/init.d/$UI_SERVICE || die "installed backend does not support the Ajenti proxy; run the full installer first"

	stage_ajenti_plugin
	if [ -d "$AJENTI_PLUGIN_DIR" ]; then
		install -d -m 0700 "$BACKUP_DIR"
		cp -Rp "$AJENTI_PLUGIN_DIR" "$BACKUP_DIR/"
		info "Existing Ajenti plugin backed up to $BACKUP_DIR"
	fi
	rm -rf "$AJENTI_PLUGIN_DIR"
	install -d -m 0755 "$AJENTI_PLUGIN_DIR"
	cp -a "$STAGE_DIR/ajenti_plugin_frigotehnica/." "$AJENTI_PLUGIN_DIR/"
	if [ "$AJENTI_VARIANT" = carel-boss ]; then
		chgrp webui "$CONFIG_DIR/ajenti-proxy.secret" 2>/dev/null || true
		chmod 0640 "$CONFIG_DIR/ajenti-proxy.secret"
	fi
	info "Ajenti plugin updated without changing Tunnel Control binaries, configuration, or OpenRC services"
	if [ -n "$AJENTI_SERVICE" ]; then
		info "Ajenti will restart in 3 seconds to load the plugin"
		nohup sh -c "sleep 3; rc-service '$AJENTI_SERVICE' restart" >/dev/null 2>&1 &
	else
		info "Restart Ajenti or reboot the device once to load the updated plugin"
	fi
	exit 0
fi

if [ "$AJENTI_ENABLED" = yes ]; then
	if [ -s "$CONFIG_DIR/ajenti-proxy.secret" ]; then
		cp "$CONFIG_DIR/ajenti-proxy.secret" "$STAGE_DIR/ajenti-proxy.secret"
	else
		printf '%s%s\n' "$("$STAGE_DIR/$UI_ASSET" generate-password)" "$("$STAGE_DIR/$UI_ASSET" generate-password)" > "$STAGE_DIR/ajenti-proxy.secret"
	fi
	stage_ajenti_plugin
fi

if [ -e "$UI_BINARY" ] || [ -e /etc/init.d/$UI_SERVICE ] || [ -e /etc/init.d/$TUNNEL_SERVICE ]; then
	install -d -m 0700 "$BACKUP_DIR"
	[ -e "$UI_BINARY" ] && cp -p "$UI_BINARY" "$BACKUP_DIR/"
	[ -e /etc/init.d/$UI_SERVICE ] && cp -p /etc/init.d/$UI_SERVICE "$BACKUP_DIR/"
	[ -e /etc/init.d/$TUNNEL_SERVICE ] && cp -p /etc/init.d/$TUNNEL_SERVICE "$BACKUP_DIR/"
	[ "$AJENTI_ENABLED" = yes ] && [ -d "$AJENTI_PLUGIN_DIR" ] && cp -Rp "$AJENTI_PLUGIN_DIR" "$BACKUP_DIR/"
	info "Existing program files backed up to $BACKUP_DIR"
fi

if [ -n "$TOKEN_FILE_SOURCE" ]; then
	[ -s "$TOKEN_FILE_SOURCE" ] || die "token file is missing or empty"
	cp "$TOKEN_FILE_SOURCE" "$STAGE_DIR/tunnel.token"
elif [ -s "$CONFIG_DIR/tunnel.token" ]; then
	cp "$CONFIG_DIR/tunnel.token" "$STAGE_DIR/tunnel.token"
	info "Existing Cloudflare tunnel token retained"
elif [ "$NON_INTERACTIVE" = yes ]; then
	info "Cloudflare tunnel token will be configured in the web interface"
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
elif [ "$NON_INTERACTIVE" = yes ]; then
	BOOTSTRAP_PASSWORD=$("$STAGE_DIR/$UI_ASSET" generate-password)
	printf '%s\n' "$BOOTSTRAP_PASSWORD" | "$STAGE_DIR/$UI_ASSET" hash-password > "$STAGE_DIR/admin.auth"
	: > "$STAGE_DIR/bootstrap.required"
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
proxy_args=
[ "$AJENTI_ENABLED" = yes ] && proxy_args=" -proxy-secret-file /opt/frigotehnica/config/ajenti-proxy.secret"

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
command_args="-listen $listen_escaped -auth-file /opt/frigotehnica/config/admin.auth -bootstrap-file /opt/frigotehnica/config/bootstrap.required -token-file /opt/frigotehnica/config/tunnel.token -cloudflared /opt/frigotehnica/cloudflared -service cloudflared-frigotehnica -log-file /opt/frigotehnica/logs/cloudflared.log -site-name '$site_escaped' -hostname '$hostname_escaped'$proxy_args"
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
if [ -s "$STAGE_DIR/tunnel.token" ]; then
	install -m 0600 "$STAGE_DIR/tunnel.token" "$CONFIG_DIR/tunnel.token"
fi
install -m 0600 "$STAGE_DIR/admin.auth" "$CONFIG_DIR/admin.auth"
if [ -e "$STAGE_DIR/bootstrap.required" ]; then
	install -m 0600 "$STAGE_DIR/bootstrap.required" "$CONFIG_DIR/bootstrap.required"
fi
if [ "$AJENTI_ENABLED" = yes ]; then
	install -m 0600 "$STAGE_DIR/ajenti-proxy.secret" "$CONFIG_DIR/ajenti-proxy.secret"
	if [ "$AJENTI_VARIANT" = carel-boss ]; then
		# The restricted Ajenti worker runs as the webui user; keep the
		# secret readable for it without exposing it to other accounts.
		chgrp webui "$CONFIG_DIR/ajenti-proxy.secret" 2>/dev/null || true
		chmod 0640 "$CONFIG_DIR/ajenti-proxy.secret"
	fi
	# The existing plugin directory was already backed up to BACKUP_DIR
	# (outside the Ajenti plugin search path). Remove it before copying so
	# stale files from older versions cannot linger, and never keep extra
	# plugin copies inside the plugin search path.
	rm -rf "$AJENTI_PLUGIN_DIR"
	install -d -m 0755 "$AJENTI_PLUGIN_DIR"
	cp -a "$STAGE_DIR/ajenti_plugin_frigotehnica/." "$AJENTI_PLUGIN_DIR/"
	info "Ajenti plugin installed to $AJENTI_PLUGIN_DIR"
fi
install -m 0755 "$STAGE_DIR/$TUNNEL_SERVICE" /etc/init.d/$TUNNEL_SERVICE
install -m 0755 "$STAGE_DIR/$UI_SERVICE" /etc/init.d/$UI_SERVICE

if [ "$ENABLE_BOOT" = yes ]; then
	rc-update add "$TUNNEL_SERVICE" default >/dev/null
	rc-update add "$UI_SERVICE" default >/dev/null
	info "OpenRC boot services enabled"
fi

if [ "$START_SERVICES" = yes ]; then
	if [ -s "$CONFIG_DIR/tunnel.token" ]; then
		rc-service "$TUNNEL_SERVICE" start
		sleep 5
		rc-service "$TUNNEL_SERVICE" status >/dev/null 2>&1 || die "tunnel service did not start"
	else
		info "Tunnel service will start after the token is configured in the UI"
	fi
	rc-service "$UI_SERVICE" start
	sleep 2
	rc-service "$UI_SERVICE" status >/dev/null 2>&1 || die "UI service did not start"
	if command -v curl >/dev/null 2>&1; then
		curl -fsS --max-time 5 "http://$LISTEN_ADDRESS/login" >/dev/null || die "UI health check failed"
	fi
	if [ "$AJENTI_ENABLED" = yes ]; then
		info "Tunnel Control will be available from Ajenti on port 8443"
	else
		info "Tunnel Control is available at http://$LISTEN_ADDRESS"
	fi
fi

info "Installation completed"
if [ -n "$BOOTSTRAP_PASSWORD" ] && [ "$AJENTI_ENABLED" != yes ]; then
	echo
	echo "============================================================"
	echo "ONE-TIME UI PASSWORD: $BOOTSTRAP_PASSWORD"
	echo "Open http://$LISTEN_ADDRESS and change this password first."
	echo "Then configure the Cloudflare tunnel token in the UI."
	echo "============================================================"
fi

if [ "$AJENTI_ENABLED" = yes ]; then
	echo
	echo "Open Ajenti on port 8443 and select Tools > Cloudflare Tunnel."
	if [ -n "$AJENTI_SERVICE" ]; then
		info "Ajenti will restart in 3 seconds to load the plugin"
		nohup sh -c "sleep 3; rc-service '$AJENTI_SERVICE' restart" >/dev/null 2>&1 &
	else
		info "Restart Ajenti or reboot the device once to load the new plugin"
	fi
fi

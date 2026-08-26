import os

try:
    from urllib.request import Request, urlopen
    from urllib.error import HTTPError, URLError
except ImportError:  # Python 2 used by older Ajenti releases
    from urllib2 import Request, urlopen, HTTPError, URLError

from jadi import component
from aj.api.http import url, HttpPlugin
from aj.api.endpoint import endpoint


UPSTREAM = 'http://127.0.0.1:9080/'
PROXY_PREFIX = '/api/frigotehnica/proxy'
SECRET_FILE = '/opt/frigotehnica/config/ajenti-proxy.secret'


def _load_secret():
    try:
        with open(SECRET_FILE, 'rb') as secret_file:
            value = secret_file.read(256).strip()
        if not isinstance(value, str):
            value = value.decode('ascii')
        return value
    except (IOError, OSError, UnicodeError):
        return ''


# Ajenti imports plugins before creating authenticated session workers. Keeping
# the root-only secret in memory lets those isolated workers proxy safely.
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
        headers = {
            'X-Frigotehnica-Ajenti': PROXY_SECRET,
            'Accept': http_context.env.get('HTTP_ACCEPT', '*/*'),
        }
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
        status = response.getcode()
        http_context.respond('%d Proxy Response' % status)
        for name in ('Content-Type', 'Cache-Control', 'Content-Disposition'):
            value = response.headers.get(name)
            if value:
                http_context.add_header(name, value)
        location = _rewrite_location(response.headers.get('Location'))
        if location:
            http_context.add_header('Location', location)
        http_context.add_header('Content-Length', str(len(data)))
        return data


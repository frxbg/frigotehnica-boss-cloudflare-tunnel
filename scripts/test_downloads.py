"""Exercise the installer's real curl downloads against a local HTTPS server."""
import hashlib
import http.server
import os
import platform
import shlex
import shutil
import ssl
import subprocess
import tempfile
import threading
import unittest
from pathlib import Path


@unittest.skipUnless(platform.system() == "Linux" and shutil.which("curl") and shutil.which("openssl"),
                     "requires Linux curl and openssl")
class InstallerDownloadTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.folder = tempfile.TemporaryDirectory(prefix="frigotehnica-https-test-")
        cls.root = Path(cls.folder.name)
        cls.cert = cls.root / "cert.pem"
        key = cls.root / "key.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                        "-keyout", str(key), "-out", str(cls.cert), "-days", "1",
                        "-subj", "/CN=localhost", "-addext", "subjectAltName=IP:127.0.0.1"],
                       check=True, capture_output=True)
        cls.tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        cls.tls.load_cert_chain(cls.cert, key)
        source = (Path(__file__).resolve().parent.parent / "install.sh").read_text()
        start = source.index("fetch_asset() {")
        cls.definitions = source[start:source.index('\nif [ "$PLUGIN_ONLY" != yes ]; then', start)]

    @classmethod
    def tearDownClass(cls):
        cls.folder.cleanup()

    def download(self, statuses, legacy=False, bad_hash=False):
        payload = b"verified release asset\n"

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                index = len(self.server.requests)
                self.server.requests.append(self.path)
                status = statuses[min(index, len(statuses)-1)]
                body = payload if status == 200 else b"temporary gateway response\n"
                self.send_response(status)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        server.socket = self.tls.wrap_socket(server.socket, server_side=True)
        server.requests = []
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory(prefix="frigotehnica-download-test-") as folder:
                output = Path(folder) / "downloaded.bin"
                digest = hashlib.sha256(b"wrong payload" if bad_hash else payload).hexdigest()
                script = ('set -eu\ninfo() { printf "%s\\n" "$*"; }\n'
                          'die() { printf "%s\\n" "$*" >&2; exit 1; }\n' + self.definitions +
                          '\nfetch_asset asset.bin ' + shlex.quote(str(output)) +
                          '\nverify_asset ' + shlex.quote(str(output)) + ' ' + digest + '\n')
                env = dict(os.environ, SCRIPT_DIR=folder,
                           RELEASE_BASE_URL="https://127.0.0.1:%d" % server.server_port,
                           INSECURE_DOWNLOADS="yes" if legacy else "no",
                           CURL_CA_BUNDLE=str(self.cert))
                result = subprocess.run(["sh", "-c", script], env=env,
                                        capture_output=True, text=True, timeout=25)
                data = output.read_bytes() if output.exists() else None
                return result, data, len(server.requests), payload
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

    def test_504_then_success_with_strict_tls(self):
        result, data, requests, payload = self.download([504, 200])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(requests, 2)
        self.assertEqual(data, payload)

    def test_504_then_success_in_explicit_legacy_ca_mode(self):
        result, data, requests, payload = self.download([504, 200], legacy=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(requests, 2)
        self.assertEqual(data, payload)

    def test_permanent_404_fails_without_transient_retries(self):
        result, _, requests, _ = self.download([404])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(requests, 1)

    def test_successful_download_still_requires_matching_checksum(self):
        result, _, requests, _ = self.download([200], bad_hash=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(requests, 1)
        self.assertIn("SHA-256 mismatch", result.stderr)


if __name__ == "__main__":
    unittest.main()

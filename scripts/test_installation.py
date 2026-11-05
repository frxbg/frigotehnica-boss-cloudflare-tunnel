"""Exercise the real installer in a disposable tree, never in /opt or /etc.

Run on Linux after building the native UI:
  FRIGOTEHNICA_TEST_UI=/tmp/frigotehnica-amd64 python3 -m unittest discover -s scripts
"""
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


@unittest.skipUnless(platform.system() == "Linux" and os.environ.get("FRIGOTEHNICA_TEST_UI"),
                     "requires a native Linux UI binary")
class InstallerIntegrationTest(unittest.TestCase):
    def test_install_upgrade_preserves_schedule_and_newer_cloudflared(self):
        source = Path(__file__).resolve().parent.parent
        with tempfile.TemporaryDirectory(prefix="frigotehnica-install-test-") as folder:
            root = Path(folder)
            app = root / "opt/frigotehnica"
            init = root / "etc/init.d"
            mockbin = root / "bin"
            init.mkdir(parents=True)
            mockbin.mkdir()
            for name, content in {
                "id": "#!/bin/sh\necho 0\n",
                "rc-service": '#!/bin/sh\n[ "$2" != status ]\n',
                "rc-update": "#!/bin/sh\nexit 0\n",
            }.items():
                path = mockbin / name
                path.write_text(content)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(mockbin)+os.pathsep+os.environ["PATH"])
            ui = root / "frigotehnica-tunnel-ui-linux-amd64"
            shutil.copyfile(os.environ["FRIGOTEHNICA_TEST_UI"], ui)
            cloudflared = root / "cloudflared-linux-amd64"
            cloudflared.write_text("#!/bin/sh\necho 'cloudflared version 2026.10.2'\n")
            helper = root / "update.sh"
            shutil.copyfile(source / "update.sh", helper)
            installer = (source / "install.sh").read_text()
            for before, after in (("/opt/frigotehnica", str(app)),
                                  ("/etc/init.d", str(init))):
                installer = installer.replace(before, after)
            for key, asset in (("UI_AMD64_SHA256", ui),
                               ("CLOUDFLARED_AMD64_SHA256", cloudflared),
                               ("UPDATE_SCRIPT_SHA256", helper)):
                digest = hashlib.sha256(asset.read_bytes()).hexdigest()
                installer = re.sub(r"^"+key+r"=.*$", key+"="+digest, installer, flags=re.M)
            script = root / "install.sh"
            script.write_text(installer)
            base = ["sh", str(script), "--non-interactive", "--no-ajenti",
                    "--listen", "127.0.0.1:9080", "--no-enable", "--no-start"]
            subprocess.run(base+["--auto-update-hours", "48"], env=env, check=True, capture_output=True)
            config = app / "config/updates.json"
            self.assertEqual(json.loads(config.read_text())["intervalHours"], 48)
            self.assertTrue((init / "frigotehnica-cloudflared-update").exists())
            self.assertTrue((app / "update.sh").exists())
            self.assertFalse((app / "config/tunnel.token").exists())
            auth = (app / "config/admin.auth").read_bytes()
            # Reinstalling the project must retain newer upstream cloudflared.
            (app / "cloudflared").write_text("#!/bin/sh\necho 'cloudflared version 2027.1.0'\n")
            subprocess.run(base, env=env, check=True, capture_output=True)
            self.assertIn("2027.1.0", (app / "cloudflared").read_text())
            self.assertEqual(json.loads(config.read_text())["intervalHours"], 48)
            self.assertEqual((app / "config/admin.auth").read_bytes(), auth)
            subprocess.run(base+["--no-auto-update"], env=env, check=True, capture_output=True)
            self.assertFalse(json.loads(config.read_text())["enabled"])


if __name__ == "__main__":
    unittest.main()

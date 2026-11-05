import hashlib
import tempfile
import unittest
import shutil
import subprocess
from pathlib import Path
from release_commands import ACTIONS, command, merge_notes, render


class ReleaseCommandsTest(unittest.TestCase):
    def test_each_operation_has_all_variants_and_final_checksums(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            for script in ("install.sh", "uninstall.sh", "update.sh"):
                (root / script).write_bytes(("#!/bin/sh\n# final " + script).encode())
            text = render("v1.4.0", root)
            self.assertEqual(text.count("sha256sum -c -"), len(ACTIONS)*4)
            for script in ("install.sh", "uninstall.sh", "update.sh"):
                self.assertIn(hashlib.sha256((root / script).read_bytes()).hexdigest(), text)
                self.assertIn("releases/download/v1.4.0/"+script, text)
            self.assertIn("wget --no-check-certificate", text)
            self.assertIn("curl -kfsSL", text)
            self.assertIn("--legacy-ca && sh", text)

    def test_rerun_replaces_generated_notes_and_preserves_manual_text(self):
        notes = merge_notes("Manual release notes", "old commands")
        updated = merge_notes(notes, "new commands")
        self.assertIn("Manual release notes", updated)
        self.assertIn("new commands", updated)
        self.assertNotIn("old commands", updated)
        self.assertEqual(updated.count("<!-- release-commands:start -->"), 1)

    def test_generated_commands_parse_as_posix_shell(self):
        shell = shutil.which("sh")
        if not shell and Path("C:/Program Files/Git/bin/sh.exe").exists():
            shell = "C:/Program Files/Git/bin/sh.exe"
        if not shell:
            self.skipTest("POSIX shell unavailable")
        for _, script, args in ACTIONS:
            for downloader in ("curl", "wget"):
                for legacy in (False, True):
                    value = command("v1.4.0", script, args, "a"*64, downloader, legacy)
                    subprocess.run([shell, "-n", "-c", value], check=True, capture_output=True)


if __name__ == "__main__":
    unittest.main()

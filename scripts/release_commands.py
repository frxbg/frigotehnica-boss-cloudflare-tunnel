#!/usr/bin/env python3
"""Generate exact, checksum-pinned commands AFTER release scripts are pinned."""
import argparse
import hashlib
import re
from pathlib import Path

REPO = "https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel"
START = "<!-- release-commands:start -->"
END = "<!-- release-commands:end -->"
ACTIONS = (
    ("Install (Ajenti, non-interactive)", "install.sh", "--non-interactive --ajenti"),
    ("Update the complete project", "install.sh", "--non-interactive --ajenti"),
    ("Update cloudflared only", "update.sh", ""),
    ("Update the Ajenti plugin only", "install.sh", "--plugin-only"),
    ("Uninstall", "uninstall.sh", ""),
)


def command(tag, script, args, digest, downloader="curl", legacy=False):
    url = "%s/releases/download/%s/%s" % (REPO, tag, script)
    path = "/tmp/frigotehnica-%s-%s" % (tag, script)
    if downloader == "curl":
        fetch = "curl %sfsSL --retry 5 --retry-delay 2 --retry-max-time 180 %s -o %s" % ("-k" if legacy else "-", url, path)
        run = "sudo "
    else:
        fetch = "wget %s-qO %s %s" % ("--no-check-certificate " if legacy else "", path, url)
        run = ""  # Micro commands run in its root terminal.
    verify = "echo '%s  %s' | sha256sum -c -" % (digest, path)
    if legacy and script in ("install.sh", "update.sh"):
        run += "env FRIGOTEHNICA_INSECURE_DOWNLOADS=yes "
    run += "sh " + path
    if args:
        run += " " + args
    if legacy and script == "update.sh":
        # Apply the explicit legacy setting before checking upstream metadata.
        run += " --legacy-ca && sudo sh " + path if downloader == "curl" else " --legacy-ca && sh " + path
    return " && ".join((fetch, verify, run))


def render(tag, root):
    digests = {s: hashlib.sha256((root / s).read_bytes()).hexdigest()
               for s in ("install.sh", "update.sh", "uninstall.sh")}
    lines = ["# Commands for " + tag, "",
             "Generated from the final release scripts. Every command checks the exact script SHA-256 before execution.", "",
             "Install/project updates configure Ajenti; replace `--ajenti` with `--no-ajenti` for a standalone panel.", "",
             "The release bundles the latest stable cloudflared available at build time. The installed updater checks Cloudflare directly thereafter.", "",
             "Legacy CA commands disable TLS verification. Their pinned script hash authenticates the initial script against this reference; dynamically fetched upstream metadata in legacy update mode is not TLS-authenticated. Prefer fixing the device CA bundle and running `sh /opt/frigotehnica/update.sh --strict-ca`.", ""]
    for downloader, legacy in (("curl", False), ("wget", False), ("curl", True), ("wget", True)):
        title = "curl + sudo" if downloader == "curl" else "Micro: wget (root terminal)"
        if legacy:
            title += " — legacy CA"
        lines += ["## " + title, ""]
        for label, script, args in ACTIONS:
            lines += ["### " + label, "", "```sh",
                      command(tag, script, args, digests[script], downloader, legacy), "```", ""]
    lines += ["## Installed updater controls", "", "```sh",
              "sh /opt/frigotehnica/update.sh --check",
              "sh /opt/frigotehnica/update.sh --status",
              "sh /opt/frigotehnica/update.sh --interval 48 --enable",
              "sh /opt/frigotehnica/update.sh --disable", "```", "",
              "Use sudo when not running in a root terminal. Updates restart a running tunnel briefly; a stopped tunnel stays stopped.", ""]
    return "\n".join(lines)


def merge_notes(existing, commands):
    section = START + "\n" + commands + "\n" + END
    if START in existing and END in existing:
        return existing[:existing.index(START)].rstrip() + "\n\n" + section + existing[existing.index(END)+len(END):]
    return existing.rstrip() + "\n\n" + section + "\n"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--tag", required=True)
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?", args.tag):
        parser.error("expected a version tag such as v1.4.0")
    commands = render(args.tag, args.root)
    (args.root / "COMMANDS.md").write_text(commands, encoding="utf-8", newline="\n")
    readme = args.root / "README.md"
    content = readme.read_text(encoding="utf-8")
    content = re.sub(r"<!-- unreleased:start -->.*?<!-- unreleased:end -->\n?", "", content, flags=re.S)
    content = re.sub(r"Current release: `[^`]+`", "Current release: `%s`" % args.tag, content)
    readme.write_text(merge_notes(content, commands), encoding="utf-8", newline="\n")
    notes = args.root / "release-notes.md"
    existing = notes.read_text(encoding="utf-8") if notes.exists() else ""
    notes.write_text(merge_notes(existing, commands), encoding="utf-8", newline="\n")


if __name__ == "__main__":
    main()

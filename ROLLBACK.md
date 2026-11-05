# Rollback

## Automatic cloudflared update rollback

The updater retains the previous cloudflared binary as
`/opt/frigotehnica/cloudflared.previous`. If the replacement cannot start, it
restores the previous binary and attempts to start the tunnel again.
This verifies OpenRC startup, not end-to-end Cloudflare connectivity.

To disable subsequent scheduled update checks during investigation, run in a
root terminal, or add sudo:

```sh
sh /opt/frigotehnica/update.sh --disable
sh /opt/frigotehnica/update.sh --status
```

## Remove the project

Use the **Uninstall** command from the `COMMANDS.md` attached to the installed
release. It includes curl, Micro/wget, and legacy-CA variants with the exact
script checksum. See [uninstall instructions](README.md#uninstall).

The uninstaller removes the tunnel, management UI, updater services, and Ajenti
integration together. It preserves configuration, logs, backups, and the
previous cloudflared binary. Protect the retained tunnel token and other
credentials if the device is reassigned.

Do not remove individual binaries or authentication files while their services
are still installed. Use the coordinated uninstaller instead of the manual
removal commands from older releases.

# Frigotehnica BOSS Cloudflare Tunnel

Secure Cloudflare Tunnel installer and local management UI for ARMv7 and x86_64 Carel BOSS devices running Gentoo/OpenRC.

## Features

- One-line installation
- Official Cloudflare `cloudflared` binary
- SHA-256 verification
- Remotely managed Cloudflare Tunnel
- Local English management interface
- Secure tunnel token replacement
- Tunnel status and connection monitoring
- OpenRC startup services
- ARMv7 hard-float support
- Upgrade backups and safe uninstall

## Installation

Run as a user with `sudo` access:

```sh
curl -fsSL https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/latest/download/install.sh | sudo sh
```

The installer securely prompts for:

- Cloudflare tunnel token
- UI administrator password

It automatically detects the device LAN address and installs the panel on port `9080`.

After installation, open:

```text
http://DEVICE_IP:9080
```

## Legacy CA certificates

Some older Carel BOSS systems have an outdated CA certificate bundle and
cannot validate GitHub's HTTPS certificate. Do not pipe an unverified download
directly into a root shell. Each release provides a pinned one-line command for
these devices: it downloads the installer in legacy TLS mode, verifies its
exact SHA-256 digest, and only then runs it. Downloaded program binaries are
also checked against the SHA-256 values embedded in the verified installer.

Copy the version-specific legacy command from the latest release notes. Its
installer digest is pinned to that release and must not be reused for a newer
version.

## Custom installation

```sh
curl -fsSL https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/latest/download/install.sh \
  | sudo sh -s -- \
    --listen 192.168.0.177:9080 \
    --site-name "BOSS Test" \
    --hostname boss-test.example.com
```

## Requirements

- Carel BOSS or compatible embedded Linux device
- ARMv7 hard-float or x86_64 architecture
- Gentoo Linux with OpenRC
- Root or sudo access
- Outbound HTTPS and Cloudflare Tunnel connectivity
- `curl`, `sha256sum`, and standard POSIX utilities

## Installed files

```text
/opt/frigotehnica/cloudflared
/opt/frigotehnica/frigotehnica-tunnel-ui
/opt/frigotehnica/config/tunnel.token
/opt/frigotehnica/config/admin.auth
/opt/frigotehnica/logs/
/etc/init.d/cloudflared-frigotehnica
/etc/init.d/frigotehnica-tunnel-ui
```

## Service management

```sh
sudo rc-service cloudflared-frigotehnica status
sudo rc-service cloudflared-frigotehnica restart

sudo rc-service frigotehnica-tunnel-ui status
sudo rc-service frigotehnica-tunnel-ui restart
```

## Security

- Tokens and passwords are never accepted as command-line arguments.
- Secrets are read interactively from `/dev/tty`.
- The tunnel token and password hash are stored with mode `0600`.
- The UI does not accept arbitrary shell commands.
- SSH, Ajenti, and the Tunnel Control UI are not exposed through Cloudflare.
- LAN access currently uses HTTP and should only be enabled on a trusted network.

Never commit tunnel tokens, passwords, customer configurations, Carel firmware, or proprietary vendor files.

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/frxbg/frigotehnica-boss-cloudflare-tunnel/main/uninstall.sh | sudo sh
```

The uninstaller retains secrets and logs so they are not destroyed accidentally. Review `/opt/frigotehnica` before manually removing retained data.

## Supported platform

The current release targets:

```text
Architecture: ARMv7 hard-float or x86_64
Operating system: Gentoo Linux
Init system: OpenRC
Tested device: Carel BOSS
```

## Disclaimer

This is an independent community project and is not affiliated with or endorsed by CAREL or Cloudflare.

Use only on devices you own or are explicitly authorized to administer.

## License

MIT

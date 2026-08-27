# Frigotehnica BOSS Cloudflare Tunnel

Secure Cloudflare Tunnel installer and local management UI for ARMv7 and x86_64 Carel BOSS devices running Gentoo/OpenRC.

## Features

- One-line installation
- Non-interactive bootstrap for restricted browser terminals
- Authenticated Ajenti integration on port `8443`
- Native CAREL BOSS Ajenti view with an authenticated API-only proxy
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

When Ajenti is installed, the installer automatically:

- binds Tunnel Control to `127.0.0.1:9080` so the port is not exposed
- adds **Tools → Cloudflare Tunnel** to Ajenti
- uses the existing Ajenti session instead of a second login
- restarts Ajenti after the installation command has completed

Open Ajenti on port `8443` and select **Tools → Cloudflare Tunnel**.

The installer selects the correct release binaries automatically:

- `x86_64`/`amd64` for the larger BOSS systems
- ARMv7 hard-float for the smaller BOSS systems

On devices without Ajenti, the installer securely prompts for:

- Cloudflare tunnel token
- UI administrator password

It detects the device LAN address and installs the standalone panel on port `9080`.

After installation, open:

```text
http://DEVICE_IP:9080
```

## Non-interactive installation

For browser terminals that cannot provide interactive input, run:

```sh
curl -fsSL https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/latest/download/install.sh | sudo sh -s -- --non-interactive
```

With Ajenti, no second login or externally reachable port `9080` is needed.
Open **Tools → Cloudflare Tunnel** and paste the Cloudflare token in **Token
management**. Without Ajenti, the installer prints a random one-time UI
password for the standalone panel on port `9080`.

The non-interactive mode never creates an unauthenticated panel and never uses
a shared default password.

## Legacy CA certificates

Some older Carel BOSS systems have an outdated CA certificate bundle and
cannot validate GitHub's HTTPS certificate. Do not pipe an unverified download
directly into a root shell. Each release provides a pinned one-line command for
these devices: it downloads the installer in legacy TLS mode, verifies its
exact SHA-256 digest, and only then runs it. Downloaded program binaries are
also checked against the SHA-256 values embedded in the verified installer.

For `v1.3.0`, use this exact non-interactive command:

```sh
curl -kfsSL https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/download/v1.3.0/install.sh -o /tmp/frigotehnica-install-v1.3.0.sh && echo 'ba30aa90c720115e1975f5cf9cfc09463d3d81e71b87aa29d600d62a94b2757a  /tmp/frigotehnica-install-v1.3.0.sh' | sha256sum -c - && sudo env FRIGOTEHNICA_INSECURE_DOWNLOADS=yes sh /tmp/frigotehnica-install-v1.3.0.sh --non-interactive --ajenti
```

The installer digest is pinned to this release and must not be reused for a
newer version.

## Upgrade

Run the installation command again. The installer detects the platform,
downloads the matching `x86_64` or ARMv7 assets, backs up the existing
installation, replaces stale Ajenti plugin files, and restarts the affected
services:

```sh
curl -fsSL https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/latest/download/install.sh | sudo sh
```

See [CHANGELOG.md](CHANGELOG.md) for release details.

## Ajenti plugin-only update

Use this mode only when Tunnel Control is already installed and working, and
you want to replace just its Ajenti integration:

```sh
curl -fsSL https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/latest/download/install.sh | sudo sh -s -- --plugin-only
```

The command auto-detects CAREL BOSS or generic Ajenti, backs up the existing
plugin, installs the matching plugin resources, and restarts Ajenti. It does
not download or replace the Tunnel Control binary, `cloudflared`, tunnel token,
administrator authentication, or OpenRC service files. If the backend or its
Ajenti proxy secret is missing or too old, the command stops and instructs you
to run the full installer first.

## Custom installation

```sh
curl -fsSL https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/releases/latest/download/install.sh \
  | sudo sh -s -- \
    --listen 192.168.0.177:9080 \
    --site-name "BOSS Test" \
    --hostname boss-test.example.com
```

Use `--ajenti` to require Ajenti integration or `--no-ajenti` to force the
standalone LAN interface. Ajenti mode only permits a loopback listen address.

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
/opt/frigotehnica/config/ajenti-proxy.secret
/opt/frigotehnica/logs/
/etc/init.d/cloudflared-frigotehnica
/etc/init.d/frigotehnica-tunnel-ui
AJENTI_SITE_PACKAGES/ajenti_plugin_frigotehnica/
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
- Non-interactive installs generate a unique one-time password and require its replacement.
- Secrets are read interactively from `/dev/tty`.
- The tunnel token and password hash are stored with mode `0600`.
- Ajenti proxy requests require a unique root-only secret and a loopback source.
- Ajenti validates the user session before the plugin proxies any UI request.
- The UI does not accept arbitrary shell commands.
- Port `9080` is loopback-only when Ajenti integration is active.
- Standalone LAN access uses HTTP and should only be enabled on a trusted network.

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

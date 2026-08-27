# Changelog

All notable changes to Frigotehnica BOSS Cloudflare Tunnel are documented here.

## [1.3.2] - 2026-08-27

### Fixed

- Replaced the unchecked OpenRC `restart` call with a verified
  `stop → start → status` sequence.
- Applied the same verified restart after saving a new Cloudflare tunnel token,
  ensuring the new process reads the updated token file.
- Added read-back verification after the atomic token-file replacement.
- Added structural Cloudflare Tunnel token validation and rejected API tokens,
  truncated values, and malformed connector tokens before saving.
- Added the Tunnel ID embedded in the saved token to both management views so
  it can be compared with the intended Cloudflare dashboard tunnel.
- Stopped counting registered connections from older cloudflared log sessions;
  a running OpenRC service with no current registration now shows
  `Not connected` instead of `Connected`.
- Changed the displayed uptime to the `cloudflared` OpenRC process uptime so a
  successful restart is immediately visible.
- Replaced the ambiguous CAREL messages `Done.` and `Token saved.` with explicit
  verified restart results.

### Testing

- Added a unit test for the complete OpenRC restart and verification sequence.
- Verified Go tests and Linux ARMv7 and x86_64 cross-builds.

## [1.3.1] - 2026-08-27

### Added

- Added a real `--plugin-only` installer mode for updating only the detected
  CAREL BOSS or generic Ajenti plugin.
- Added plugin-only backup, compatibility checks, and Ajenti restart handling.
- Documented the plugin-only command and exactly which installed files it does
  and does not change.

### Changed

- Updated the generic Ajenti plugin version to `1.3.1` and the CAREL variant
  to `1.3.1-carel`.

## [1.3.0] - 2026-08-27

Changes since `v1.2.2-rc2`:

### Added

- Added a native Angular view for the legacy CAREL BOSS Ajenti interface.
- Added an authenticated API-only middleware proxy under
  `/view/frigotehnica-tunnel/api/`.
- Added CAREL-compatible prebuilt resource bundles: `build/all.js`,
  `build/all.css`, and empty vendor bundle placeholders.
- Added token management, tunnel status, refresh, restart, and stop controls to
  the native Ajenti view.
- Documented automatic `x86_64`/amd64 and ARMv7 hard-float binary selection.

### Changed

- Updated the Ajenti plugin version to `1.3.0` and the CAREL variant to
  `1.3.0-carel`.
- CAREL requests now use the existing authenticated Ajenti identity instead of
  exposing a separate panel login.
- Plugin installation now copies the complete staged resource tree, including
  prebuilt bundles, and removes stale files from older plugin versions.
- The CAREL proxy secret is installed with permissions suitable for the
  restricted `webui` worker while remaining unavailable to other users.
- Updated the pinned legacy-CA installation command for release `v1.3.0`.

### Fixed

- Fixed CAREL routing so the core `/view/.*` SPA handler serves the native view
  while only API requests are intercepted by the proxy middleware.
- Fixed POST, PUT, PATCH, and DELETE forwarding through the CAREL proxy.
- Restored CAREL plugin removal and service restart handling in `uninstall.sh`.

### Compatibility

- Larger CAREL BOSS systems: `x86_64`/amd64.
- Smaller CAREL BOSS systems: ARMv7 hard-float.
- Both architectures remain built and published by the release workflow.

[1.3.0]: https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/compare/v1.2.2-rc2...v1.3.0
[1.3.1]: https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/compare/v1.3.0...v1.3.1
[1.3.2]: https://github.com/frxbg/frigotehnica-boss-cloudflare-tunnel/compare/v1.3.1...v1.3.2

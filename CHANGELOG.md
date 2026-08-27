# Changelog

All notable changes to Frigotehnica BOSS Cloudflare Tunnel are documented here.

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

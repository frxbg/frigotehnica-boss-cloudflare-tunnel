# Rollback

## BOSS upload proxy only

First restore the previous working BOSS web route in Cloudflare (for example,
HTTPS to the device's reserved LAN IP with its previous TLS settings). Verify
the login page before stopping the proxy. Leave SSH and Ajenti routes intact.

```sh
rc-service frigotehnica-boss-proxy stop
rc-update del frigotehnica-boss-proxy default
```

These commands retain the files for recovery. A proxy update backs up the
previous executable as `boss-proxy.binary` and its service definition as
`boss-proxy.service` under `/opt/frigotehnica/backups/<timestamp>/`. To undo an
update, stop the proxy, restore those two files to their original paths with
mode `0755`, then start it again. CAREL application files are not modified.

## Entire installation

Run locally against the authorized Carel BOSS:

```sh
rc-service frigotehnica-tunnel-ui stop
rc-service frigotehnica-boss-proxy stop
rc-update del frigotehnica-boss-proxy default
rc-update del frigotehnica-tunnel-ui default
rc-service cloudflared-frigotehnica stop
rc-update del cloudflared-frigotehnica default
rm /etc/init.d/frigotehnica-tunnel-ui
rm /etc/init.d/cloudflared-frigotehnica
rm /opt/frigotehnica/frigotehnica-tunnel-ui
rm /etc/init.d/frigotehnica-boss-proxy
rm /opt/frigotehnica/frigotehnica-boss-proxy
rm /opt/frigotehnica/config/admin.auth
```

The commands above leave the verified `cloudflared` binary and tunnel token in place. To remove the complete installation after making a secure backup of anything still required:

```sh
rm -r /opt/frigotehnica
```


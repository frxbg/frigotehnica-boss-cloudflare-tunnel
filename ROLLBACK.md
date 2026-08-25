# Rollback

Run locally against the authorized Carel BOSS:

```sh
rc-service frigotehnica-tunnel-ui stop
rc-update del frigotehnica-tunnel-ui default
rc-service cloudflared-frigotehnica stop
rc-update del cloudflared-frigotehnica default
rm /etc/init.d/frigotehnica-tunnel-ui
rm /etc/init.d/cloudflared-frigotehnica
rm /opt/frigotehnica/frigotehnica-tunnel-ui
rm /opt/frigotehnica/config/admin.auth
```

The commands above leave the verified `cloudflared` binary and tunnel token in place. To remove the complete installation after making a secure backup of anything still required:

```sh
rm -r /opt/frigotehnica
```


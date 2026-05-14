# Tracker Setup

This project is meant to act as a lightweight `sendBeacon` endpoint for browsers.

## Quick setup

1. Build the binary with `./setup.sh` or `cd go && go build -o ../gount .`.
2. Place the correct GeoLite2 `.mmdb` file in `data/geodata/` or point `geodb_path` at your provisioned file.
3. Set a real `secret_salt` in `config.yaml`.
4. Optionally set `geodb_sha256` to verify the GeoLite2 file at startup.
5. Choose your storage backend with `db_type`.
6. If you run behind nginx or Caddy, set `trusted_proxies` and keep proxy rate limiting enabled.
7. Add the `navigator.sendBeacon()` snippet to your pages.
8. Verify `GET /health` and send a test `POST /t`.

## Browser example

```html
<script>
  navigator.sendBeacon('https://tracker.example.com/t', new URLSearchParams({
    p: location.pathname + location.search
  }));
</script>
```

## Reverse proxy reminder

Only configure `trusted_proxies` for IPs or CIDRs you actually control. If it is empty, gount ignores forwarding headers and uses the direct socket address.

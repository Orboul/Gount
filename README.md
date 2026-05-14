# About gount

`gount` is a lightweight, self-hosted beacon endpoint for browser tracking.

It is designed for `navigator.sendBeacon()` and a small `POST /t` surface. You run it yourself, point your site at the tracker, and it records pseudonymous visit events without cookies or third-party analytics.

## What it does

When a browser posts to `/t`, gount:

1. Resolves the visitor IP address.
2. Uses proxy headers only when the request came from a configured trusted proxy.
3. Creates a pseudonymous visitor ID from `IP + User-Agent + secret_salt`.
4. Looks up the visitor country, and optionally city, using a local GeoLite2 database.
5. Stores the visit in the configured backend.
6. Returns `204 No Content`.

## Tracking endpoint

```text
POST /t
```

Beacon body fields:

- `p` = page path or identifier to record
- `ref` = optional referrer/source label

Examples:

```text
p=/pricing
p=/blog/my-post?utm=summer&ref=newsletter
```

If `p` is omitted, gount stores `/`.

If `ref` is omitted, gount stores:

- the host from the browser `Referer` header, if present
- otherwise `Direct`

## Website integration

```html
<script>
  navigator.sendBeacon('https://tracker.example.com/t', new URLSearchParams({
    p: location.pathname + location.search
  }));
</script>
```

With an explicit referrer label:

```html
<script>
  navigator.sendBeacon('https://tracker.example.com/t', new URLSearchParams({
    p: location.pathname + location.search,
    ref: 'newsletter'
  }));
</script>
```

Replace `tracker.example.com` with your own tracker domain or server address.

## Setup

### Option 1: Run the setup script

From this repository checkout:

```bash
./setup.sh
```

The script:

- checks your Go version
- builds the binary from `go/`
- writes a starter `config.yaml`
- creates `data/geodata/` for your GeoLite2 file
- writes a small local README next to the binary
- optionally installs a `systemd` service on Linux

### Option 2: Build from source

```bash
cd go
go build -o ../gount .
```

The built binary is written to the repository root as `gount`.

### First run requirements

Before starting gount, you must:

1. Set a real `secret_salt` in `config.yaml` if you are using the default template.
2. Place the correct GeoLite2 `.mmdb` file at `geodb_path`, or at the default location:
   `data/geodata/GeoLite2-Country.mmdb` or `data/geodata/GeoLite2-City.mmdb`.
3. Optionally set `geodb_sha256` if you want startup checksum verification.

Start it with:

```bash
./gount
```

Or with an explicit config path:

```bash
./gount /path/to/config.yaml
```

## Configuration

Main settings in `config.yaml`:

| Option | Description |
|---|---|
| `server_ip` | IP address to bind to. Leave blank for all interfaces. |
| `server_port` | TCP port to listen on. |
| `secret_salt` | Secret used when hashing visitor IDs. Startup fails if blank or left at the placeholder. |
| `log_path` | JSONL log file for runtime events. Default: `data/logs/gount.jsonl`. |
| `strict_tracking_errors` | When `true`, `/t` returns `503` if storage fails. |
| `cors_allowed_origins` | Explicit origins allowed to call `/t` cross-origin. Empty denies cross-origin browser use. Set `["*"]` only if you intentionally want public collection. |
| `real_ip_header` | Which trusted proxy header source to use: `auto`, `x-forwarded-for`, `x-real-ip`, or `remote-addr`. |
| `trusted_proxies` | IPs or CIDRs allowed to supply `X-Forwarded-For` / `X-Real-IP`. |
| `track_rate_limit_rps` | Per-IP rate limit for `/t`. Default: `20`. Negative disables the limiter. |
| `track_rate_limit_burst` | Per-IP burst for `/t`. Default: `40`. |
| `health_rate_limit_rps` | Per-IP rate limit for `/health`. Default: `2`. Negative disables the limiter. |
| `health_rate_limit_burst` | Per-IP burst for `/health`. Default: `4`. |
| `db_type` | Storage backend: `sqlite`, `postgres`, `mysql`, `csv`, or `json`. |
| `db_path` | File path for file-based backends. |
| `db_dsn` | DSN for PostgreSQL or MySQL. |
| `geo_type` | `country` or `city`. |
| `geodb_path` | Path to the GeoLite2 database file. |
| `geodb_sha256` | Optional SHA-256 checksum for the provisioned GeoLite2 database file. |
| `retention_days` | How long visit records are kept. |

For most single-server setups, `sqlite` is the easiest option.

## Reverse proxy setup

If gount is behind nginx, Caddy, or another reverse proxy, configure `trusted_proxies` so it correctly identifies forwarded client IPs and add admission control in the proxy.

```yaml
trusted_proxies:
  - "127.0.0.1/32"
  - "::1/128"
```

nginx example:

```nginx
limit_req_zone $binary_remote_addr zone=gount_track:10m rate=20r/s;
limit_req_zone $binary_remote_addr zone=gount_health:1m rate=2r/s;

location = /t {
    limit_req zone=gount_track burst=40 nodelay;
    proxy_pass         http://127.0.0.1:8080;
    proxy_set_header   Host             $host;
    proxy_set_header   X-Forwarded-For  $proxy_add_x_forwarded_for;
    proxy_set_header   X-Real-IP        $remote_addr;
}

location = /health {
    limit_req zone=gount_health burst=4 nodelay;
    proxy_pass http://127.0.0.1:8080;
}
```

Caddy example:

```text
reverse_proxy 127.0.0.1:8080 {
    header_up X-Forwarded-For {remote_host}
    header_up X-Real-IP {remote_host}
}
```

If `trusted_proxies` is empty, gount ignores proxy headers and uses the direct socket address.

## Operations

Health check:

```text
GET /health -> 200 OK
HEAD /health -> 200 OK
```

Refreshing the GeoLite2 database:

1. Replace the `.mmdb` file with a newly downloaded copy.
2. Update `geodb_sha256` if you are verifying checksums.
3. Restart gount.

Example checksum command:

```bash
shasum -a 256 data/geodata/GeoLite2-Country.mmdb
```

Retention cleanup:

gount runs a background cleanup every 24 hours and deletes records older than `retention_days`.

Secret salt rotation:

```bash
openssl rand -hex 32
```

Replace `secret_salt` in `config.yaml` and restart. Rotating the salt changes derived visitor IDs, so existing anonymous IDs will no longer match newly generated ones.

## Notes

- `/t` rejects oversized bodies with `413 Payload Too Large`.
- `/health` only accepts `GET` and `HEAD`, and returns a generic unhealthy body.
- File backends like CSV and JSON are rewritten during retention cleanup.
- If you expose the service directly to the internet, leave `trusted_proxies` empty unless you truly have a proxy in front of it.

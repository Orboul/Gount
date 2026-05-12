# About gount

`gount` is a lightweight, self-hosted, privacy-first page view tracker written in Go.

It is designed for people who want simple traffic tracking without handing visitor data to a third party. You run it yourself, point your site at it, and it records visits using a small HTTP endpoint.

## What it does

When a browser requests the tracking endpoint, gount:

1. Resolves the visitor IP address.
2. Uses trusted proxy headers only when the request came from a configured reverse proxy.
3. Creates a pseudonymous visitor ID by hashing `IP + User-Agent + secret_salt`.
4. Looks up the visitor country, and optionally city, using a local GeoLite2 database.
5. Stores the visit in the configured backend.
6. Returns `204 No Content`.

## Why use it

- No cookies.
- No third-party analytics dependency.
- No raw IP storage.
- Small deployment footprint.
- Multiple storage backends.
- Works with both JavaScript and no-JavaScript tracking.

## Storage backends

gount supports:

- `sqlite`
- `postgres`
- `mysql`
- `csv`
- `json`

For most single-server setups, `sqlite` is the easiest option.

## How tracking works

The main endpoint is:

```text
GET /t
```

Common query parameters:

- `p` = page path or identifier to record
- `ref` = optional referrer/source label

Examples:

```text
/t?p=/pricing
/t?p=/blog/my-post&ref=newsletter
```

If `p` is omitted, gount stores `/`.

If `ref` is omitted, gount stores:

- the host from the browser `Referer` header, if present
- otherwise `Direct`

## Website integration

### JavaScript snippet

```html
<script>
  (function() {
    var path = encodeURIComponent(window.location.pathname + window.location.search);
    fetch('https://tracker.example.com/t?p=' + path, {
      method: 'GET',
      keepalive: true
    });
  })();
</script>
```

### Tracking pixel

```html
<img src="https://tracker.example.com/t?p=/landing-page"
     width="1" height="1" style="display:none" alt="">
```

### JS + noscript fallback

```html
<script>
  fetch('https://tracker.example.com/t?p=' + encodeURIComponent(location.pathname), {
    method: 'GET',
    keepalive: true
  });
</script>
<noscript>
  <img src="https://tracker.example.com/t?p=/landing-page"
       width="1" height="1" style="display:none" alt="">
</noscript>
```

Replace `tracker.example.com` with your own tracker domain or server address.

## Setup instructions

### Requirements

- Go 1.24 or newer if building from source
- A server or VPS to run the tracker
- A domain or subdomain if you want a friendly tracking URL

### Option 1: Use the provided binary

1. Download the binary for your platform.
2. Place it in its own directory on your server.
3. Run it once.
4. Edit the generated `config.yaml`.
5. Restart it.

On first run, gount will:

- create a default `config.yaml` if one does not exist
- download the required GeoLite2 database if it is missing
- create the selected storage backend when needed

### Option 2: Build from source

```bash
git clone https://github.com/youruser/gount
cd gount/go
go build -o ../gount .
```

The built binary will be written to the repository root as `gount`.

### Option 3: Use the setup script

The repository includes `setup.sh`, which:

- checks your Go version
- downloads the source
- builds the binary
- creates a starter `config.yaml`
- writes a small local README
- optionally installs a systemd service on Linux

Run:

```bash
./setup.sh
```

## Configuration

Main settings in `config.yaml`:

| Option | Description |
|---|---|
| `server_ip` | IP address to bind to. Leave blank for all interfaces. |
| `server_port` | TCP port to listen on. |
| `secret_salt` | Secret used when hashing visitor IDs. |
| `log_path` | JSONL log file for runtime events. Default: `data/logs/gount.jsonl`. |
| `trusted_proxies` | IPs or CIDRs allowed to supply `X-Forwarded-For` / `X-Real-IP`. |
| `db_type` | Storage backend: `sqlite`, `postgres`, `mysql`, `csv`, or `json`. |
| `db_path` | File path for file-based backends. |
| `db_dsn` | DSN for PostgreSQL or MySQL. |
| `geo_type` | `country` or `city`. |
| `geodb_path` | Path to the GeoLite2 database file. |
| `retention_days` | How long visit records are kept. |
| `update_frequency_days` | Reminder cadence for refreshing GeoLite2 data. |

All runtime logs are also written to a JSONL log file. By default that is `data/logs/gount.jsonl`.

## Reverse proxy setup

If gount is behind nginx, Caddy, or another reverse proxy, configure `trusted_proxies` so it will trust forwarded client IP headers.

Example:

```yaml
trusted_proxies:
  - "127.0.0.1/32"
  - "::1/128"
  - "10.0.0.0/8"
```

### nginx example

```nginx
location / {
    proxy_pass         http://127.0.0.1:8080;
    proxy_set_header   X-Forwarded-For  $proxy_add_x_forwarded_for;
    proxy_set_header   X-Real-IP        $remote_addr;
}
```

### Caddy example

```text
reverse_proxy 127.0.0.1:8080 {
    header_up X-Real-IP {remote_host}
}
```

If `trusted_proxies` is empty, gount ignores proxy headers and uses the direct socket address.

## Operating instructions

### Starting the server

Run the binary:

```bash
./gount
```

Or use an explicit config path:

```bash
./gount /path/to/config.yaml
```

### Health check

```text
GET /health -> 200 OK
```

Useful for:

- load balancers
- uptime monitors
- container health checks
- systemd watchdog or external probes

### Updating the GeoLite2 database

gount only downloads the database automatically when it is missing.

To refresh it later:

1. Delete the current `.mmdb` file.
2. Restart gount.

Example:

```bash
rm data/geodata/GeoLite2-Country.mmdb
./gount
```

### Retention cleanup

gount runs a background cleanup every 24 hours and deletes records older than `retention_days`.

### Secret salt rotation

Generate a new salt:

```bash
openssl rand -hex 32
```

Then replace `secret_salt` in `config.yaml` and restart the service.

Important: rotating the salt changes the derived visitor IDs, so existing anonymous IDs will no longer match newly generated ones.

## Running as a service

On Linux, you can run gount under `systemd`.

Example service:

```ini
[Unit]
Description=gount tracker
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/gount
ExecStart=/opt/gount/gount
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Suggested deployment flow

1. Build or download the binary.
2. Run it once to generate config and geodata.
3. Set a real `secret_salt`.
4. Configure your storage backend.
5. If using a reverse proxy, set `trusted_proxies`.
6. Put it behind nginx, Caddy, or your load balancer.
7. Add the tracking snippet to your site.
8. Verify `/health` and a test hit to `/t`.

## Good fit for

- personal sites
- self-hosted apps
- landing pages
- internal tools
- privacy-conscious projects

## Notes

- This is a page view tracker, not a full analytics suite.
- Geo accuracy depends on the GeoLite2 database.
- If you use file backends like CSV or JSON, cleanup rewrites the file.
- If you expose the service directly to the internet, leave `trusted_proxies` empty unless you truly have a proxy in front of it.

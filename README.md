# apkount

A lightweight, self-hosted, privacy-first page-view tracker written in Go.

- **No cookies.** Visitors are identified by a salted SHA-256 hash of their IP + User-Agent — the raw IP is never stored.
- **No external services.** Geo-lookup runs entirely from a local MaxMind GeoLite2 database.
- **One binary.** Drop it next to a `config.yaml` and run it. Everything else bootstraps itself on first launch.
- **Pluggable storage.** SQLite, PostgreSQL, MySQL, CSV, or JSON Lines — pick what fits your stack.

---

## How it works

The tracker exposes a single endpoint, `GET /t`. You embed it in your pages as either a tracking pixel or a JS `fetch`. When a request arrives the binary:

1. Extracts the real client IP (honours `X-Forwarded-For` / `X-Real-IP` from reverse proxies)
2. Derives a pseudonymous user ID: `SHA-256(ip | userAgent | secret_salt)`
3. Resolves the visitor's country (and optionally city) from the local GeoLite2 DB
4. Writes one row to the configured store
5. Returns `204 No Content` — zero bytes on the wire

A background goroutine prunes rows older than `retention_days` once every 24 hours.

---

## Quick start

```bash
# 1. Download the binary for your platform from the releases page, e.g.:
curl -LO https://github.com/youruser/apkount/releases/latest/download/apkount-linux-amd64
chmod +x apkount-linux-amd64

# 2. Run it — on first launch it writes config.yaml and downloads GeoLite2-Country.mmdb
./apkount-linux-amd64

# 3. Edit config.yaml (change secret_salt at minimum), then restart
```

After the first run the directory looks like this:

```
apkount-linux-amd64
config.yaml
data/
  visits.db           ← SQLite (default)
  geodata/
    GeoLite2-Country.mmdb
```

---

## Configuration reference

All settings live in `config.yaml` next to the binary. You can also pass an explicit path as the first CLI argument:

```bash
./apkount /etc/apkount/config.yaml
```

### Full annotated config

```yaml
# TCP port the HTTP server listens on.
server_port: 8080

# Secret salt mixed into the SHA-256 user ID hash.
# Treat this like a password — change it before deploying.
# Generate one with: openssl rand -hex 32
secret_salt: "CHANGE_ME_use_openssl_rand_hex_32"

# ── Storage ──────────────────────────────────────────────────────────────────

# Backend to use: sqlite | postgres | mysql | csv | json
db_type: "sqlite"

# File path for the sqlite / csv / json backends (relative to the binary).
# Leave blank to use the default for each type:
#   sqlite  →  data/visits.db
#   csv     →  data/visits.csv
#   json    →  data/visits.jsonl
db_path: ""

# Full DSN for postgres or mysql backends. Ignored for file-based backends.
# postgres:  postgres://user:pass@localhost:5432/apkount?sslmode=disable
# mysql:     user:pass@tcp(localhost:3306)/apkount?parseTime=true
db_dsn: ""

# ── Geo-lookup ───────────────────────────────────────────────────────────────

# GeoLite2 edition: "country" (ISO code only) or "city" (ISO code + city name).
# This also controls which edition is auto-downloaded on first run.
# Source: https://github.com/P3TERX/GeoLite.mmdb (no license key needed)
geo_type: "country"

# Path to the .mmdb file (relative to the binary).
# Leave blank to use the default:
#   data/geodata/GeoLite2-Country.mmdb  (geo_type: country)
#   data/geodata/GeoLite2-City.mmdb     (geo_type: city)
geodb_path: ""

# ── Retention ────────────────────────────────────────────────────────────────

# Delete visit records older than this many days.
# The cleanup runs once every 24 hours in the background.
retention_days: 90

# Reminder only — no automated refresh. MaxMind updates GeoLite2 weekly;
# bi-weekly is a sensible manual cadence.
update_frequency_days: 14
```

### Storage backends

| `db_type` | What you need | Notes |
|---|---|---|
| `sqlite` | nothing extra | Default. Good for single-server deployments. |
| `postgres` | set `db_dsn` | Table + index are created automatically on first run. |
| `mysql` | set `db_dsn` | MariaDB also works. Same auto-migration. |
| `csv` | nothing extra | Human-readable. Cleanup rewrites the file. |
| `json` | nothing extra | One JSON object per line (JSONL). Cleanup rewrites the file. |

The `visits` table / file schema is the same across all backends:

| Field | Type | Description |
|---|---|---|
| `unique_id` | string | SHA-256 hash — never the raw IP |
| `country` | string | ISO 3166-1 alpha-2 code, e.g. `US` |
| `city` | string | City name (empty unless `geo_type: city`) |
| `path` | string | Page path or URL |
| `timestamp` | integer | Unix seconds (UTC) |

---

## Integrating into a website

The tracker returns `204 No Content` with an empty body, so it works as both a pixel and a plain fetch.

### Option A — Tracking pixel (no JavaScript required)

Drop a 1×1 transparent image tag at the bottom of your `<body>`. The browser makes a GET request automatically, no JS needed.

```html
<img src="https://tracker.example.com/t?p=/your-page-path"
     width="1" height="1" style="display:none" alt="">
```

Replace `/your-page-path` with the page identifier you want recorded — or omit the `?p=` parameter and the tracker will fall back to the HTTP `Referer` header.

### Option B — JavaScript fetch (fires after page load)

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

`keepalive: true` ensures the request completes even if the user navigates away immediately.

### Option C — `<noscript>` fallback alongside JS

Use both: the JS version fires when scripts are allowed, the pixel fires when they are not.

```html
<script>
  fetch('https://tracker.example.com/t?p=' + encodeURIComponent(location.pathname));
</script>
<noscript>
  <img src="https://tracker.example.com/t" width="1" height="1" style="display:none" alt="">
</noscript>
```

### The `p` query parameter

| Scenario | What gets recorded |
|---|---|
| `?p=/blog/post-1` | `/blog/post-1` |
| No `?p=`, `Referer` header present | Full referrer URL |
| Neither | `/` |

---

## Running behind a reverse proxy

apkount reads `X-Forwarded-For` and `X-Real-IP` to get the real client IP. Make sure your proxy sets one of these headers.

**nginx:**
```nginx
location / {
    proxy_pass         http://127.0.0.1:8080;
    proxy_set_header   X-Forwarded-For  $proxy_add_x_forwarded_for;
    proxy_set_header   X-Real-IP        $remote_addr;
}
```

**Caddy:**
```
reverse_proxy 127.0.0.1:8080 {
    header_up X-Real-IP {remote_host}
}
```

---

## Updating the GeoLite2 database

The binary only auto-downloads the `.mmdb` on first run. To refresh it later, delete the file and restart — the binary will download the latest release automatically.

```bash
rm data/geodata/GeoLite2-Country.mmdb
systemctl restart apkount
```

MaxMind updates GeoLite2 every Tuesday. The `update_frequency_days` config value is a reminder only — it does not trigger any automated refresh.

---

## Building from source

Requires Go 1.22+.

```bash
git clone https://github.com/youruser/apkount
cd apkount
go build -o apkount .
```

To cross-compile for all platforms at once:

```bash
./build.sh
# Binaries written to dist/
```

---

## Health check

```
GET /health  →  200 OK  "ok"
```

Useful for load-balancer probes, uptime monitors, and `systemd` liveness checks.

---

## Running as a systemd service

```ini
[Unit]
Description=apkount tracker
After=network.target

[Service]
Type=simple
User=apkount
WorkingDirectory=/opt/apkount
ExecStart=/opt/apkount/apkount
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now apkount
```

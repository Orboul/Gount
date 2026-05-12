# gount

A lightweight, privacy-first page-view tracker. No cookies, no external services — just a single binary next to a config file.

---

## Starting gount

```bash
./gount
```

On first run, gount will automatically download the GeoLite2 database it needs for geo-lookup. Once it's ready you'll see:

```
[init] listening on :8080
```

To use a custom config path:

```bash
./gount /path/to/config.yaml
```

---

## Health check

```
GET /health  →  200 OK  "ok"
```

---

## Tracking your pages

Add one of the following to any page you want to track. Replace `tracker.example.com` with your own domain or server address.

**Tracking pixel** (no JavaScript required):
```html
<img src="https://tracker.example.com/t?p=/your-page" width="1" height="1" style="display:none" alt="">
```

**JavaScript fetch** (fires after page load):
```html
<script>
  fetch('https://tracker.example.com/t?p=' + encodeURIComponent(location.pathname), { keepalive: true });
</script>
```

---

## Key config options

All settings live in `config.yaml` next to the binary.

| Option | Default | Description |
|---|---|---|
| `server_ip` | *(blank = all)* | IP address to bind to (`127.0.0.1` for local only) |
| `server_port` | `8080` | Port the tracker listens on |
| `secret_salt` | *(generated)* | Salt for hashing visitor IDs — treat like a password |
| `db_type` | `sqlite` | Storage backend: `sqlite`, `postgres`, `mysql`, `csv`, `json` |
| `db_dsn` | *(empty)* | Connection string for postgres/mysql |
| `geo_type` | `country` | `country` (lightweight) or `city` (more storage + CPU) |
| `retention_days` | `90` | Days to keep visit records before auto-deletion |
| `update_frequency_days` | `14` | Reminder cadence for refreshing the GeoLite2 DB |

### Rotating the secret salt

```bash
# Generate a new salt
openssl rand -hex 32
```

Replace `secret_salt` in `config.yaml` and restart. Note: existing visitor IDs will no longer match after rotation.

### Refreshing the geo database

MaxMind updates GeoLite2 weekly. To refresh, delete the `.mmdb` file and restart — gount will re-download it automatically:

```bash
rm data/geodata/GeoLite2-Country.mmdb
./gount
```

---

## Directory layout

```
gount             ← the binary
config.yaml       ← your configuration
data/
  visits.db       ← SQLite database (default)
  geodata/
    GeoLite2-Country.mmdb
```

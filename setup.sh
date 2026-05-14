#!/usr/bin/env bash
set -euo pipefail

# ─── Config ───────────────────────────────────────────────────────────────────
REQUIRED="1.24.0"
REPO="${GOUNT_REPO:-https://github.com/orboul/gount.git}"
INSTALL_DIR="$(pwd)"
SERVICE_NAME="gount"
PROXY_CHOICE="none"
PROXY_DOMAIN=""
# ──────────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; DIM='\033[2m'; NC='\033[0m'
info()    { echo -e "  ${GREEN}✔${NC}  $*"; }
warn()    { echo -e "  ${YELLOW}!${NC}  $*"; }
error()   { echo -e "  ${RED}✘${NC}  $*" >&2; exit 1; }
step()    { echo -e "\n${CYAN}${BOLD}  $* ${NC}"; echo -e "  ${DIM}$(printf '─%.0s' {1..48})${NC}"; }
label()   { echo -e "  ${BOLD}$*${NC}"; }
ask()     { echo -e "  ${DIM}$*${NC}"; }

divider() { echo -e "\n  ${DIM}$(printf '━%.0s' {1..50})${NC}\n"; }

ensure_install_dir_ready() {
    if [[ -d "$INSTALL_DIR/gount" ]]; then
        error "A directory already exists at $INSTALL_DIR/gount. Run setup from a clean base directory, or remove/rename that folder first."
    fi
}

# ─── Go version check ─────────────────────────────────────────────────────────
check_go() {
    step "Checking Go"
    command -v go &>/dev/null || error "Go is not installed. Visit https://go.dev/dl/ to install Go $REQUIRED or newer."

    local installed
    installed="$(go version | awk '{print $3}' | sed 's/go//')"
    info "Found Go $installed"

    local oldest
    oldest="$(printf '%s\n' "$REQUIRED" "$installed" | sort -V | head -n1)"
    [[ "$oldest" == "$REQUIRED" ]] || error "Go $installed is too old — need $REQUIRED or newer. Visit https://go.dev/dl/"
    info "Go version OK"
}

# ─── Build ────────────────────────────────────────────────────────────────────
build_binary() {
    local source_dir
    source_dir="$(mktemp -d)"

    step "Downloading & Building" >&2
    info "Cloning source..." >&2
    git clone --filter=blob:none --sparse "$REPO" "$source_dir/repo" &>/dev/null
    cd "$source_dir/repo"
    git sparse-checkout set go &>/dev/null
    info "Source ready" >&2

    cd go
    info "Compiling..." >&2
    go build -o "$source_dir/gount" . || { echo -e "  ${RED}✘${NC}  Build failed." >&2; exit 1; }
    info "Build complete" >&2

    echo "$source_dir"
}

# ─── Install ──────────────────────────────────────────────────────────────────
install_binary() {
    local source_dir="$1"
    local target_path="$INSTALL_DIR/gount"

    step "Installing"
    mkdir -p "$INSTALL_DIR"
    install -m 755 "$source_dir/gount" "$target_path"
    mkdir -p "$INSTALL_DIR/data/geodata"
    info "Binary installed  →  $target_path"
    info "Binary is executable"
    info "GeoDB directory   →  $INSTALL_DIR/data/geodata"
}

# ─── Config ───────────────────────────────────────────────────────────────────
write_config() {
    local config_path="$INSTALL_DIR/config.yaml"

    if [[ -f "$config_path" ]]; then
        warn "config.yaml already exists — skipping (your settings are preserved)."
        return
    fi

    step "Configuration"

    # ── Bind IP ──
    echo
    label "Bind IP address"
    ask "Which IP address should gount listen on?"
    ask "Leave blank to listen on all interfaces (0.0.0.0)."
    ask "Enter 127.0.0.1 to accept only local connections."
    echo
    read -rp "  IP address [default: all interfaces]: " server_ip_input
    local server_ip="${server_ip_input:-}"
    if [[ -n "$server_ip" ]] && ! [[ "$server_ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$|^::1$|^[0-9a-fA-F:]+$ ]]; then
        warn "Value doesn't look like a valid IP — using blank (all interfaces)."
        server_ip=""
    fi
    if [[ -z "$server_ip" ]]; then
        info "Bind IP: all interfaces (0.0.0.0)"
    else
        info "Bind IP: $server_ip"
    fi

    # ── Storage backend ──
    echo
    label "Storage backend"
    ask "Where should visit data be saved?"
    echo
    echo -e "    ${BOLD}1)${NC} sqlite   ${DIM}— single file, no setup needed (default)${NC}"
    echo -e "    ${BOLD}2)${NC} postgres ${DIM}— PostgreSQL${NC}"
    echo -e "    ${BOLD}3)${NC} mysql    ${DIM}— MySQL / MariaDB${NC}"
    echo -e "    ${BOLD}4)${NC} csv      ${DIM}— plain CSV file${NC}"
    echo -e "    ${BOLD}5)${NC} json     ${DIM}— JSON Lines file${NC}"
    echo
    read -rp "  Choice [1-5, default 1]: " db_choice
    case "$db_choice" in
        2) db_type="postgres" ;;
        3) db_type="mysql"    ;;
        4) db_type="csv"      ;;
        5) db_type="json"     ;;
        *) db_type="sqlite"   ;;
    esac
    info "Storage: $db_type"

    if [[ "$db_type" == "postgres" || "$db_type" == "mysql" ]]; then
        warn "Credentials go in config.yaml — a placeholder has been added for you."
    fi

    # ── Geo accuracy ──
    divider
    label "Geo accuracy"
    ask "How precisely should visitor location be resolved?"
    echo
    echo -e "    ${BOLD}1)${NC} country ${DIM}— ISO country code only, lightweight (default)${NC}"
    echo -e "    ${BOLD}2)${NC} city    ${DIM}— country + city name, uses more storage and CPU${NC}"
    echo
    read -rp "  Choice [1-2, default 1]: " geo_choice
    case "$geo_choice" in
        2) geo_type="city"    ;;
        *) geo_type="country" ;;
    esac
    info "Geo accuracy: $geo_type"

    # ── Data retention ──
    divider
    label "Visitor data retention"
    ask "Visit records older than this will be automatically deleted."
    ask "(Cleanup runs every 24 hours in the background.)"
    echo
    read -rp "  Keep visits for how many days? [default 90]: " retention_input
    local retention_days="${retention_input:-90}"
    if ! [[ "$retention_days" =~ ^[0-9]+$ ]] || [[ "$retention_days" -lt 1 ]]; then
        warn "Invalid value — using 90."
        retention_days=90
    fi
    info "Data retention: $retention_days days"

    # ── Write file ──
    local salt
    salt="$(openssl rand -hex 32)"

    local db_dsn_placeholder=""
    if [[ "$db_type" == "postgres" ]]; then
        db_dsn_placeholder="postgres://user:pass@localhost:5432/gount?sslmode=disable"
    elif [[ "$db_type" == "mysql" ]]; then
        db_dsn_placeholder="user:pass@tcp(localhost:3306)/gount?parseTime=true"
    fi

    tee "$config_path" > /dev/null <<EOF
# gount configuration
# -------------------------------------------------------------------

# IP address the tracker binds to.
# Leave blank (or "0.0.0.0") to listen on all interfaces.
# Set to "127.0.0.1" to accept only local connections.
server_ip: "$server_ip"

# TCP port the tracker listens on.
server_port: 8080

# Secret salt used to hash visitor IDs — generated automatically by setup.
# To rotate: replace with a new value from: openssl rand -hex 32
# Note: changing this will make all existing visitor IDs unresolvable.
secret_salt: "$salt"

# Path to the JSONL log file (relative to the binary).
# Leave blank for the default:
#   data/logs/gount.jsonl
log_path: ""

# When true, /t returns 503 if a visit cannot be written to storage.
# When false, write failures are logged and /t still returns 204.
strict_tracking_errors: false

# Which browser origins may call /t cross-origin.
# Leave empty to deny cross-origin browser beacons by default.
# Set one or more explicit origins, for example:
#   - "https://example.com"
#   - "https://www.example.com"
# Or set:
#   - "*"
# only if you intentionally want public collection.
cors_allowed_origins: []

# Which source to use for the client IP when the request comes from a trusted
# proxy. Valid values:
#   auto             -> X-Forwarded-For, then X-Real-IP, then RemoteAddr
#   x-forwarded-for  -> X-Forwarded-For, then RemoteAddr
#   x-real-ip        -> X-Real-IP, then RemoteAddr
#   remote-addr      -> always use RemoteAddr
real_ip_header: "auto"

# Reverse proxies allowed to supply X-Forwarded-For / X-Real-IP.
# Add exact IPs or CIDR ranges, for example:
#   - "127.0.0.1/32"
#   - "::1/128"
#   - "10.0.0.0/8"
# Leave empty to ignore proxy headers entirely.
trusted_proxies: []

# Per-IP request limits.
# Zero uses the secure defaults. Set a negative value to disable a limiter.
track_rate_limit_rps: 20
track_rate_limit_burst: 40
health_rate_limit_rps: 2
health_rate_limit_burst: 4

# Storage backend: sqlite | postgres | mysql | csv | json
db_type: "$db_type"

# For sqlite/csv/json — path to the data file (relative to the binary).
# Leave blank to use the default (data/visits.db, etc.).
db_path: ""

# For postgres/mysql — fill in your connection string below.
# postgres: postgres://user:pass@localhost:5432/gount?sslmode=disable
# mysql:    user:pass@tcp(localhost:3306)/gount?parseTime=true
db_dsn: "$db_dsn_placeholder"

# GeoLite2 edition: "country" (ISO code only) or "city" (adds city name).
# city uses more storage and CPU.
geo_type: "$geo_type"

# Path to the GeoLite2 .mmdb file (relative to the binary).
# Leave blank for the default derived from geo_type.
geodb_path: ""

# Optional SHA-256 checksum for the GeoLite2 .mmdb file.
# Leave blank to skip checksum verification.
geodb_sha256: ""

# Delete visit records older than this many days (cleanup runs every 24 h).
retention_days: $retention_days
EOF

    echo
    info "Config written  →  $config_path"
    info "Secret salt generated automatically"
    warn "Place your GeoLite2 .mmdb file under $INSTALL_DIR/data/geodata or set geodb_path before starting gount."
    warn "If you want startup checksum verification, fill in geodb_sha256 after downloading the file."
    if [[ "$db_type" == "postgres" || "$db_type" == "mysql" ]]; then
        warn "Update db_dsn in config.yaml with your $db_type credentials before starting."
    fi
}

proxy_help() {
    local config_path="$INSTALL_DIR/config.yaml"
    local domain
    local reply

    divider
    label "Reverse proxy help"
    ask "Will you put gount behind nginx or Caddy for a domain like tracker.example.com?"
    echo
    echo -e "    ${BOLD}1)${NC} none    ${DIM}— no reverse proxy help${NC}"
    echo -e "    ${BOLD}2)${NC} nginx   ${DIM}— show nginx snippet + config reminder${NC}"
    echo -e "    ${BOLD}3)${NC} caddy   ${DIM}— show Caddy snippet + config reminder${NC}"
    echo
    read -rp "  Choice [1-3, default 1]: " reply
    case "$reply" in
        2) PROXY_CHOICE="nginx" ;;
        3) PROXY_CHOICE="caddy" ;;
        *) PROXY_CHOICE="none" ;;
    esac

    [[ "$PROXY_CHOICE" == "none" ]] && return

    echo
    read -rp "  Domain name [optional, e.g. tracker.example.com]: " domain
    PROXY_DOMAIN="${domain:-tracker.example.com}"

    echo
    info "Proxy setup is manual by design."
    warn "Edit $config_path and make sure these are set:"
    echo -e "    ${BOLD}trusted_proxies:${NC}"
    echo -e "    ${BOLD}  - \"127.0.0.1/32\"${NC}"
    echo -e "    ${BOLD}  - \"::1/128\"${NC}"
    echo -e "    ${BOLD}real_ip_header: \"auto\"${NC}"
    echo

    if [[ "$PROXY_CHOICE" == "nginx" ]]; then
        label "nginx example"
        cat <<EOF
limit_req_zone \$binary_remote_addr zone=gount_track:10m rate=20r/s;
limit_req_zone \$binary_remote_addr zone=gount_health:1m rate=2r/s;

server {
    server_name ${PROXY_DOMAIN};

    location = /t {
        limit_req          zone=gount_track burst=40 nodelay;
        proxy_pass         http://127.0.0.1:8080;
        proxy_set_header   Host             \$host;
        proxy_set_header   X-Forwarded-For  \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Real-IP        \$remote_addr;
    }

    location = /health {
        limit_req          zone=gount_health burst=4 nodelay;
        proxy_pass         http://127.0.0.1:8080;
    }
}
EOF
        echo
        ask "Typical nginx commands (adjust for your distro and file layout):"
        echo -e "    ${BOLD}sudo nano /etc/nginx/sites-available/${PROXY_DOMAIN}${NC}"
        echo -e "    ${BOLD}sudo ln -s /etc/nginx/sites-available/${PROXY_DOMAIN} /etc/nginx/sites-enabled/${PROXY_DOMAIN}${NC}"
        echo -e "    ${BOLD}sudo nginx -t${NC}"
        echo -e "    ${BOLD}sudo systemctl reload nginx${NC}"
    else
        label "Caddy example"
        cat <<EOF
${PROXY_DOMAIN} {
    reverse_proxy 127.0.0.1:8080 {
        header_up X-Forwarded-For {remote_host}
        header_up X-Real-IP {remote_host}
    }
}
EOF
        echo
        ask "Typical Caddy commands (adjust for your setup):"
        echo -e "    ${BOLD}sudo nano /etc/caddy/Caddyfile${NC}"
        echo -e "    ${BOLD}sudo caddy validate --config /etc/caddy/Caddyfile${NC}"
        echo -e "    ${BOLD}sudo systemctl reload caddy${NC}"
    fi
}

# ─── Write install README ─────────────────────────────────────────────────────
write_readme() {
    local readme_path="$INSTALL_DIR/README.md"

    tee "$readme_path" > /dev/null <<'EOF'
# gount

A lightweight, privacy-first beacon endpoint for browser tracking. No cookies, no external services — just a single binary next to a config file.

---

## Starting gount

```bash
./gount
```

Before first start:

- place the correct GeoLite2 `.mmdb` file in `data/geodata/` or set `geodb_path`
- optionally set `geodb_sha256` if you want checksum verification
- keep `secret_salt` unique and private

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

Add this to any page you want to track. Replace `tracker.example.com` with your own domain or server address.

**JavaScript sendBeacon** (fires without keeping the page alive):
```html
<script>
  navigator.sendBeacon('https://tracker.example.com/t', new URLSearchParams({
    p: location.pathname + location.search
  }));
</script>
```

If you omit `p`, gount stores `/`. If you omit `ref`, gount uses the request `Referer` host when available, otherwise `Direct`.

---

## Key config options

All settings live in `config.yaml` next to the binary.

| Option | Default | Description |
|---|---|---|
| `server_ip` | *(blank = all)* | IP address to bind to (`127.0.0.1` for local only) |
| `server_port` | `8080` | Port the tracker listens on |
| `secret_salt` | *(generated)* | Salt for hashing visitor IDs — treat like a password |
| `log_path` | `data/logs/gount.jsonl` | JSONL log file for runtime events |
| `strict_tracking_errors` | `false` | Return `503` on storage failure instead of always returning `204` |
| `cors_allowed_origins` | `[]` | Origins allowed to POST to `/t`; empty denies cross-origin use |
| `real_ip_header` | `auto` | Which trusted proxy header to use for the client IP |
| `trusted_proxies` | `[]` | IPs/CIDRs allowed to supply `X-Forwarded-For` / `X-Real-IP` |
| `track_rate_limit_rps` | `20` | Per-IP `/t` request rate limit (`< 0` disables) |
| `track_rate_limit_burst` | `40` | Per-IP `/t` burst limit |
| `health_rate_limit_rps` | `2` | Per-IP `/health` request rate limit (`< 0` disables) |
| `health_rate_limit_burst` | `4` | Per-IP `/health` burst limit |
| `db_type` | `sqlite` | Storage backend: `sqlite`, `postgres`, `mysql`, `csv`, `json` |
| `db_path` | *(auto)* | File path for sqlite/csv/json storage |
| `db_dsn` | *(empty)* | Connection string for postgres/mysql |
| `geo_type` | `country` | `country` (lightweight) or `city` (more storage + CPU) |
| `geodb_path` | *(auto)* | Path to the GeoLite2 `.mmdb` file |
| `geodb_sha256` | *(empty)* | Optional SHA-256 checksum for the GeoLite2 `.mmdb` file |
| `retention_days` | `90` | Days to keep visit records before auto-deletion |

### Rotating the secret salt

```bash
# Generate a new salt
openssl rand -hex 32
```

Replace `secret_salt` in `config.yaml` and restart. Note: existing visitor IDs will no longer match after rotation.

### Reverse proxies

gount ignores `X-Forwarded-For` and `X-Real-IP` unless the request comes from a configured `trusted_proxies` IP or CIDR. Example:

```yaml
trusted_proxies:
  - "127.0.0.1/32"
  - "::1/128"
```

### Refreshing the geo database

Replace the `.mmdb` file with the new copy, update `geodb_sha256` if you verify checksums, and restart:

```bash
shasum -a 256 data/geodata/GeoLite2-Country.mmdb
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
EOF

    info "README written   →  $readme_path"
}

# ─── Systemd service (Linux only) ────────────────────────────────────────────
install_systemd() {
    [[ "$(uname -s)" == "Linux" ]] || return 0
    command -v systemctl &>/dev/null || { warn "systemd not found — skipping service setup."; return 0; }

    step "Installing systemd service"

    sudo tee /etc/systemd/system/${SERVICE_NAME}.service > /dev/null <<EOF
[Unit]
Description=gount — lightweight privacy-first beacon endpoint
After=network.target

[Service]
Type=simple
User=$USER
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/gount
Restart=on-failure
RestartSec=5

NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    info "Service installed  →  /etc/systemd/system/${SERVICE_NAME}.service"
    echo
    echo -e "    ${BOLD}sudo systemctl start ${SERVICE_NAME}${NC}   ${DIM}start now${NC}"
    echo -e "    ${BOLD}sudo systemctl enable ${SERVICE_NAME}${NC}  ${DIM}start on boot${NC}"
    echo -e "    ${BOLD}sudo journalctl -u ${SERVICE_NAME} -f${NC}  ${DIM}view logs${NC}"
}

# ─── Cleanup prompt ───────────────────────────────────────────────────────────
cleanup_source() {
    local source_dir="$1"
    echo
    read -rp "  Remove downloaded source code? [Y/n] " reply
    if [[ "$reply" =~ ^[Nn]$ ]]; then
        info "Source kept at $source_dir"
    else
        rm -rf "$source_dir"
        info "Source removed"
    fi
}

# ─── Entry point ──────────────────────────────────────────────────────────────
main() {
    echo -e "${BOLD}"
    echo "   ██████╗  ██████╗ ██╗   ██╗███╗   ██╗████████╗"
    echo "  ██╔════╝ ██╔═══██╗██║   ██║████╗  ██║╚══██╔══╝"
    echo "  ██║  ███╗██║   ██║██║   ██║██╔██╗ ██║   ██║"
    echo "  ██║   ██║██║   ██║██║   ██║██║╚██╗██║   ██║"
    echo "  ╚██████╔╝╚██████╔╝╚██████╔╝██║ ╚████║   ██║"
    echo "   ╚═════╝  ╚═════╝  ╚═════╝ ╚═╝  ╚═══╝   ╚═╝"
    echo -e "${NC}  Privacy-first beacon endpoint\n"

    echo -e "  This will build and install gount to ${BOLD}$INSTALL_DIR${NC}."
    echo
    read -r -p "  Continue? [Y/n] " reply
    [[ "$reply" =~ ^[Nn]$ ]] && { echo "  Aborted."; exit 0; }
    echo

    ensure_install_dir_ready
    check_go

    local source_dir
    source_dir="$(build_binary)"

    install_binary "$source_dir"
    write_config
    proxy_help
    write_readme
    install_systemd
    cleanup_source "$source_dir"

    divider
    step "All done"
    echo
    info "gount installed   →  $INSTALL_DIR/gount"
    info "Config            →  $INSTALL_DIR/config.yaml"
    info "README            →  $INSTALL_DIR/README.md"
    echo
    echo -e "  To start gount:"
    echo -e "    ${BOLD}${INSTALL_DIR}/gount${NC}"
    
    echo
}

main "$@"

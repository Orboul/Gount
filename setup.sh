#!/usr/bin/env bash
set -euo pipefail

# ─── Config ───────────────────────────────────────────────────────────────────
REQUIRED="1.22.0"
REPO="https://github.com/Orboul/Gount.git"
INSTALL_DIR="$(pwd)/gount"
SERVICE_NAME="gount"
# ──────────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; DIM='\033[2m'; NC='\033[0m'
info()    { echo -e "  ${GREEN}✔${NC}  $*"; }
warn()    { echo -e "  ${YELLOW}!${NC}  $*"; }
error()   { echo -e "  ${RED}✘${NC}  $*" >&2; exit 1; }
step()    { echo -e "\n${CYAN}${BOLD}  $* ${NC}"; echo -e "  ${DIM}$(printf '─%.0s' {1..48})${NC}"; }
label()   { echo -e "  ${BOLD}$*${NC}"; }
ask()     { echo -e "  ${DIM}$*${NC}"; }

divider() { echo -e "\n  ${DIM}$(printf '━%.0s' {1..50})${NC}\n"; }

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
    go build -o "$source_dir/gount" ./... || { echo -e "  ${RED}✘${NC}  Build failed." >&2; exit 1; }
    info "Build complete" >&2

    echo "$source_dir"
}

# ─── Install ──────────────────────────────────────────────────────────────────
install_binary() {
    local source_dir="$1"

    step "Installing"
    mkdir -p "$INSTALL_DIR"
    install -m 755 "$source_dir/gount" "$INSTALL_DIR/gount"
    info "Binary installed  →  $INSTALL_DIR/gount"
}

# ─── Config ───────────────────────────────────────────────────────────────────
write_config() {
    local config_path="$INSTALL_DIR/config.yaml"

    if [[ -f "$config_path" ]]; then
        warn "config.yaml already exists — skipping (your settings are preserved)."
        return
    fi

    step "Configuration"

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

    # ── Geodata refresh reminder ──
    divider
    label "Geodata update reminder"
    ask "MaxMind updates the GeoLite2 database weekly. How often should"
    ask "gount remind you to refresh it?"
    echo
    read -rp "  Every how many days? [default 14]: " geo_update_input
    local update_frequency_days="${geo_update_input:-14}"
    if ! [[ "$update_frequency_days" =~ ^[0-9]+$ ]] || [[ "$update_frequency_days" -lt 1 ]]; then
        warn "Invalid value — using 14."
        update_frequency_days=14
    fi
    info "Geodata reminder: every $update_frequency_days days"

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

# TCP port the tracker listens on.
server_port: 8080

# Secret salt used to hash visitor IDs — generated automatically by setup.
# To rotate: replace with a new value from: openssl rand -hex 32
# Note: changing this will make all existing visitor IDs unresolvable.
secret_salt: "$salt"

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
# city uses more storage and CPU — the right edition is auto-downloaded on first run.
geo_type: "$geo_type"

# Path to the GeoLite2 .mmdb file (relative to the binary).
# Leave blank for the default derived from geo_type.
geodb_path: ""

# Delete visit records older than this many days (cleanup runs every 24 h).
retention_days: $retention_days

# How often (in days) to remind you to refresh the GeoLite2 DB.
# MaxMind updates it weekly — bi-weekly is a sensible cadence.
update_frequency_days: $update_frequency_days
EOF

    echo
    info "Config written  →  $config_path"
    info "Secret salt generated automatically"
    if [[ "$db_type" == "postgres" || "$db_type" == "mysql" ]]; then
        warn "Update db_dsn in config.yaml with your $db_type credentials before starting."
    fi
}

# ─── Write install README ─────────────────────────────────────────────────────
write_readme() {
    local readme_path="$INSTALL_DIR/README.md"

    tee "$readme_path" > /dev/null <<'EOF'
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
Description=gount — lightweight privacy-first page-view tracker
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
    echo "  ██████╗  ██████╗ ██╗   ██╗███╗   ██╗████████╗"
    echo "  ██╔════╝ ██╔═══██╗██║   ██║████╗  ██║╚══██╔══╝"
    echo "  ██║  ███╗██║   ██║██║   ██║██╔██╗ ██║   ██║"
    echo "  ██║   ██║██║   ██║██║   ██║██║╚██╗██║   ██║"
    echo "  ╚██████╔╝╚██████╔╝╚██████╔╝██║ ╚████║   ██║"
    echo "   ╚═════╝  ╚═════╝  ╚═════╝ ╚═╝  ╚═══╝   ╚═╝"
    echo -e "${NC}  Privacy-first page-view tracker\n"

    echo -e "  This will build and install gount to ${BOLD}$INSTALL_DIR${NC}."
    echo
    read -r -p "  Continue? [Y/n] " reply
    [[ "$reply" =~ ^[Nn]$ ]] && { echo "  Aborted."; exit 0; }
    echo

    check_go

    local source_dir
    source_dir="$(build_binary)"

    install_binary "$source_dir"
    write_config
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
    echo -e "  To start:  ${BOLD}$INSTALL_DIR/gount${NC}"
    echo
}

main "$@"

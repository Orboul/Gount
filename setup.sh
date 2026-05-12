#!/usr/bin/env bash
set -euo pipefail

# ─── Config ───────────────────────────────────────────────────────────────────
REPO="matthewsaw/gount"
INSTALL_DIR="/opt/gount"
BIN_NAME="gount"
SERVICE_NAME="gount"
# ──────────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'
info()    { echo -e "${GREEN}[gount]${NC} $*"; }
warn()    { echo -e "${YELLOW}[gount]${NC} $*"; }
error()   { echo -e "${RED}[gount]${NC} $*" >&2; exit 1; }
step()    { echo -e "\n${BOLD}── $* ${NC}"; }

# ─── Detect OS / arch ─────────────────────────────────────────────────────────
detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$arch" in
        x86_64|amd64)  arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        armv7*)        arch="arm"   ;;
        *) error "Unsupported architecture: $arch" ;;
    esac

    case "$os" in
        linux)  PLATFORM="linux-$arch"  ;;
        darwin) PLATFORM="darwin-$arch" ;;
        *) error "Unsupported OS: $os (only Linux and macOS are supported)" ;;
    esac

    info "Detected platform: $PLATFORM"
}

# ─── Dependency check ─────────────────────────────────────────────────────────
check_deps() {
    local missing=()
    for cmd in curl openssl; do
        command -v "$cmd" &>/dev/null || missing+=("$cmd")
    done
    [[ ${#missing[@]} -eq 0 ]] || error "Missing required tools: ${missing[*]}"
}

# ─── Fetch latest release tag from GitHub ────────────────────────────────────
latest_version() {
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | head -1 \
        | sed 's/.*"tag_name": *"\(.*\)".*/\1/'
}

# ─── Download binary ──────────────────────────────────────────────────────────
download_binary() {
    local version="$1"
    local url="https://github.com/${REPO}/releases/download/${version}/${BIN_NAME}-${PLATFORM}"
    local tmp
    tmp="$(mktemp)"

    info "Downloading $BIN_NAME $version for $PLATFORM..."
    if ! curl -fsSL --progress-bar "$url" -o "$tmp"; then
        rm -f "$tmp"
        error "Download failed. Check that release ${version} exists for ${PLATFORM}."
    fi

    echo "$tmp"
}

# ─── Install ──────────────────────────────────────────────────────────────────
install_binary() {
    local tmp="$1"

    step "Installing to $INSTALL_DIR"
    if [[ "$EUID" -ne 0 ]] && ! sudo -n true 2>/dev/null; then
        warn "Root access required to install to $INSTALL_DIR."
        warn "You will be prompted for your password."
    fi

    sudo mkdir -p "$INSTALL_DIR"
    sudo install -m 755 "$tmp" "$INSTALL_DIR/$BIN_NAME"
    rm -f "$tmp"
    info "Binary installed to $INSTALL_DIR/$BIN_NAME"
}

# ─── Config ───────────────────────────────────────────────────────────────────
write_config() {
    local config_path="$INSTALL_DIR/config.yaml"

    if [[ -f "$config_path" ]]; then
        warn "config.yaml already exists — skipping (your settings are preserved)."
        return
    fi

    step "Writing default config"
    local salt
    salt="$(openssl rand -hex 32)"

    sudo tee "$config_path" > /dev/null <<EOF
# gount configuration
# -------------------------------------------------------------------

# TCP port the tracker listens on.
server_port: 8080

# Secret salt mixed into the SHA-256 User ID hash.
# Pre-generated — change it if you want a fresh one:
#   openssl rand -hex 32
secret_salt: "$salt"

# Storage backend: sqlite | postgres | mysql | csv | json
db_type: "sqlite"

# For sqlite/csv/json — path to the data file (relative to the binary).
# Leave blank to use the default (data/visits.db, etc.).
db_path: ""

# For postgres/mysql — full DSN connection string.
# postgres: postgres://user:pass@localhost:5432/gount?sslmode=disable
# mysql:    user:pass@tcp(localhost:3306)/gount?parseTime=true
db_dsn: ""

# GeoLite2 edition: "country" (ISO code only) or "city" (adds city name).
# Controls which edition is auto-downloaded on first run.
geo_type: "country"

# Path to the GeoLite2 .mmdb file (relative to the binary).
# Leave blank for the default derived from geo_type.
geodb_path: ""

# Delete visit records older than this many days (cleanup runs every 24 h).
retention_days: 90

# Reminder cadence for refreshing the GeoLite2 DB (metadata only, no auto-refresh).
update_frequency_days: 14
EOF

    info "Config written to $config_path"
    warn "Review $config_path before running in production."
}

# ─── Systemd service (Linux only) ────────────────────────────────────────────
install_systemd() {
    [[ "$(uname -s)" == "Linux" ]] || return 0
    command -v systemctl &>/dev/null || { warn "systemd not found — skipping service setup."; return 0; }

    step "Installing systemd service"

    # Create a dedicated system user if it doesn't exist.
    if ! id "$SERVICE_NAME" &>/dev/null; then
        sudo useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_NAME"
        info "Created system user: $SERVICE_NAME"
    fi

    # Give the service user ownership of the install dir.
    sudo chown -R "$SERVICE_NAME:$SERVICE_NAME" "$INSTALL_DIR"

    sudo tee /etc/systemd/system/${SERVICE_NAME}.service > /dev/null <<EOF
[Unit]
Description=gount — lightweight privacy-first page-view tracker
After=network.target

[Service]
Type=simple
User=${SERVICE_NAME}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BIN_NAME}
Restart=on-failure
RestartSec=5

# Harden the service.
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=${INSTALL_DIR}
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    info "Service installed: /etc/systemd/system/${SERVICE_NAME}.service"
    echo
    echo -e "  Start now:        ${BOLD}sudo systemctl start ${SERVICE_NAME}${NC}"
    echo -e "  Enable on boot:   ${BOLD}sudo systemctl enable ${SERVICE_NAME}${NC}"
    echo -e "  View logs:        ${BOLD}sudo journalctl -u ${SERVICE_NAME} -f${NC}"
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

    check_deps
    detect_platform

    echo -e "This will download and install gount to ${BOLD}$INSTALL_DIR${NC}."
    read -r -p "  Continue? [y/N] " reply
    [[ "$reply" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }
    echo

    step "Fetching latest release"
    VERSION="${VERSION:-$(latest_version)}"
    [[ -n "$VERSION" ]] || error "Could not determine latest version. Set VERSION=vX.Y.Z to override."
    info "Version: $VERSION"

    local tmp
    tmp="$(download_binary "$VERSION")"
    install_binary "$tmp"
    write_config
    install_systemd

    step "Done"
    info "gount $VERSION is installed at $INSTALL_DIR/$BIN_NAME"
    echo
    echo -e "  Run manually:   ${BOLD}$INSTALL_DIR/$BIN_NAME${NC}"
    echo -e "  Config file:    ${BOLD}$INSTALL_DIR/config.yaml${NC}"
    echo
    warn "Remember to set a custom secret_salt in config.yaml before going live."
}

main "$@"

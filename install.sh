#!/bin/bash

# Server Scraper Installation Script
# Usage: curl -fsSL https://raw.githubusercontent.com/vdohide-core/server-scraper/main/install.sh | sudo -E bash -s -- [OPTIONS]

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Defaults
HTTP_PORT="8081"
UNINSTALL=false
MONGODB_URI=""

APP_NAME="server-scraper"
APP_DIR="/opt/$APP_NAME"
SERVICE_NAME="server-scraper"
GITHUB_REPO="vdohide-core/server-scraper"
RELEASES_URL="https://github.com/$GITHUB_REPO/releases/latest/download"

print_status()  { echo -e "${GREEN}[INFO]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
print_error()   { echo -e "${RED}[ERROR]${NC} $1"; }

# Parse args
while [[ $# -gt 0 ]]; do
    case $1 in
        --uninstall)    UNINSTALL=true; shift ;;
        -p|--port)      HTTP_PORT="$2"; shift 2 ;;
        --mongodb-uri)  MONGODB_URI="$2"; shift 2 ;;
        -h|--help)
            echo "Server Scraper Installer"
            echo ""
            echo "Usage: curl -fsSL https://raw.githubusercontent.com/$GITHUB_REPO/main/install.sh | sudo -E bash -s -- [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  -p, --port PORT    HTTP port (default: 8081)"
            echo "  --mongodb-uri URI  MongoDB connection string (optional, for Google Drive OAuth)"
            echo "  --uninstall        Uninstall completely"
            echo "  -h, --help         Show this help"
            echo ""
            echo "Examples:"
            echo "  # Install"
            echo "  curl -fsSL https://raw.githubusercontent.com/$GITHUB_REPO/main/install.sh | sudo -E bash"
            echo ""
            echo "  # Install with custom port + MongoDB"
            echo "  curl -fsSL https://raw.githubusercontent.com/$GITHUB_REPO/main/install.sh | sudo -E bash -s -- \\"
            echo "      --port 8081 --mongodb-uri \"mongodb+srv://user:pass@host/db\""
            echo ""
            echo "  # Uninstall"
            echo "  curl -fsSL https://raw.githubusercontent.com/$GITHUB_REPO/main/install.sh | sudo bash -s -- --uninstall"
            exit 0 ;;
        *)
            print_error "Unknown option: $1"; exit 1 ;;
    esac
done

# ─── Uninstall ────────────────────────────────────────────────
if [ "$UNINSTALL" = true ]; then
    print_warning "⚠️  Starting Uninstallation..."
    systemctl stop "${SERVICE_NAME}"    2>/dev/null || true
    systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
    [ -f "/etc/systemd/system/${SERVICE_NAME}.service" ] && rm "/etc/systemd/system/${SERVICE_NAME}.service"
    systemctl daemon-reload
    [ -d "$APP_DIR" ] && rm -rf "$APP_DIR"
    print_status "✅ Uninstalled successfully!"
    exit 0
fi

# Check root
if [ "$(id -u)" -ne 0 ]; then
    print_error "This script must be run as root (use sudo)"
    exit 1
fi

print_status "🚀 Starting Installation..."
print_status "Configuration: Port=$HTTP_PORT"

# ─── System Dependencies ──────────────────────────────────────
print_status "Installing system dependencies (curl, chromium)..."
if command -v apt-get &>/dev/null; then
    apt-get update -qq
    apt-get install -y -qq curl chromium-browser 2>/dev/null || apt-get install -y -qq curl chromium 2>/dev/null || true
elif command -v yum &>/dev/null; then
    yum install -y curl chromium
elif command -v dnf &>/dev/null; then
    dnf install -y curl chromium
fi

# ─── Stop existing service ────────────────────────────────────
print_status "Stopping existing service..."
systemctl stop ${SERVICE_NAME} 2>/dev/null || true

# ─── Create app directory ─────────────────────────────────────
print_status "Creating app directory: $APP_DIR"
mkdir -p "$APP_DIR"
cd "$APP_DIR"

# ─── Download binary ──────────────────────────────────────────
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    BINARY="linux"
elif [ "$ARCH" = "aarch64" ]; then
    BINARY="linux-arm64"
else
    print_error "Unsupported architecture: $ARCH"
    exit 1
fi

print_status "Downloading binary ($BINARY) from latest release..."
curl -fsSL "$RELEASES_URL/$BINARY" -o "$APP_DIR/$APP_NAME"
chmod +x "$APP_DIR/$APP_NAME"
print_status "Binary downloaded."

# ─── Create .env ─────────────────────────────────────────────
print_status "Creating .env file..."
cat > "$APP_DIR/.env" <<EOF
HTTP_PORT=$HTTP_PORT
MONGODB_URI=$MONGODB_URI
EOF

# ─── Systemd service ──────────────────────────────────────────
print_status "Creating systemd service..."
cat > /etc/systemd/system/${SERVICE_NAME}.service <<EOF
[Unit]
Description=Server Scraper API
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$APP_DIR
ExecStart=$APP_DIR/$APP_NAME
Restart=always
RestartSec=5
EnvironmentFile=$APP_DIR/.env
Environment=HOME=/root

[Install]
WantedBy=multi-user.target
EOF

# ─── Enable & start ───────────────────────────────────────────
systemctl daemon-reload
systemctl enable ${SERVICE_NAME}
systemctl start  ${SERVICE_NAME}

# ─── Verify ───────────────────────────────────────────────────
sleep 2
if systemctl is-active --quiet ${SERVICE_NAME}; then
    echo ""
    echo "============================================"
    print_status "✅ Installation completed successfully!"
    echo "============================================"
    echo ""
    echo "  Service:  $SERVICE_NAME"
    echo "  Port:     $HTTP_PORT"
    echo ""
    echo "  Health:   http://localhost:$HTTP_PORT/health"
    echo "  Scraper:  http://localhost:$HTTP_PORT/scraper?url=<URL>"
    echo "  Parsers:  http://localhost:$HTTP_PORT/parsers"
    echo ""
    echo "  Commands:"
    echo "    systemctl status $SERVICE_NAME"
    echo "    systemctl restart $SERVICE_NAME"
    echo "    journalctl -u $SERVICE_NAME -f"
    echo "    Uninstall: curl -fsSL https://raw.githubusercontent.com/$GITHUB_REPO/main/install.sh | sudo bash -s -- --uninstall"
    echo "============================================"
else
    print_error "❌ Application failed to start. Check logs:"
    journalctl -u ${SERVICE_NAME} --no-pager
    exit 1
fi
